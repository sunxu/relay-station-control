#!/usr/bin/env python3
"""Bounded, protected HTTP acceptance harness for Control Directory/Binding."""
import argparse, json, os, re, signal, ssl, sys, tempfile, threading, time, uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from http.cookiejar import CookieJar
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, build_opener, HTTPSHandler, HTTPRedirectHandler, HTTPCookieProcessor, ProxyHandler

MAX_CONFIG = 128 * 1024
MAX_BODY = 4 * 1024 * 1024
MODES = ("read", "bind", "baseline", "assert-stale", "assert-recovered", "wait-stale", "wait-recovered", "directory-check", "data-plane")

class HarnessError(Exception): pass

@contextmanager
def _deadline(seconds):
    """Apply one wall-clock budget to connect, headers, and body reads."""
    if threading.current_thread() is not threading.main_thread():
        yield
        return
    old_handler = signal.getsignal(signal.SIGALRM)
    old_timer = signal.setitimer(signal.ITIMER_REAL, 0)
    def alarm(_signum, _frame): raise HarnessError("http_timeout")
    signal.signal(signal.SIGALRM, alarm)
    signal.setitimer(signal.ITIMER_REAL, seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, old_handler)
        if old_timer[0] > 0:
            signal.setitimer(signal.ITIMER_REAL, old_timer[0], old_timer[1])

def _protected(path, limit=MAX_CONFIG):
    p = Path(path)
    if not p.is_absolute() or any(x.is_symlink() for x in (p, *p.parents)) or not p.is_file(): raise HarnessError("protected_file_invalid")
    if p.stat().st_uid != os.getuid() or p.stat().st_mode & 0o077: raise HarnessError("protected_file_permissions")
    parent = p.parent
    if parent.is_symlink(): raise HarnessError("protected_file_invalid")
    # The containing private directory must be owner-only. System ancestors
    # such as /tmp are intentionally outside this check.
    if parent.stat().st_uid != os.getuid() or parent.stat().st_mode & 0o077: raise HarnessError("protected_file_permissions")
    if p.stat().st_size < 1 or p.stat().st_size > limit: raise HarnessError("protected_file_size")
    return p.read_bytes()

def _json_file(path):
    try: return json.loads(_protected(path).decode())
    except (UnicodeDecodeError, json.JSONDecodeError): raise HarnessError("config_invalid")

def _utc(value):
    if not isinstance(value, str): raise HarnessError("timestamp_invalid")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if parsed.tzinfo is None: raise ValueError
        return parsed.astimezone(timezone.utc)
    except ValueError: raise HarnessError("timestamp_invalid")

def _same_utc(a, b):
    if a is None or b is None: return a == b
    return _utc(a) == _utc(b)

def _url(base, path):
    from urllib.parse import urlsplit, urlunsplit
    u = urlsplit(base)
    if u.scheme not in ("http", "https") or not u.netloc or u.query or u.fragment or u.username:
        raise HarnessError("url_invalid")
    return urlunsplit((u.scheme, u.netloc, path, "", ""))

class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl): raise HarnessError("redirect_rejected")

class Client:
    def __init__(self, cfg):
        self.cfg = cfg
        self.base = cfg["control_url"].rstrip("/")
        self.timeout = float(cfg.get("timeout_seconds", 10))
        if not 1 <= self.timeout <= 30: raise HarnessError("timeout_invalid")
        # Acceptance must use the explicitly configured endpoint.  In particular,
        # do not inherit HTTP(S)_PROXY from a developer shell.
        handlers = [ProxyHandler({}), NoRedirect()]
        if self.base.startswith("https://"):
            context = ssl.create_default_context()
            if cfg.get("ca_file"):
                context.load_verify_locations(cafile=str(Path(cfg["ca_file"])))
            handlers.append(HTTPSHandler(context=context))
        self.cookies = CookieJar()
        handlers.append(HTTPCookieProcessor(self.cookies))
        self.opener = build_opener(*handlers)
        self.csrf = None

    def call(self, method, path, payload=None, csrf=False, expected=None):
        body = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
        headers = {"Accept": "application/json"}
        if body is not None: headers["Content-Type"] = "application/json"
        if csrf:
            if not self.csrf: self.session()
            headers["X-CSRF-Token"] = self.csrf
        req = Request(_url(self.base, path), data=body, headers=headers, method=method)
        try:
            with _deadline(self.timeout):
                with self.opener.open(req, timeout=self.timeout) as resp:
                    raw = resp.read(MAX_BODY + 1)
                    if len(raw) > MAX_BODY: raise HarnessError("response_too_large")
                    status = resp.status
        except HTTPError as exc:
            try:
                with _deadline(self.timeout): raw = exc.read(MAX_BODY + 1)
                status = exc.code
            finally:
                exc.close()
        except (URLError, OSError, TimeoutError, HarnessError) as exc:
            if isinstance(exc, HarnessError): raise
            raise HarnessError("http_unavailable") from exc
        if len(raw) > MAX_BODY: raise HarnessError("response_too_large")
        try: data = json.loads(raw.decode()) if raw else {}
        except (UnicodeDecodeError, json.JSONDecodeError): raise HarnessError("json_invalid")
        if expected and status not in expected: raise HarnessError("unexpected_http_status")
        return status, data

    def session(self):
        cookie_output = self.cfg.get("session_cookie_file")
        cookie = cookie_output if cookie_output and Path(cookie_output).exists() else None
        session_cookie_name = None
        if cookie:
            value = _protected(cookie, 8192).decode().strip()
            if not value: raise HarnessError("session_invalid")
            # Accept either a complete `name=value` Cookie file or the legacy
            # value-only file.  The file itself remains protected and is never
            # included in result/error output.
            if "=" in value:
                name, value = value.split("=", 1)
            else:
                name = self.cfg.get("session_cookie_name", "__Host-relay_control_session")
            if not name or not value: raise HarnessError("session_invalid")
            # Preserve the exact cookie identity supplied by the protected
            # session file.  This matters for an existing non-default cookie
            # name (and avoids rewriting a valid session under another name).
            session_cookie_name = name
            from http.cookiejar import Cookie
            from urllib.parse import urlsplit
            host = urlsplit(self.base).hostname
            self.cookies.set_cookie(Cookie(
                version=0, name=name, value=value, port=None, port_specified=False,
                domain=host, domain_specified=True, domain_initial_dot=False,
                path="/", path_specified=True, secure=self.base.startswith("https://"),
                expires=None, discard=True, comment=None, comment_url=None,
                rest={}, rfc2109=False,
            ))
        def login():
            if not self.cfg.get("password_file") or not self.cfg.get("login_name"):
                raise HarnessError("credentials_required")
            password = _protected(self.cfg["password_file"], 4096).decode().rstrip("\n")
            status, data = self.call("POST", "/api/auth/login", {"login_name": self.cfg["login_name"], "password": password}, expected={200})
            if data.get("state") == "mfa_required":
                if not self.cfg.get("mfa_code_file") or not Path(self.cfg["mfa_code_file"]).exists():
                    raise HarnessError("mfa_code_required")
                code = _protected(self.cfg["mfa_code_file"], 256).decode().strip()
                status, data = self.call("POST", "/api/auth/mfa", {"method": self.cfg.get("mfa_method", "totp"), "code": code}, expected={200})
            if data.get("state") != "authenticated": raise HarnessError("authentication_failed")
        logged_in = not cookie
        if not cookie:
            login()
        if cookie:
            # Only an explicit 401 means that the persisted session is stale.
            # Network, redirect, timeout, and malformed-response failures are
            # surfaced directly and never trigger a second login attempt.
            status, data = self.call("GET", "/api/auth/session", expected={200, 401})
        else:
            status, data = self.call("GET", "/api/auth/session", expected={200})
        if status == 401:
            if not cookie or not self.cfg.get("password_file"):
                raise HarnessError("authentication_failed")
            self.cookies.clear()
            login()
            logged_in = True
            status, data = self.call("GET", "/api/auth/session", expected={200})
        self.csrf = data.get("csrf_token")
        if not isinstance(self.csrf, str) or len(self.csrf) < 32: raise HarnessError("csrf_missing")
        if logged_in and cookie_output:
            from http.cookiejar import eff_request_host
            host = eff_request_host(Request(self.base))[1]
            name = session_cookie_name or self.cfg.get("session_cookie_name", "__Host-relay_control_session" if self.base.startswith("https://") else "relay_control_session")
            for item in self.cookies:
                if item.domain == host and item.name == name:
                    _write_secret(cookie_output, item.name + "=" + item.value + "\n")
                    break
        return data

def validate_persistent_config(path, cfg):
    workspace = Path(__file__).resolve().parents[3]
    temporary = tuple(Path(value) for value in ("/Volumes/DevRAM", "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"))
    references = [("config", path)] + [(key, value) for key, value in cfg.items() if key.endswith("_file")]
    for key, value in references:
        if not isinstance(value, (str, Path)):
            raise HarnessError("protected_file_invalid")
        candidate = Path(value)
        if not candidate.is_absolute() or any(part.is_symlink() for part in (candidate, *candidate.parents)):
            raise HarnessError("protected_file_invalid")
        if candidate.is_relative_to(workspace) or any(candidate.is_relative_to(base) for base in temporary):
            raise HarnessError("persistent_config_required")
        parent = candidate.parent
        if not parent.is_dir() or parent.stat().st_uid != os.getuid() or parent.stat().st_mode & 0o077:
            raise HarnessError("protected_directory_permissions")
        if candidate.exists():
            info = candidate.stat()
            forbidden = 0o022 if key.endswith("ca_file") else 0o077
            if not candidate.is_file() or info.st_uid != os.getuid() or info.st_mode & forbidden:
                raise HarnessError("protected_file_permissions")
            if not 0 < info.st_size <= MAX_CONFIG:
                raise HarnessError("protected_file_size")

def _cfg(path):
    cfg = _json_file(path)
    required = ("control_url", "gateway_instance_id", "node_instance_id", "gateway_account_id")
    if not isinstance(cfg, dict) or any(not isinstance(cfg.get(k), str) or not cfg[k] for k in required): raise HarnessError("config_invalid")
    for key in ("gateway_instance_id", "node_instance_id"):
        try: uuid.UUID(cfg[key])
        except (ValueError, AttributeError): raise HarnessError("config_invalid")
    aid = cfg["gateway_account_id"]
    if not re.fullmatch(r"[1-9][0-9]{0,18}", aid) or int(aid) > 9223372036854775807: raise HarnessError("gateway_account_id_invalid")
    return cfg

def _read(c):
    if not c.csrf:
        c.session()
    data = c.call("GET", "/api/relay-bindings/nodes/" + c.cfg["node_instance_id"], expected={200})[1]
    if not isinstance(data, dict) or data.get("relay_node_id") != c.cfg["node_instance_id"]:
        raise HarnessError("binding_identity_mismatch")
    binding = data.get("current_binding")
    if binding is not None:
        if not isinstance(binding, dict): raise HarnessError("binding_contract_invalid")
        for key in ("relay_node_id", "gateway_instance_id", "gateway_account_id"):
            if binding.get(key) != (c.cfg["node_instance_id"] if key == "relay_node_id" else c.cfg[key]) or data.get(key) != binding.get(key):
                raise HarnessError("binding_identity_mismatch")
    return data

def _binding_id(data):
    b = data.get("current_binding")
    return b.get("binding_id") if isinstance(b, dict) else None

def run(mode, cfg, _client=None):
    c = _client or Client(cfg)
    if mode == "read": return _read(c)
    if mode == "bind":
        data = _read(c); current = data.get("current_binding")
        if current:
            if (current.get("gateway_account_id") != cfg["gateway_account_id"] or
                current.get("gateway_instance_id") != cfg["gateway_instance_id"]):
                raise HarnessError("binding_conflict")
            return {"result": "already_bound", "binding_id": _binding_id(data)}
        try:
            status, result = c.call("POST", "/api/relay-bindings/bind", {"relay_node_id": cfg["node_instance_id"], "gateway_instance_id": cfg["gateway_instance_id"], "gateway_account_id": cfg["gateway_account_id"]}, csrf=True, expected={200})
            observed = _read(c)
            if not _binding_id(observed): raise HarnessError("mutation_unknown")
            return observed
        except HarnessError as original:
            # A lost response must never trigger a second mutation. Re-read the
            # exact target and report reconciliation only when all identities match.
            try:
                observed = _read(c)
                binding = observed.get("current_binding")
                if (isinstance(binding, dict) and binding.get("gateway_account_id") == cfg["gateway_account_id"] and
                    binding.get("relay_node_id", observed.get("relay_node_id")) == cfg["node_instance_id"] and
                    binding.get("gateway_instance_id", observed.get("gateway_instance_id")) == cfg["gateway_instance_id"]):
                    return {"result": "reconciled", "binding_id": binding.get("binding_id")}
            except HarnessError:
                pass
            raise HarnessError("mutation_unknown") from original
    if mode == "baseline":
        data = _read(c)
        if not _binding_id(data) or data.get("directory_freshness") != "fresh" or data.get("resolution") != "resolved" or not data.get("last_success_observation_at"):
            raise HarnessError("baseline_target_not_ready")
        _utc(data["last_success_observation_at"])
        baseline = {"gateway_instance_id": cfg["gateway_instance_id"], "node_instance_id": cfg["node_instance_id"], "gateway_account_id": cfg["gateway_account_id"], "control_url": cfg["control_url"], "binding_id": _binding_id(data), "last_success_observation_at": data["last_success_observation_at"], "captured_at": datetime.now(timezone.utc).isoformat()}
        _write(cfg["baseline_file"], baseline); return baseline
    if mode in ("assert-stale", "assert-recovered"):
        data = _read(c); baseline = _json_file(cfg["baseline_file"])
        for key in ("gateway_instance_id", "node_instance_id", "gateway_account_id", "control_url"):
            if baseline.get(key) != c.cfg.get(key): raise HarnessError("baseline_identity_mismatch")
        if _binding_id(data) != baseline.get("binding_id"): raise HarnessError("baseline_identity_mismatch")
        fresh = data.get("directory_freshness")
        if mode == "assert-stale":
            if fresh != "stale" or data.get("resolution") != "unknown" or not _same_utc(data.get("last_success_observation_at"), baseline.get("last_success_observation_at")): raise HarnessError("stale_assertion_failed")
        else:
            if fresh != "fresh" or data.get("resolution") != "resolved": raise HarnessError("recovered_not_fresh")
            if not data.get("last_success_observation_at") or _utc(data["last_success_observation_at"]) <= _utc(baseline["last_success_observation_at"]): raise HarnessError("observation_not_advanced")
        return data
    if mode in ("wait-stale", "wait-recovered"):
        wait_timeout = float(cfg.get("wait_timeout_seconds", 600))
        poll_interval = float(cfg.get("poll_interval_seconds", 10))
        if not 1 <= wait_timeout <= 900 or not 1 <= poll_interval <= 30:
            raise HarnessError("wait_config_invalid")
        deadline = time.monotonic() + wait_timeout
        c.session()
        target = "assert-stale" if mode == "wait-stale" else "assert-recovered"
        while time.monotonic() < deadline:
            try: return run(target, cfg, c)
            except HarnessError as exc:
                if str(exc) not in ("stale_assertion_failed", "recovered_not_fresh", "observation_not_advanced", "http_unavailable", "unexpected_http_status"):
                    raise
                print(json.dumps({"mode": mode, "result": "polling"}, separators=(",", ":")), flush=True)
                time.sleep(min(poll_interval, max(0.1, deadline-time.monotonic())))
        raise HarnessError("wait_timeout")
    if mode == "directory-check":
        return _directory_check(cfg, c.timeout)
    if mode == "data-plane":
        url = cfg.get("data_plane_url")
        if not url: raise HarnessError("data_plane_config_missing")
        payload = _json_file(cfg["data_plane_request_file"]) if cfg.get("data_plane_request_file") else {"model": cfg.get("data_plane_model", "gpt-test"), "messages": [{"role": "user", "content": "health"}], "stream": False}
        token = None
        if cfg.get("data_plane_token_file"):
            token = _protected(cfg["data_plane_token_file"], 8192).decode().strip()
            if not token: raise HarnessError("data_plane_token_invalid")
        status, data = c.call("POST", url, payload, expected={200}) if url.startswith("/") else _external_call(url, payload, c.timeout, cfg.get("data_plane_ca_file", cfg.get("ca_file")), "POST", token)
        if status != 200 or not isinstance(data, dict): raise HarnessError("data_plane_invalid")
        choices, output = data.get("choices"), data.get("output")
        if not ((isinstance(choices, list) and choices and all(isinstance(x, dict) and isinstance(x.get("message"), dict) for x in choices)) or
                (isinstance(output, list) and output and all(isinstance(x, dict) and isinstance(x.get("type"), str) for x in output))):
            raise HarnessError("data_plane_invalid")
        return {"result": "data_plane", "status": status, "response_shape": "object"}
    raise HarnessError("invalid_mode")

def _external_call(url, payload, timeout, ca_file=None, method="POST", token=None, return_headers=False):
    from urllib.parse import urlsplit
    u = urlsplit(url)
    if u.scheme not in ("http", "https") or not u.netloc or u.username or u.query or u.fragment:
        raise HarnessError("url_invalid")
    body = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token is not None: headers["Authorization"] = "Bearer " + token
    req = Request(url, data=body, headers=headers, method=method)
    handlers = [ProxyHandler({}), NoRedirect()]
    if u.scheme == "https":
        context = ssl.create_default_context()
        if ca_file: context.load_verify_locations(cafile=str(Path(ca_file)))
        handlers.append(HTTPSHandler(context=context))
    try:
        with _deadline(timeout):
            with build_opener(*handlers).open(req, timeout=timeout) as resp:
                raw = resp.read(MAX_BODY + 1)
                if len(raw) > MAX_BODY: raise HarnessError("response_too_large")
                status = resp.status
                response_headers = {k.lower(): v for k, v in resp.headers.items()}
    except HTTPError as exc:
        try:
            with _deadline(timeout): raw = exc.read(MAX_BODY + 1)
            status = exc.code
            response_headers = {k.lower(): v for k, v in exc.headers.items()}
        finally:
            exc.close()
    except (URLError, OSError, TimeoutError, HarnessError) as exc:
        if isinstance(exc, HarnessError): raise
        raise HarnessError("http_unavailable") from exc
    if len(raw) > MAX_BODY: raise HarnessError("response_too_large")
    if status >= 400:
        # Error routes may intentionally return a proxy HTML body. Only their
        # bounded status/headers are acceptance evidence; never retain body.
        data = {}
    else:
        try: data = json.loads(raw.decode()) if raw else {}
        except (UnicodeDecodeError, json.JSONDecodeError): raise HarnessError("json_invalid")
    return (status, data, response_headers) if return_headers else (status, data)

def _directory_check(cfg, timeout):
    url = cfg.get("directory_url")
    token_file = cfg.get("directory_token_file")
    if (not isinstance(url, str) or not url or not isinstance(token_file, str) or
        not isinstance(cfg.get("directory_unknown_url"), str) or not isinstance(cfg.get("directory_public_url"), str)):
        raise HarnessError("directory_config_missing")
    token = _protected(token_file, 8192).decode().strip()
    if not token: raise HarnessError("directory_token_invalid")
    directory_ca = cfg.get("directory_ca_file", cfg.get("ca_file"))
    status, data, headers = _external_call(url, None, timeout, directory_ca, "GET", token, True)
    if status != 200 or not isinstance(data, dict): raise HarnessError("directory_read_failed")
    if type(data.get("schema_version")) is not int or data["schema_version"] != 1: raise HarnessError("directory_source_contract_invalid")
    if headers.get("cache-control", "").lower() != "no-store": raise HarnessError("directory_cache_control_invalid")
    # The source v1 response is deliberately inspected as JSON without coercing
    # its numeric account ID through Python/JS-style floating point.
    if "gateway_account_id" in data and type(data["gateway_account_id"]) is not int:
        raise HarnessError("directory_source_contract_invalid")
    if "application/json" not in headers.get("content-type", "").lower():
        raise HarnessError("directory_content_type_invalid")
    accounts = data.get("accounts")
    if not isinstance(accounts, list): raise HarnessError("directory_source_contract_invalid")
    for account in accounts:
        if (not isinstance(account, dict) or type(account.get("id")) is not int or
            not 1 <= account["id"] <= 9223372036854775807):
            raise HarnessError("directory_source_contract_invalid")
    result = {"result": "directory_check", "status": status, "source_schema_version": data.get("schema_version")}
    checks = {"authorized_read": True}
    for method, key, suffix, expected, label, supplied in (
        ("GET", "directory_missing_token_url", "", 401, "missing_token", None),
        ("GET", "directory_wrong_token_url", "", 401, "wrong_token", "invalid"),
        ("POST", "directory_post_url", "", 405, "post_rejected", token),
    ):
        s, _ = _external_call(cfg.get(key, url + suffix), None, timeout, directory_ca, method, supplied)
        if s != expected: raise HarnessError("directory_" + label + "_failed")
        checks[label] = True
    for key, suffix, expected, label, supplied in (
        ("directory_unknown_url", "/internal/unknown", 404, "unknown_internal", token),
        ("directory_public_url", "/public", 403, "public_rejected", None),
    ):
        s, _ = _external_call(cfg.get(key, url.rstrip("/") + suffix), None, timeout, directory_ca, "GET", supplied)
        if s != expected: raise HarnessError("directory_" + label + "_failed")
        checks[label] = True
    result["checks"] = checks
    return result

def _write(path, data):
    p = Path(path)
    if not p.is_absolute() or p.is_symlink(): raise HarnessError("output_path_invalid")
    p.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=p.name+".", dir=p.parent, text=True)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w") as f: json.dump(data, f, separators=(",", ":")); f.write("\n"); f.flush(); os.fsync(f.fileno())
        os.replace(tmp, p)
    finally:
        if os.path.exists(tmp): os.unlink(tmp)

def _write_secret(path, value):
    p = Path(path)
    if not p.is_absolute() or p.is_symlink(): raise HarnessError("output_path_invalid")
    p.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=p.name+".", dir=p.parent, text=True)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w") as f:
            f.write(value); f.flush(); os.fsync(f.fileno())
        os.replace(tmp, p)
    finally:
        if os.path.exists(tmp): os.unlink(tmp)

def main(argv=None):
    parser = argparse.ArgumentParser(add_help=True); parser.add_argument("--config", required=True); parser.add_argument("mode", nargs="?", default="read", choices=MODES)
    args = parser.parse_args(argv)
    try:
        cfg = _cfg(args.config)
        validate_persistent_config(args.config, cfg)
        run(args.mode, cfg)
        print(json.dumps({"mode": args.mode, "result": "success"}, separators=(",", ":")))
        return 0
    except HarnessError as exc:
        print(json.dumps({"mode": args.mode, "result": "failed", "reason": str(exc)}, separators=(",", ":")), file=sys.stderr); return 1
    except Exception:
        print(json.dumps({"mode": args.mode, "result": "failed", "reason": "internal_error"}, separators=(",", ":")), file=sys.stderr); return 1

if __name__ == "__main__": sys.exit(main())

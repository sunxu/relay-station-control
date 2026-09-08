import contextlib
import io
import json
import os
import stat
import ssl
import subprocess
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest import mock

import directory_http as harness


class AcceptanceHandler(BaseHTTPRequestHandler):
    requests = []
    bind_bodies = []
    account = 9007199254740993
    binding_none = False
    lose_bind_response = False
    binding_available_after_lost = False
    fail_reconcile_read = False
    auth_mode = "normal"
    binding_id = "00000000-0000-0000-0000-000000000003"
    slow_session = False

    def log_message(self, *_):
        pass

    def handle(self):
        try:
            super().handle()
        except (BrokenPipeError, ConnectionResetError):
            # Deadline tests deliberately close the client socket mid-response.
            pass

    def _send(self, status, body, headers=None):
        raw = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        cookie = self.headers.get("Cookie", "")
        auth = self.headers.get("Authorization")
        self.__class__.requests.append(("GET", self.path, cookie, auth))
        if self.path == "/api/auth/session":
            if self.__class__.slow_session:
                time.sleep(self.__class__.slow_session)
            if self.__class__.auth_mode == "always401" or (self.__class__.auth_mode == "expired" and "session-value" not in cookie):
                return self._send(401, {"error": "unauthorized"})
            if "__Host-relay_control_session=session-value" not in cookie:
                return self._send(401, {"error": "unauthorized"})
            return self._send(200, {"authenticated": True, "csrf_token": "x" * 40})
        if self.path == "/redirect":
            self.send_response(302); self.send_header("Location", "/api/auth/session"); self.end_headers(); return
        if self.path == "/oversized":
            self.send_response(200); self.send_header("Content-Length", str(harness.MAX_BODY + 1)); self.end_headers(); self.wfile.write(b"x" * (harness.MAX_BODY + 1)); return
        if self.path == "/slowdrip":
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers()
            for chunk in (b'{"x":', b'1', b'}'):
                self.wfile.write(chunk); self.wfile.flush(); time.sleep(0.6)
            return
        if self.path == "/slow-error":
            time.sleep(0.5)
            self.send_response(503); self.send_header("Content-Type", "application/json"); self.end_headers()
            for chunk in (b'{"error":', b'"busy"', b'}'):
                self.wfile.write(chunk); self.wfile.flush(); time.sleep(0.6)
            return
        if self.path.startswith("/api/relay-bindings/nodes/"):
            if self.__class__.fail_reconcile_read:
                return self._send(503, {"error": "unavailable"})
            if self.__class__.binding_none:
                return self._send(200, {"relay_node_id": "00000000-0000-0000-0000-000000000002", "current_binding": None, "directory_freshness": "unknown", "resolution": "unbound"})
            return self._send(200, {
                "relay_node_id": "00000000-0000-0000-0000-000000000002",
                "gateway_instance_id": "00000000-0000-0000-0000-000000000001",
                "gateway_account_id": str(self.account),
                "current_binding": {
                    "binding_id": self.__class__.binding_id,
                    "gateway_instance_id": "00000000-0000-0000-0000-000000000001",
                    "relay_node_id": "00000000-0000-0000-0000-000000000002",
                    "gateway_account_id": str(self.account),
                },
                "last_success_observation_at": "2026-09-08T01:02:03Z",
                "directory_freshness": "fresh",
                "resolution": "resolved",
            })
        if self.path.startswith("/directory"):
            if self.path == "/directory/public":
                return self._send(403, {"error": "forbidden"})
            if auth != "Bearer reader-token":
                return self._send(401, {"error": "unauthorized"})
            if self.path in ("/directory/wrong-token", "/directory?wrong_token=1"):
                return self._send(401, {"error": "unauthorized"})
            if self.path == "/directory/internal/unknown":
                return self._send(404, {"error": "not_found"})
            return self._send(200, {
                "schema_version": 1,
                "accounts": [{"id": self.account}],
            }, {"Cache-Control": "no-store"})
        if self.path == "/chat/completions":
            return self._send(200, {"choices": [{"message": {"role": "assistant", "content": "OK"}}]})
        return self._send(404, {"error": "not_found"})

    def do_POST(self):
        self.__class__.requests.append(("POST", self.path, self.headers.get("Cookie", ""), self.headers.get("Authorization")))
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length else b""
        if self.path == "/api/relay-bindings/bind":
            self.__class__.bind_bodies.append(body)
        if self.path == "/api/auth/login":
            return self._send(200, {"state": "mfa_required"})
        if self.path == "/api/auth/mfa":
            return self._send(200, {"state": "authenticated"}, {"Set-Cookie": "__Host-relay_control_session=session-value; Path=/"})
        if self.path == "/chat/completions":
            return self._send(200, {"choices": [{"message": {"role": "assistant", "content": "OK"}}]})
        if self.path == "/api/relay-bindings/bind" and self.__class__.lose_bind_response:
            self.__class__.fail_reconcile_read = not self.__class__.binding_available_after_lost
            self.__class__.binding_none = not self.__class__.binding_available_after_lost
            self.close_connection = True
            self.connection.shutdown(2)
            return
        if self.path == "/api/relay-bindings/bind":
            return self._send(200, {"result": "bound"})
        if self.path == "/directory":
            return self._send(405, {"error": "method_not_allowed"})
        return self._send(404, {"error": "not_found"})


class DirectoryHTTPTests(unittest.TestCase):
    def setUp(self):
        AcceptanceHandler.requests = []
        AcceptanceHandler.bind_bodies = []
        AcceptanceHandler.account = 9007199254740993
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), AcceptanceHandler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        os.chmod(self.root, stat.S_IRWXU)
        self.cookie = self.root / "session.cookie"
        self.cookie.write_text("__Host-relay_control_session=session-value\n")
        self.token = self.root / "reader.token"
        self.token.write_text("reader-token\n")
        self.data_plane_token = self.root / "data-plane.token"
        self.data_plane_token.write_text("data-plane-secret\n")
        for p in (self.cookie, self.token, self.data_plane_token): os.chmod(p, stat.S_IRUSR | stat.S_IWUSR)
        self.config_file = self.root / "config.json"
        self.baseline = self.root / "baseline.json"
        self.cfg = {
            "control_url": "http://127.0.0.1:%d" % self.server.server_port,
            "gateway_instance_id": "00000000-0000-0000-0000-000000000001",
            "node_instance_id": "00000000-0000-0000-0000-000000000002",
            "gateway_account_id": "9007199254740993",
            "session_cookie_file": str(self.cookie),
            "directory_url": "http://127.0.0.1:%d/directory" % self.server.server_port,
            "directory_token_file": str(self.token),
            "directory_unknown_url": "http://127.0.0.1:%d/directory/internal/unknown" % self.server.server_port,
            "directory_public_url": "http://127.0.0.1:%d/directory/public" % self.server.server_port,
            "data_plane_url": "http://127.0.0.1:%d/chat/completions" % self.server.server_port,
            "data_plane_token_file": str(self.data_plane_token),
            "baseline_file": str(self.baseline),
        }
        self.config_file.write_text(json.dumps(self.cfg))
        os.chmod(self.config_file, stat.S_IRUSR | stat.S_IWUSR)

    def tearDown(self):
        AcceptanceHandler.binding_none = False
        AcceptanceHandler.lose_bind_response = False
        AcceptanceHandler.binding_available_after_lost = False
        AcceptanceHandler.fail_reconcile_read = False
        AcceptanceHandler.binding_id = "00000000-0000-0000-0000-000000000003"
        AcceptanceHandler.slow_session = False
        AcceptanceHandler.auth_mode = "normal"
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.tmp.cleanup()

    def test_config_requires_decimal_string_and_preserves_large_id(self):
        for value in ("9007199254740991", "9007199254740992", "9007199254740993", "9223372036854775807"):
            self.cfg["gateway_account_id"] = value
            self.config_file.write_text(json.dumps(self.cfg))
            self.assertEqual(harness._cfg(str(self.config_file))["gateway_account_id"], value)
        self.cfg["gateway_account_id"] = "9223372036854775808"
        self.config_file.write_text(json.dumps(self.cfg))
        with self.assertRaises(harness.HarnessError): harness._cfg(str(self.config_file))
        self.cfg["gateway_account_id"] = 9007199254740993
        self.config_file.write_text(json.dumps(self.cfg))
        with self.assertRaises(harness.HarnessError): harness._cfg(str(self.config_file))

    def test_read_reuses_protected_cookie_and_does_not_mutate(self):
        before = len(AcceptanceHandler.requests)
        result = harness.run("read", harness._cfg(str(self.config_file)))
        self.assertEqual(result["current_binding"]["gateway_account_id"], "9007199254740993")
        self.assertEqual(len(AcceptanceHandler.requests), before + 2)
        self.assertEqual(AcceptanceHandler.requests[-1][2], "__Host-relay_control_session=session-value")
        self.assertNotIn("POST", [r[0] for r in AcceptanceHandler.requests])

    def test_directory_check_covers_auth_method_routes_and_source_v1(self):
        result = harness.run("directory-check", harness._cfg(str(self.config_file)))
        self.assertEqual(result["source_schema_version"], 1)
        self.assertEqual(set(result["checks"]), {"authorized_read", "missing_token", "wrong_token", "post_rejected", "unknown_internal", "public_rejected"})

    def test_baseline_uses_utc_and_recovered_requires_advance(self):
        cfg = harness._cfg(str(self.config_file))
        baseline = harness.run("baseline", cfg)
        self.assertEqual(baseline["gateway_account_id"], "9007199254740993")
        with self.assertRaises(harness.HarnessError): harness.run("assert-recovered", cfg)

    def test_baseline_rejects_wrong_target_and_does_not_change_on_failed_assertion(self):
        cfg = harness._cfg(str(self.config_file))
        harness.run("baseline", cfg)
        before = self.baseline.read_bytes()
        wrong = json.loads(before)
        wrong["node_instance_id"] = "00000000-0000-0000-0000-000000000003"
        harness._write(str(self.baseline), wrong)
        with self.assertRaisesRegex(harness.HarnessError, "baseline_identity_mismatch"):
            harness.run("assert-stale", cfg)
        self.assertEqual(self.baseline.read_bytes(), json.dumps(wrong, separators=(",", ":")).encode() + b"\n")
        self.assertNotEqual(before, self.baseline.read_bytes())

    def test_same_binding_is_noop_and_different_identity_is_rejected(self):
        cfg = harness._cfg(str(self.config_file))
        self.assertEqual(harness.run("bind", cfg), {"result": "already_bound", "binding_id": "00000000-0000-0000-0000-000000000003"})
        self.assertNotIn("POST", [r[0] for r in AcceptanceHandler.requests])
        self.cfg["gateway_instance_id"] = "00000000-0000-0000-0000-000000000003"
        self.config_file.write_text(json.dumps(self.cfg))
        with self.assertRaises(harness.HarnessError): harness.run("bind", harness._cfg(str(self.config_file)))

    def test_lost_bind_response_reconciles_once_or_reports_unknown(self):
        cfg = harness._cfg(str(self.config_file))
        AcceptanceHandler.binding_none = True
        AcceptanceHandler.lose_bind_response = True
        # The fixture cannot persist a just-created binding after closing the
        # socket, so the first case proves the one-POST unknown path.
        with self.assertRaises(harness.HarnessError): harness.run("bind", cfg)
        self.assertEqual([r[0] for r in AcceptanceHandler.requests].count("POST"), 1)
        AcceptanceHandler.requests = []
        AcceptanceHandler.fail_reconcile_read = False
        with self.assertRaises(harness.HarnessError): harness.run("bind", cfg)
        self.assertEqual([r[0] for r in AcceptanceHandler.requests].count("POST"), 1)

    def test_lost_bind_response_reconciles_exact_binding_once(self):
        cfg = harness._cfg(str(self.config_file))
        AcceptanceHandler.binding_none = True
        AcceptanceHandler.lose_bind_response = True
        AcceptanceHandler.binding_available_after_lost = True
        result = harness.run("bind", cfg)
        self.assertEqual(result, {"result": "reconciled", "binding_id": "00000000-0000-0000-0000-000000000003"})
        self.assertEqual([r[0] for r in AcceptanceHandler.requests].count("POST"), 1)
        self.assertEqual(len(AcceptanceHandler.bind_bodies), 1)

    def test_bound_read_rejects_missing_or_malformed_binding_id(self):
        cfg = harness._cfg(str(self.config_file))
        for value in (None, "", "binding-1", 123):
            AcceptanceHandler.binding_id = value
            for mode in ("read", "bind"):
                with self.subTest(value=value, mode=mode):
                    with self.assertRaisesRegex(harness.HarnessError, "binding_contract_invalid"):
                        harness.run(mode, cfg)
        self.assertNotIn("POST", [row[0] for row in AcceptanceHandler.requests])

    def test_all_int64_values_are_read_and_request_serialized_as_strings(self):
        values = ("9007199254740991", "9007199254740992", "9007199254740993", "9223372036854775807")
        for value in values:
            self.cfg["gateway_account_id"] = value
            self.config_file.write_text(json.dumps(self.cfg))
            cfg = harness._cfg(str(self.config_file))
            AcceptanceHandler.account = int(value)
            self.assertEqual(harness.run("read", cfg)["gateway_account_id"], value)
            client = harness.Client(cfg)
            client.session()
            client.call("POST", "/api/relay-bindings/bind", {
                "relay_node_id": cfg["node_instance_id"],
                "gateway_instance_id": cfg["gateway_instance_id"],
                "gateway_account_id": value,
            }, csrf=True, expected={200})
            self.assertIn(b'"gateway_account_id":"' + value.encode() + b'"', AcceptanceHandler.bind_bodies[-1])
        self.cfg["gateway_account_id"] = "9223372036854775808"
        self.config_file.write_text(json.dumps(self.cfg))
        with self.assertRaises(harness.HarnessError): harness._cfg(str(self.config_file))
        AcceptanceHandler.account = 9007199254740993

    def test_naive_timestamps_are_rejected(self):
        with self.assertRaises(harness.HarnessError): harness._utc("2026-09-08T01:02:03")

    def test_expired_cookie_falls_back_to_login_mfa_once(self):
        password = self.root / "password"
        mfa = self.root / "mfa"
        password.write_text("password\n"); mfa.write_text("123456\n")
        os.chmod(password, 0o600); os.chmod(mfa, 0o600)
        self.cookie.write_text("__Host-relay_control_session=expired-value\n")
        self.cfg.update({"password_file": str(password), "login_name": "admin", "mfa_code_file": str(mfa)})
        self.config_file.write_text(json.dumps(self.cfg)); AcceptanceHandler.auth_mode = "expired"
        # expired session is rejected, then the one controlled login/MFA path succeeds
        self.assertEqual(harness.Client(harness._cfg(str(self.config_file))).session()["authenticated"], True)
        self.assertEqual([r[0] for r in AcceptanceHandler.requests].count("POST"), 2)
        self.assertEqual(self.cookie.read_text().strip(), "__Host-relay_control_session=session-value")
        before = len(AcceptanceHandler.requests)
        self.assertTrue(harness.Client(harness._cfg(str(self.config_file))).session()["authenticated"])
        self.assertEqual([r[0] for r in AcceptanceHandler.requests[before:]].count("POST"), 0)

    def test_missing_credentials_and_mfa_are_fixed_failures(self):
        self.cfg.pop("session_cookie_file")
        self.config_file.write_text(json.dumps(self.cfg))
        with self.assertRaisesRegex(harness.HarnessError, "credentials_required"):
            harness.Client(harness._cfg(str(self.config_file))).session()
        password = self.root / "password"; password.write_text("password\n"); os.chmod(password, 0o600)
        self.cfg.update({"password_file": str(password), "login_name": "admin"})
        self.config_file.write_text(json.dumps(self.cfg))
        with self.assertRaisesRegex(harness.HarnessError, "mfa_code_required"):
            harness.Client(harness._cfg(str(self.config_file))).session()

    def test_auth_failure_is_bounded_and_redirect_or_oversize_is_rejected(self):
        password = self.root / "password"
        mfa = self.root / "mfa"
        password.write_text("password\n"); mfa.write_text("123456\n"); os.chmod(password, 0o600); os.chmod(mfa, 0o600)
        self.cfg.pop("session_cookie_file"); self.cfg.update({"password_file": str(password), "login_name": "admin", "mfa_code_file": str(mfa)})
        self.config_file.write_text(json.dumps(self.cfg)); AcceptanceHandler.auth_mode = "always401"
        with self.assertRaises(harness.HarnessError): harness.Client(harness._cfg(str(self.config_file))).session()
        self.assertEqual([r[1] for r in AcceptanceHandler.requests].count("/api/auth/login"), 1)
        with self.assertRaises(harness.HarnessError): harness._external_call(self.cfg["control_url"] + "/redirect", None, 2, method="GET")
        with self.assertRaises(harness.HarnessError): harness._external_call(self.cfg["control_url"] + "/oversized", None, 2, method="GET")

    def test_directory_schema_and_account_shape_are_strict(self):
        cfg = harness._cfg(str(self.config_file))
        original = AcceptanceHandler.do_GET
        try:
            def invalid(self):
                if self.path == "/directory":
                    return self._send(200, {"schema_version": 0, "accounts": [{"id": True}]}, {"Cache-Control": "no-store"})
                return original(self)
            AcceptanceHandler.do_GET = invalid
            with self.assertRaises(harness.HarnessError): harness.run("directory-check", cfg)
        finally:
            AcceptanceHandler.do_GET = original

    def test_https_requires_trusted_ca_and_proxy_environment_is_ignored(self):
        cert, key = self.root / "server.pem", self.root / "server.key"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                        "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1",
                        "-addext", "basicConstraints=critical,CA:TRUE",
                        "-addext", "keyUsage=critical,keyCertSign,digitalSignature",
                        "-addext", "extendedKeyUsage=serverAuth",
                        "-keyout", str(key), "-out", str(cert)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        os.chmod(cert, 0o600); os.chmod(key, 0o600)
        server = ThreadingHTTPServer(("127.0.0.1", 0), AcceptanceHandler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(cert), str(key))
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        try:
            url = "https://127.0.0.1:%d/api/auth/session" % server.server_port
            with self.assertRaises(harness.HarnessError): harness._external_call(url, None, 2, method="GET")
            status, data = harness._external_call(url, None, 2, str(cert), "GET")
            self.assertEqual(status, 401)
            with mock.patch.dict(os.environ, {"HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1"}):
                status, _ = harness._external_call(self.cfg["control_url"] + "/api/auth/session", None, 2, method="GET")
                self.assertEqual(status, 401)
        finally:
            server.shutdown(); server.server_close(); thread.join()

    def test_stale_and_recovered_compare_utc_and_resolution(self):
        cfg = harness._cfg(str(self.config_file))
        baseline = {"gateway_instance_id": cfg["gateway_instance_id"], "node_instance_id": cfg["node_instance_id"], "gateway_account_id": cfg["gateway_account_id"], "control_url": cfg["control_url"], "binding_id": "00000000-0000-0000-0000-000000000003", "last_success_observation_at": "2026-09-08T01:00:00Z"}
        harness._write(cfg["baseline_file"], baseline)
        stale = {"relay_node_id": cfg["node_instance_id"], "current_binding": {"binding_id": "00000000-0000-0000-0000-000000000003", "relay_node_id": cfg["node_instance_id"], "gateway_instance_id": cfg["gateway_instance_id"], "gateway_account_id": cfg["gateway_account_id"]}, "gateway_account_id": cfg["gateway_account_id"], "directory_freshness": "stale", "resolution": "unknown", "last_success_observation_at": "2026-09-08T01:00:00+00:00"}
        recovered = dict(stale, directory_freshness="fresh", resolution="resolved", last_success_observation_at="2026-09-08T01:01:00+00:00")
        with mock.patch.object(harness, "_read", side_effect=[stale, recovered]):
            self.assertEqual(harness.run("assert-stale", cfg)["resolution"], "unknown")
            self.assertEqual(harness.run("assert-recovered", cfg)["resolution"], "resolved")

    def test_data_plane_requires_explicit_mode_and_keeps_only_response_shape(self):
        cfg = harness._cfg(str(self.config_file))
        client = harness.Client(cfg)
        client.session()
        result = harness.run("data-plane", cfg, client)
        self.assertEqual(result, {"result": "data_plane", "status": 200, "response_shape": "object"})
        self.assertEqual(AcceptanceHandler.requests[-1][3], "Bearer data-plane-secret")
        self.assertEqual(AcceptanceHandler.requests[-1][2], "")
        self.cfg["data_plane_url"] = "/chat/completions"
        self.config_file.write_text(json.dumps(self.cfg))
        relative_cfg = harness._cfg(str(self.config_file))
        before = len(AcceptanceHandler.requests)
        with mock.patch.object(harness, "_protected", side_effect=AssertionError("must not read token")):
            with self.assertRaisesRegex(harness.HarnessError, "url_invalid"):
                harness.run("data-plane", relative_cfg)
        self.assertEqual(len(AcceptanceHandler.requests), before)

    def test_lost_bind_response_with_malformed_reconciled_id_stays_unknown(self):
        cfg = harness._cfg(str(self.config_file))
        AcceptanceHandler.binding_none = True
        AcceptanceHandler.lose_bind_response = True
        AcceptanceHandler.binding_available_after_lost = True
        AcceptanceHandler.binding_id = "binding-1"
        with self.assertRaisesRegex(harness.HarnessError, "mutation_unknown"):
            harness.run("bind", cfg)
        self.assertEqual([r[0] for r in AcceptanceHandler.requests].count("POST"), 1)

    def test_slowdrip_obeys_single_total_deadline(self):
        started = time.monotonic()
        with self.assertRaises(harness.HarnessError) as caught:
            harness._external_call(self.cfg["control_url"] + "/slowdrip", None, 1, method="GET")
        self.assertEqual(str(caught.exception), "http_timeout")
        self.assertLess(time.monotonic() - started, 1.8)

    def test_wait_deadline_caps_control_body_read_and_reports_wait_timeout(self):
        client = harness.Client(harness._cfg(str(self.config_file)))
        started = time.monotonic()
        client.wait_deadline = started + 0.4
        with self.assertRaisesRegex(harness.HarnessError, "wait_timeout"):
            client.call("GET", "/slowdrip")
        self.assertLess(time.monotonic() - started, 0.9)

    def test_wait_deadline_is_not_reset_for_http_error_body(self):
        client = harness.Client(harness._cfg(str(self.config_file)))
        started = time.monotonic()
        client.wait_deadline = started + 0.6
        with self.assertRaisesRegex(harness.HarnessError, "wait_timeout"):
            client.call("GET", "/slow-error", expected={200})
        self.assertLess(time.monotonic() - started, 1.0)

    def test_run_wait_budget_covers_initial_session(self):
        cfg = harness._cfg(str(self.config_file))
        harness._write(cfg["baseline_file"], {
            "gateway_instance_id": cfg["gateway_instance_id"],
            "node_instance_id": cfg["node_instance_id"],
            "gateway_account_id": cfg["gateway_account_id"],
            "control_url": cfg["control_url"],
            "binding_id": "00000000-0000-0000-0000-000000000003",
            "last_success_observation_at": "2026-09-08T01:02:03Z",
        })
        cfg.update({"wait_timeout_seconds": 1, "timeout_seconds": 30})
        AcceptanceHandler.slow_session = 1.6
        started = time.monotonic()
        with self.assertRaisesRegex(harness.HarnessError, "wait_timeout"):
            harness.run("wait-stale", cfg)
        self.assertLess(time.monotonic() - started, 1.5)

    def test_main_stdout_and_stderr_are_fixed_shape_and_redacted(self):
        # The persistent-config policy intentionally rejects TemporaryDirectory
        # paths.  Patch only that policy here so the real HTTP read path and the
        # CLI's output boundary are exercised without creating a developer
        # configuration outside the test fixture.
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.object(harness, "validate_persistent_config"), \
             contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            self.assertEqual(harness.main(["--config", str(self.config_file), "read"]), 0)
        self.assertEqual(json.loads(stdout.getvalue()), {"mode": "read", "result": "success"})
        self.assertEqual(stderr.getvalue(), "")
        for secret in ("session-value", "reader-token", "9007199254740993"):
            self.assertNotIn(secret, stdout.getvalue())
            self.assertNotIn(secret, stderr.getvalue())

        AcceptanceHandler.auth_mode = "always401"
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.object(harness, "validate_persistent_config"), \
             contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            self.assertEqual(harness.main(["--config", str(self.config_file), "read"]), 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(json.loads(stderr.getvalue()), {"mode": "read", "result": "failed", "reason": "authentication_failed"})
        for secret in ("session-value", "reader-token", "9007199254740993"):
            self.assertNotIn(secret, stderr.getvalue())


if __name__ == "__main__":
    unittest.main()

export type ImportErrorCode = "invalidJson" | "invalidAccount" | "invalidProxy" | "duplicateIdentity" | "tooLarge" | "tooMany" | "forbiddenConfiguration";
export class AccountImportError extends Error {
  constructor(readonly code: ImportErrorCode) { super(code); }
}
export interface PreparedAccountImport { email: string; disabled: boolean; proxyConfigured: boolean; json: string }
const limit = 1048576;
const denied = new Set(["weight", "priority", "headers", "request_retry", "request-retry", "excluded_models", "excluded-models", "note", "websockets", "prefix", "models", "disable_cooling", "disable-cooling", "fingerprint_profile", "fingerprint-profile", "base_url", "base-url", "model_aliases", "model-aliases", "request_scoped_errors", "request-scoped-errors", "tool_prefix_disabled", "tool-prefix-disabled"]);
const protocols = new Set(["http", "https", "socks5", "socks5h"]);
const fail = (code: ImportErrorCode): never => { throw new AccountImportError(code); };
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return fail("invalidAccount");
  return value as Record<string, unknown>;
}
function optionalString(value: unknown): string | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== "string") return fail("invalidAccount");
  return value;
}
function disabledState(value: unknown): boolean {
  if (value === undefined) return false;
  if (typeof value !== "boolean") return fail("invalidAccount");
  return value;
}
function proxyURL(value: unknown): string {
  if (typeof value !== "string" || !value || /[\s\u0000-\u001f\u007f?#]/u.test(value)) return fail("invalidProxy");
  try {
    const url = new URL(value);
    const authority = value.match(/^[a-z][a-z0-9+.-]*:\/\/([^/]+)/)?.[1];
    if (!authority || authority.endsWith(":" ) || !protocols.has(url.protocol.slice(0, -1)) || !url.hostname || (url.pathname !== "" && url.pathname !== "/") || (url.port && (+url.port < 1 || +url.port > 65535))) return fail("invalidProxy");
    if (/[\u0000-\u001f\u007f]/u.test(decodeURIComponent(url.username) + decodeURIComponent(url.password))) return fail("invalidProxy");
    return value;
  } catch { return fail("invalidProxy"); }
}
function buildProxy(value: unknown): string {
  const p = object(value);
  if (typeof p.protocol !== "string" || !protocols.has(p.protocol) || typeof p.host !== "string" || !p.host || /[\s/@?#\\\u0000-\u001f\u007f]/u.test(p.host) || typeof p.port !== "number" || !Number.isInteger(p.port) || p.port < 1 || p.port > 65535) return fail("invalidProxy");
  const username = optionalString(p.username) ?? "";
  const password = optionalString(p.password) ?? "";
  if (password && !username) return fail("invalidProxy");
  const auth = username ? `${encodeURIComponent(username)}:${encodeURIComponent(password)}@` : "";
  const host = p.host.includes(":") && !p.host.startsWith("[") ? `[${p.host}]` : p.host;
  return proxyURL(`${p.protocol}://${auth}${host}:${p.port}`);
}
function prepare(fields: Record<string, unknown>): PreparedAccountImport {
  const email = optionalString(fields.email)?.trim().toLowerCase();
  if (!email || new TextEncoder().encode(email).length > 238 || /[\/\\\u0000-\u001f\u007f]/u.test(email) || email === "." || email === "..") return fail("invalidAccount");
  if (!optionalString(fields.refresh_token)?.trim()) return fail("invalidAccount");
  for (const key of Object.keys(fields)) if (denied.has(key)) return fail("forbiddenConfiguration");
  if ("proxy_url" in fields && "proxy-url" in fields) return fail("invalidProxy");
  const proxy = "proxy_url" in fields ? proxyURL(fields.proxy_url) : "proxy-url" in fields ? proxyURL(fields["proxy-url"]) : undefined;
  const disabled = disabledState(fields.disabled);
  const json = JSON.stringify({ ...fields, email, disabled });
  if (new TextEncoder().encode(json).length > limit) return fail("tooLarge");
  return { email, disabled, proxyConfigured: !!proxy, json };
}

// Pure, memory-only conversion. Errors contain codes, never input fragments.
export function parseAccountImport(text: string): PreparedAccountImport[] {
  if (new TextEncoder().encode(text).length > limit) return fail("tooLarge");
  let value: unknown;
  try { value = JSON.parse(text); } catch { return fail("invalidJson"); }
  const root = object(value);
  if (!("accounts" in root)) {
    if (root.type !== "antigravity") return fail("invalidAccount");
    return [prepare(root)];
  }
  if (!Array.isArray(root.accounts) || !root.accounts.length) return fail("invalidAccount");
  if (root.accounts.length > 100) return fail("tooMany");
  if (root.proxies !== undefined && !Array.isArray(root.proxies)) return fail("invalidProxy");
  const proxies = new Map<string, unknown>();
  for (const value of (root.proxies ?? []) as unknown[]) {
    const proxy = object(value);
    if (typeof proxy.proxy_key !== "string" || !proxy.proxy_key || proxies.has(proxy.proxy_key)) return fail("invalidProxy");
    proxies.set(proxy.proxy_key, proxy);
  }
  const seen = new Set<string>();
  return root.accounts.map((value) => {
    const account = object(value);
    if (account.platform !== "antigravity" || account.type !== "oauth") return fail("invalidAccount");
    const credential = object(account.credentials);
    const project = optionalString(credential.project_id);
    const alternate = optionalString(credential.antigravity_project_id);
    if (project && alternate && project !== alternate) return fail("invalidAccount");
    const fields: Record<string, unknown> = { type: "antigravity", email: credential.email, refresh_token: credential.refresh_token, disabled: disabledState(account.disabled) };
    const access = optionalString(credential.access_token);
    if (access !== undefined) fields.access_token = access;
    if (project || alternate) fields.project_id = project || alternate;
    const expiry = optionalString(credential.expires_at);
    if (expiry !== undefined) {
      const instant = /^\d+$/u.test(expiry) ? Number(expiry) * 1000 : /^\d{4}-\d\d-\d\dT.*(?:Z|[+-]\d\d:\d\d)$/u.test(expiry) ? Date.parse(expiry) : NaN;
      if (!Number.isSafeInteger(instant) || !Number.isFinite(new Date(instant).getTime())) return fail("invalidAccount");
      fields.expired = new Date(instant).toISOString();
    }
    if (account.proxy_key !== undefined && account.proxy_key !== null) {
      if (typeof account.proxy_key !== "string" || !proxies.has(account.proxy_key)) return fail("invalidProxy");
      fields.proxy_url = buildProxy(proxies.get(account.proxy_key));
    }
    const result = prepare(fields);
    if (seen.has(result.email)) return fail("duplicateIdentity");
    seen.add(result.email);
    return result;
  });
}

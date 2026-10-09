import { describe, expect, it } from "vitest";
import { parseAccountImport } from "./account-json-import";

const single = { type: "antigravity", email: " User@Example.Invalid ", refresh_token: "synthetic-refresh" };
const account = { platform: "antigravity", type: "oauth", credentials: { email: "batch@example.invalid", refresh_token: "synthetic-refresh", access_token: "synthetic-access", project_id: "project", antigravity_project_id: "project", expires_at: "2026-10-09T20:00:00+08:00", model_mapping: {} }, proxy_key: "p", auto_pause_on_expired: true };
const proxy = { proxy_key: "p", protocol: "socks5h", host: "proxy.example.invalid", port: 1080, username: "u@x", password: "p:/x" };
const parse = (value: unknown) => parseAccountImport(JSON.stringify(value));

describe("account JSON import", () => {
  it("derives normalized identity and defaults disabled without leaking credential into preview", () => {
    const result = parse(single)[0]!;
    expect(result.email).toBe("user@example.invalid");
    expect(result.disabled).toBe(false);
    expect(JSON.parse(result.json)).toEqual({ ...single, email: "user@example.invalid", disabled: false });
  });
  it("converts Sub2API proxy, expiry and project while omitting scheduler configuration", () => {
    const result = parse({ accounts: [account], proxies: [proxy] })[0]!;
    expect(JSON.parse(result.json)).toEqual({ type: "antigravity", email: "batch@example.invalid", refresh_token: "synthetic-refresh", access_token: "synthetic-access", project_id: "project", expired: "2026-10-09T12:00:00.000Z", disabled: false, proxy_url: "socks5h://u%40x:p%3A%2Fx@proxy.example.invalid:1080" });
    expect(result.proxyConfigured).toBe(true);
  });
  it("converts Sub2API Unix seconds expiry to the same instant", () => {
    const result = parse({ accounts: [{ ...account, credentials: { ...account.credentials, expires_at: "1791547200" } }], proxies: [proxy] })[0]!;
    expect(JSON.parse(result.json).expired).toBe("2026-10-09T12:00:00.000Z");
  });
  it.each([true, false])("preserves explicit disabled=%s", (disabled) => {
    expect(parse({ ...single, disabled })[0]!.disabled).toBe(disabled);
    expect(parse({ accounts: [{ ...account, disabled }], proxies: [proxy] })[0]!.disabled).toBe(disabled);
  });
  it.each([null, "false", 0])("rejects nonboolean disabled %s", (disabled) => expect(() => parse({ ...single, disabled })).toThrow());
  it.each(["http", "https", "socks5", "socks5h"])("retains supported proxy %s", (protocol) => expect(JSON.parse(parse({ ...single, proxy_url: `${protocol}://proxy.example.invalid:1080` })[0]!.json).proxy_url).toBe(`${protocol}://proxy.example.invalid:1080`));
  it("supports IPv6 and missing proxy references only when genuinely unconfigured", () => {
    expect(JSON.parse(parse({ accounts: [account], proxies: [{ ...proxy, host: "::1" }] })[0]!.json).proxy_url).toContain("@[::1]:1080");
    expect(parse({ accounts: [{ ...account, proxy_key: null }] })[0]!.proxyConfigured).toBe(false);
  });
  it.each([
    { accounts: [account], proxies: [] },
    { accounts: [account], proxies: [proxy, proxy] },
    { accounts: [account], proxies: [{ ...proxy, port: 0 }] },
    { accounts: [account], proxies: [{ ...proxy, host: "evil@proxy.invalid" }] },
    { accounts: [{ ...account, credentials: { ...account.credentials, antigravity_project_id: "different" } }], proxies: [proxy] },
    { accounts: [{ ...account, credentials: { ...account.credentials, expires_at: "invalid" } }], proxies: [proxy] },
    { accounts: [account, account], proxies: [proxy] },
    { ...single, priority: 1 }, { ...single, "request-retry": 1 },
    { ...single, proxy_url: "http://proxy.invalid?secret" },
    { ...single, proxy_url: "http://proxy.invalid", "proxy-url": "http://proxy.invalid" },
    { ...single, refresh_token: "" }, { ...single, email: "../bad" },
    { accounts: [] }, { accounts: [{ ...account, platform: "other" }] }, null,
  ])("rejects invalid whole-file input %#", (value) => expect(() => parse(value)).toThrow());
  it("bounds input and never includes raw input in errors", () => {
    expect(() => parseAccountImport("{" + "synthetic-secret" )).toThrow("invalidJson");
    expect(() => parseAccountImport(" ".repeat(1048577))).toThrow("tooLarge");
    expect(() => parse({ accounts: Array.from({ length: 101 }, (_, i) => ({ ...account, proxy_key: null, credentials: { email: `u${i}@example.invalid`, refresh_token: "synthetic" } })) })).toThrow("tooMany");
  });
});

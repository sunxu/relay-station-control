import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthApiError, generatedAuthApi, userFacingError } from "./auth-api";

afterEach(() => vi.unstubAllGlobals());

describe("generated client boundary", () => {
  it("places bootstrap secret only in the dedicated header", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "in_progress", totp_enrollment: { otpauth_uri: "otpauth://totp/test?secret=server", algorithm: "SHA1", digits: 6, period_seconds: 30 } }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await generatedAuthApi.bootstrapStart("runtime-only", { login_name: "admin.one", display_name: "测试管理员", password: "correct horse battery staple" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, request] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/bootstrap/start");
    expect(url).not.toContain("runtime-only");
    expect(new Headers(request.headers).get("X-Bootstrap-Secret")).toBe("runtime-only");
    expect(request.body).not.toContain("runtime-only");
    expect(request.cache).toBe("no-store");
  });

  it("treats an unauthorized session as signed out", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ code: "unauthorized", message: "unauthorized", request_id: "request-0001" }), { status: 401 })));
    await expect(generatedAuthApi.session()).resolves.toBeNull();
  });

  it("sends CSRF once and never retries a non-idempotent request", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ code: "csrf_invalid", message: "forbidden", request_id: "request-0002" }), { status: 403 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(generatedAuthApi.recoveryCodes("session-csrf", { reason: "安全轮换恢复代码" })).rejects.toMatchObject({ status: 403 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, request] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/auth/recovery-codes/regenerate");
    expect(new Headers(request.headers).get("X-CSRF-Token")).toBe("session-csrf");
  });

  it("maps account-sensitive and administrator protection errors to bounded UI copy", () => {
    expect(userFacingError(new AuthApiError(401, { code: "authentication_failed", message: "internal", request_id: "request-0003" }))).toBe("认证失败，请检查输入后重试。");
    expect(userFacingError(new AuthApiError(429, { code: "rate_limited", message: "internal", request_id: "request-0004" }))).toBe("尝试次数过多，请稍后再试。");
    expect(userFacingError(new AuthApiError(409, { code: "last_administrator_protected", message: "internal", request_id: "request-0005" }))).toBe("不能禁用最后一个可用管理员。");
    expect(userFacingError(new AuthApiError(403, { code: "administrator_self_disable_forbidden", message: "internal", request_id: "request-0006" }))).toBe("不能禁用当前登录账号。");
  });
});

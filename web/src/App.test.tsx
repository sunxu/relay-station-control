import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Modal } from "antd";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { AuthApiError } from "./api/auth-api";
import type { AuthApi } from "./api/auth-api";
import type { Administrator, BootstrapState, SessionResponse } from "./api/generated/control";

const administrator: Administrator = {
  id: "00000000-0000-4000-8000-000000000001", login_name: "admin.one", display_name: "测试管理员",
  auth_source: "local", role: "super_admin", status: "enabled",
  created_at: "2026-08-25T00:00:00Z", updated_at: "2026-08-25T00:00:00Z",
};

const session: SessionResponse = {
  state: "authenticated", administrator, mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "c".repeat(32), created_at: "2026-08-25T00:00:00Z", last_activity_at: "2026-08-25T00:00:00Z",
  idle_expires_at: "2099-08-25T00:30:00Z", absolute_expires_at: "2099-08-25T12:00:00Z",
  reauthenticated_until: null, recovery_codes_remaining: 10,
};

const secondAdministrator: Administrator = {
  ...administrator,
  id: "00000000-0000-4000-8000-000000000002",
  login_name: "admin.two",
  display_name: "第二管理员",
};

function dispatchPageTransition(type: "pagehide" | "pageshow", persisted: boolean) {
  const event = new Event(type);
  Object.defineProperty(event, "persisted", { value: persisted });
  fireEvent(window, event);
}

function makeApi(state: BootstrapState, current: SessionResponse | null = null): AuthApi {
  return {
    bootstrapStatus: vi.fn().mockResolvedValue(state),
    bootstrapStart: vi.fn().mockResolvedValue({ status: "in_progress", totp_enrollment: { otpauth_uri: "otpauth://totp/test?secret=MEMORYONLY", algorithm: "SHA1", digits: 6, period_seconds: 30 } }),
    bootstrapComplete: vi.fn().mockResolvedValue({ status: "completed", session, recovery_codes: Array.from({ length: 10 }, (_, i) => `recovery-${i}-onlyonce`) }),
    bootstrapReset: vi.fn().mockResolvedValue("required"), session: vi.fn().mockResolvedValue(current),
    login: vi.fn().mockResolvedValue({ state: "mfa_required", expires_at: "2099-08-25T00:05:00Z", methods: ["totp", "recovery_code"] }),
    completeMfa: vi.fn().mockResolvedValue(session), logout: vi.fn().mockResolvedValue(undefined),
    activate: vi.fn().mockResolvedValue({ state: "enrollment_required", totp_enrollment: { otpauth_uri: "otpauth://totp/activation?secret=MEMORYONLY", algorithm: "SHA1", digits: 6, period_seconds: 30 } }),
    reauthenticate: vi.fn().mockResolvedValue({ ...session, reauthenticated_until: "2099-08-25T00:05:00Z" }),
    changePassword: vi.fn().mockResolvedValue(session),
    recoveryCodes: vi.fn().mockResolvedValue({ recovery_codes: Array.from({ length: 10 }, (_, i) => `new-recovery-${i}`), remaining: 10 }),
    administrators: vi.fn().mockResolvedValue({ items: [administrator], next_cursor: null }),
    createAdministrator: vi.fn(), disableAdministrator: vi.fn(), activationToken: vi.fn(), resetMfa: vi.fn(),
  };
}

afterEach(() => {
  Modal.destroyAll();
  document.querySelectorAll(".ant-modal-root").forEach((element) => element.remove());
  window.history.replaceState(null, "", "/");
  window.localStorage.clear();
  window.sessionStorage.clear();
});

describe("authentication shell routing", () => {
  it.each([["required" as const], ["in_progress" as const]])("routes bootstrap state %s to bootstrap", async (state) => {
    render(<App api={makeApi(state)} />);
    expect(await screen.findByTestId("bootstrap-page")).toBeInTheDocument();
  });

  it("routes completed bootstrap without a session to login", async () => {
    render(<App api={makeApi("completed")} />);
    expect(await screen.findByTestId("login-page")).toBeInTheDocument();
  });

  it("routes an authenticated session to the lazy management chunk", async () => {
    render(<App api={makeApi("completed", session)} />);
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.getByText("测试管理员 · admin.one")).toBeInTheDocument();
  });

  it("fails closed when bootstrap state cannot be read", async () => {
    const api = makeApi("completed");
    vi.mocked(api.bootstrapStatus).mockRejectedValue(new Error("database unavailable"));
    render(<App api={api} />);
    expect(await screen.findByTestId("auth-unavailable")).toBeInTheDocument();
  });
});

describe("secret lifecycle", () => {
  async function startAndComplete(api: AuthApi) {
    render(<App api={api} />);
    fireEvent.change(await screen.findByTestId("bootstrap-secret"), { target: { value: "runtime-only-secret" } });
    fireEvent.change(screen.getByLabelText("登录名"), { target: { value: "admin.one" } });
    fireEvent.change(screen.getByLabelText("实名显示名"), { target: { value: "测试管理员" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "correct horse battery staple" } });
    fireEvent.click(screen.getByRole("button", { name: "开始或继续初始化" }));
    await screen.findByTestId("totp-enrollment");
    fireEvent.change(screen.getByLabelText("TOTP 验证码"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "确认并永久完成" }));
    await screen.findByTestId("one-time-page");
  }

  it("keeps bootstrap secret out of URL and storage, then removes it from the DOM", async () => {
    await startAndComplete(makeApi("required"));
    expect(window.location.href).not.toContain("runtime-only-secret");
    expect(JSON.stringify(window.localStorage)).not.toContain("runtime-only-secret");
    expect(JSON.stringify(window.sessionStorage)).not.toContain("runtime-only-secret");
    expect(screen.queryByDisplayValue("runtime-only-secret")).not.toBeInTheDocument();
  });

  it("strips query/hash and requires a manually pasted activation token", async () => {
    window.history.replaceState(null, "", "/?activation_token=leaked#token");
    render(<App api={makeApi("completed")} />);
    await screen.findByTestId("login-page");
    fireEvent.click(screen.getByRole("button", { name: "使用激活令牌设置新账号" }));
    expect(await screen.findByTestId("activation-page")).toBeInTheDocument();
    await waitFor(() => expect(window.location.search + window.location.hash).toBe(""));
    expect(screen.getByTestId("activation-token")).toHaveValue("");
  });

  it("requires confirmation before discarding one-time recovery codes", async () => {
    await startAndComplete(makeApi("required"));
    expect(screen.getByRole("button", { name: "确认并离开" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "确认并离开" }));
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.queryByText("recovery-0-onlyonce")).not.toBeInTheDocument();
  });

  it.each([
    ["pagehide", false],
    ["pageshow", true],
  ] as const)("wipes one-time material on %s (persisted=%s)", async (eventName, persisted) => {
    const api = makeApi("required");
    await startAndComplete(api);
    expect(screen.getByText("recovery-0-onlyonce")).toBeInTheDocument();
    dispatchPageTransition(eventName, persisted);
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.queryByText("recovery-0-onlyonce")).not.toBeInTheDocument();
    expect(api.bootstrapComplete).toHaveBeenCalledTimes(1);
  });

  it("wipes one-time material when navigation is interrupted before unload", async () => {
    const api = makeApi("required");
    await startAndComplete(api);
    fireEvent(window, new Event("beforeunload"));
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.queryByText("recovery-0-onlyonce")).not.toBeInTheDocument();
    expect(api.bootstrapComplete).toHaveBeenCalledTimes(1);
  });
});

describe("login MFA", () => {
  it("supports recovery-code MFA and exposes only the remaining count", async () => {
    const api = makeApi("completed");
    vi.mocked(api.completeMfa).mockResolvedValue({ ...session, recovery_codes_remaining: 7, mfa: { required: true, completed: true, method: "recovery_code" } });
    render(<App api={api} />);
    await screen.findByTestId("login-page");
    fireEvent.change(screen.getByLabelText("登录名"), { target: { value: "admin.one" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "correct horse battery staple" } });
    fireEvent.click(screen.getByRole("button", { name: /继.*续/ }));
    expect(await screen.findByText("需要第二步验证")).toBeInTheDocument();
    fireEvent.click(screen.getByText("恢复码"));
    const recoveryInput = screen.getAllByLabelText("恢复码").find((element) => element.getAttribute("type") === "password");
    expect(recoveryInput).toBeDefined();
    fireEvent.change(recoveryInput!, { target: { value: "one-time-recovery-code" } });
    fireEvent.click(screen.getByRole("button", { name: "验证并登录" }));
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.getByText("剩余恢复码：7")).toBeInTheDocument();
    expect(screen.queryByText("one-time-recovery-code")).not.toBeInTheDocument();
  });

  it("expires a stale challenge locally and returns to password login", async () => {
    const api = makeApi("completed");
    vi.mocked(api.login).mockResolvedValue({ state: "mfa_required", expires_at: "2020-01-01T00:00:00Z", methods: ["totp"] });
    render(<App api={api} />);
    await screen.findByTestId("login-page");
    fireEvent.change(screen.getByLabelText("登录名"), { target: { value: "admin.one" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "correct horse battery staple" } });
    fireEvent.click(screen.getByRole("button", { name: /继.*续/ }));
    expect(await screen.findByText("验证已过期，请重新登录。")).toBeInTheDocument();
    expect(await screen.findByLabelText("登录名")).toBeInTheDocument();
    expect(api.completeMfa).not.toHaveBeenCalled();
  });

  it("clears the challenge when the server reports it expired", async () => {
    const api = makeApi("completed");
    vi.mocked(api.completeMfa).mockRejectedValue(new AuthApiError(401, {
      code: "challenge_expired",
      message: "expired",
      request_id: "request-expired",
    }));
    render(<App api={api} />);
    await screen.findByTestId("login-page");
    fireEvent.change(screen.getByLabelText("登录名"), { target: { value: "admin.one" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "correct horse battery staple" } });
    fireEvent.click(screen.getByRole("button", { name: /继.*续/ }));
    await screen.findByText("需要第二步验证");
    fireEvent.change(screen.getByLabelText("TOTP 验证码"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "验证并登录" }));
    expect(await screen.findByText("验证已过期，请重新登录。")).toBeInTheDocument();
    expect(await screen.findByLabelText("登录名")).toBeInTheDocument();
    expect(api.completeMfa).toHaveBeenCalledTimes(1);
  });
});

describe("session rotation and high-risk operations", () => {
  it("uses the rotated CSRF proof for the next high-risk mutation", async () => {
    const api = makeApi("completed", session);
    const rotated = { ...session, csrf_token: "r".repeat(32), reauthenticated_until: "2099-08-25T00:05:00Z" };
    vi.mocked(api.reauthenticate).mockResolvedValue(rotated);
    vi.mocked(api.administrators).mockResolvedValue({ items: [administrator, secondAdministrator], next_cursor: null });
    vi.mocked(api.createAdministrator).mockResolvedValue({ administrator: secondAdministrator, activation_token: "activation-once-".repeat(4), expires_at: "2099-08-26T00:00:00Z" });
    render(<App api={api} />);
    await screen.findByTestId("management-page");

    fireEvent.click(screen.getByRole("tab", { name: "重新认证" }));
    fireEvent.change(screen.getByLabelText("当前密码"), { target: { value: "correct horse battery staple" } });
    fireEvent.change(screen.getByLabelText("MFA 验证码"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "重新认证" }));
    await waitFor(() => expect(api.reauthenticate).toHaveBeenCalledWith(session.csrf_token, expect.any(Object)));

    fireEvent.click(screen.getByRole("tab", { name: "管理员" }));
    fireEvent.change(screen.getByLabelText("登录名"), { target: { value: "admin.three" } });
    fireEvent.change(screen.getByLabelText("实名显示名"), { target: { value: "第三管理员" } });
    fireEvent.change(screen.getByLabelText("操作原因"), { target: { value: "创建值班恢复管理员账号" } });
    fireEvent.click(screen.getByRole("button", { name: "创建并显示一次性令牌" }));
    await waitFor(() => expect(api.createAdministrator).toHaveBeenCalledWith(rotated.csrf_token, expect.any(Object)));
    expect(await screen.findByTestId("one-time-page")).toBeInTheDocument();

    dispatchPageTransition("pagehide", true);
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.queryByText("activation-once-".repeat(4))).not.toBeInTheDocument();
    expect(api.createAdministrator).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["administrator_self_disable_forbidden" as const, "不能禁用当前登录账号。"],
    ["last_administrator_protected" as const, "不能禁用最后一个可用管理员。"],
  ])("shows backend protection error %s for disable", async (code, message) => {
    const freshSession = { ...session, reauthenticated_until: "2099-08-25T00:05:00Z" };
    const api = makeApi("completed", freshSession);
    vi.mocked(api.disableAdministrator).mockRejectedValue(new AuthApiError(code === "administrator_self_disable_forbidden" ? 403 : 409, {
      code,
      message: "bounded backend detail",
      request_id: "request-protected",
    }));
    render(<App api={api} />);
    await screen.findByTestId("management-page");
    fireEvent.click(screen.getByRole("tab", { name: "管理员" }));
    fireEvent.click(await screen.findByRole("radio", { name: "选择管理员 admin.one" }));
    fireEvent.change(screen.getByPlaceholderText("操作原因（10–500 字符）"), { target: { value: "安全测试禁用管理员操作" } });
    fireEvent.click(screen.getByTestId("disable-administrator"));
    const dialogs = await screen.findAllByRole("dialog");
    const dialog = dialogs.at(-1)!;
    fireEvent.click(within(dialog).getByText("OK").closest("button")!);
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(api.disableAdministrator).toHaveBeenCalledTimes(1);
  });
});

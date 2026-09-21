import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AuthApiError } from "../api/auth-api";
import { FrontendFoundationProvider } from "./FrontendFoundationProvider";
import { GlobalHeader } from "./GlobalHeader";

const { authState } = vi.hoisted(() => ({
  authState: {
    route: "settings",
    session: {
      csrf_token: "c".repeat(32),
      administrator: { display_name: "测试管理员", login_name: "admin.one" },
    },
    api: { logout: vi.fn() },
    clearSession: vi.fn(),
    refreshSession: vi.fn(),
    navigate: vi.fn(),
  },
}));

vi.mock("../auth/AuthContext", () => ({
  useAuth: () => authState,
  handleSessionError: (error: unknown, clearSession: () => void) => {
    if (error instanceof AuthApiError && error.status === 401) clearSession();
  },
}));

function renderHeader() {
  return render(
    <FrontendFoundationProvider initialLocale="zh-CN">
      <GlobalHeader />
    </FrontendFoundationProvider>,
  );
}

describe("GlobalHeader logout semantics", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("clears the session once after logout succeeds", async () => {
    authState.api.logout.mockResolvedValue(undefined);
    renderHeader();

    fireEvent.click(screen.getByTestId("global-logout"));

    await waitFor(() => expect(authState.clearSession).toHaveBeenCalledTimes(1));
    expect(authState.refreshSession).not.toHaveBeenCalled();
  });

  it("clears the session when the server reports an invalid session", async () => {
    authState.api.logout.mockRejectedValue(new AuthApiError(401, {
      code: "authentication_failed",
      message: "expired",
      request_id: "request-401",
    }));
    renderHeader();

    fireEvent.click(screen.getByTestId("global-logout"));

    await waitFor(() => expect(authState.clearSession).toHaveBeenCalledTimes(1));
  });

  it("retains the session and shows a bounded error for generic failures", async () => {
    authState.api.logout.mockRejectedValue(new Error("network down"));
    renderHeader();

    fireEvent.click(screen.getByTestId("global-logout"));

    expect(await screen.findByTestId("global-logout-error")).toHaveTextContent("请求暂时无法完成");
    expect(authState.clearSession).not.toHaveBeenCalled();
  });

  it("refreshes CSRF and retains the session after csrf_invalid", async () => {
    authState.api.logout.mockRejectedValue(new AuthApiError(403, {
      code: "csrf_invalid",
      message: "stale csrf",
      request_id: "request-csrf",
    }));
    authState.refreshSession.mockResolvedValue(authState.session);
    renderHeader();

    fireEvent.click(screen.getByTestId("global-logout"));

    await waitFor(() => expect(authState.refreshSession).toHaveBeenCalledTimes(1));
    expect(authState.clearSession).not.toHaveBeenCalled();
    expect(await screen.findByTestId("global-logout-error")).toHaveTextContent("安全凭据已变化");
  });
});

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { AuthApi } from "../api/auth-api";
import type { Administrator, SessionResponse } from "../api/generated/control";
import { AuthProvider, useAuth } from "./AuthContext";

const administrator: Administrator = {
  id: "00000000-0000-4000-8000-000000000001",
  login_name: "admin.one",
  display_name: "测试管理员",
  auth_source: "local",
  role: "super_admin",
  status: "enabled",
  created_at: "2026-08-25T00:00:00Z",
  updated_at: "2026-08-25T00:00:00Z",
};

const refreshedSession: SessionResponse = {
  state: "authenticated",
  administrator,
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "n".repeat(32),
  created_at: "2026-08-25T00:00:00Z",
  last_activity_at: "2026-08-25T00:01:00Z",
  idle_expires_at: "2026-08-25T00:31:00Z",
  absolute_expires_at: "2026-08-25T12:00:00Z",
  reauthenticated_until: null,
  recovery_codes_remaining: 10,
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function apiWithSession(session: AuthApi["session"]): AuthApi {
  return {
    bootstrapStatus: vi.fn().mockResolvedValue("required"),
    bootstrapStart: vi.fn(),
    bootstrapComplete: vi.fn(),
    bootstrapReset: vi.fn(),
    session,
    login: vi.fn(),
    completeMfa: vi.fn(),
    logout: vi.fn(),
    activate: vi.fn(),
    reauthenticate: vi.fn(),
    changePassword: vi.fn(),
    recoveryCodes: vi.fn(),
    administrators: vi.fn(),
    createAdministrator: vi.fn(),
    disableAdministrator: vi.fn(),
    activationToken: vi.fn(),
    resetMfa: vi.fn(),
  };
}

function RefreshHarness({ onPromises }: { onPromises(first: Promise<SessionResponse | null>, second: Promise<SessionResponse | null>): void }) {
  const auth = useAuth();
  const [resolved, setResolved] = useState("");
  const refresh = () => {
    const first = auth.refreshSession();
    const second = auth.refreshSession();
    onPromises(first, second);
    void Promise.all([first, second]).then(([one, two]) => {
      setResolved(`${one?.csrf_token}:${two?.csrf_token}`);
    });
  };
  return <><button onClick={refresh}>并发刷新</button><output>{resolved}</output></>;
}

describe("AuthContext session refresh", () => {
  it("shares one in-flight request and the same refreshed CSRF result", async () => {
    const pending = deferred<SessionResponse | null>();
    const session = vi.fn().mockReturnValue(pending.promise);
    const api = apiWithSession(session);
    let first!: Promise<SessionResponse | null>;
    let second!: Promise<SessionResponse | null>;

    render(
      <AuthProvider api={api}>
        <RefreshHarness onPromises={(one, two) => { first = one; second = two; }} />
      </AuthProvider>,
    );
    await waitFor(() => expect(api.bootstrapStatus).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "并发刷新" }));

    expect(first).toBe(second);
    expect(session).toHaveBeenCalledTimes(1);
    pending.resolve(refreshedSession);
    await expect(first).resolves.toBe(refreshedSession);
    await expect(second).resolves.toBe(refreshedSession);
    expect(await screen.findByText(`${refreshedSession.csrf_token}:${refreshedSession.csrf_token}`)).toBeInTheDocument();
  });
});

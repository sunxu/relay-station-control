import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import App from "./App";
import type { AuthApi } from "./api/auth-api";
import type { SessionResponse } from "./api/generated/control";

const chunk = vi.hoisted(() => ({ loaded: vi.fn() }));

vi.mock("./pages/AssetsPage", () => {
  chunk.loaded();
  return { default: () => <main data-testid="mock-assets-page">资产注册表 chunk</main> };
});

const session: SessionResponse = {
  state: "authenticated",
  administrator: {
    id: "00000000-0000-4000-8000-000000000001",
    login_name: "admin.one",
    display_name: "测试管理员",
    auth_source: "local",
    role: "super_admin",
    status: "enabled",
    created_at: "2026-08-25T00:00:00Z",
    updated_at: "2026-08-25T00:00:00Z",
  },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "c".repeat(32),
  created_at: "2026-08-25T00:00:00Z",
  last_activity_at: "2026-08-25T00:00:00Z",
  idle_expires_at: "2099-08-25T00:30:00Z",
  absolute_expires_at: "2099-08-25T12:00:00Z",
  reauthenticated_until: null,
  recovery_codes_remaining: 10,
};

function api(): AuthApi {
  return {
    bootstrapStatus: vi.fn().mockResolvedValue("completed"),
    session: vi.fn().mockResolvedValue(session),
    administrators: vi.fn().mockResolvedValue({ items: [session.administrator], next_cursor: null }),
  } as unknown as AuthApi;
}

afterEach(() => {
  window.history.replaceState(null, "", "/");
  chunk.loaded.mockClear();
});

it("does not load the assets chunk until authenticated navigation selects /assets", async () => {
  render(<App api={api()} />);
  expect(await screen.findByTestId("management-page")).toBeInTheDocument();
  expect(chunk.loaded).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "资产注册表" }));
  expect(await screen.findByTestId("mock-assets-page")).toBeInTheDocument();
  expect(chunk.loaded).toHaveBeenCalledTimes(1);
  expect(window.location.pathname).toBe("/assets");
});

it.each(["/assets", "/assets/"])("restores an authenticated direct visit to %s", async (pathname) => {
  window.history.replaceState(null, "", pathname);
  render(<App api={api()} />);
  expect(await screen.findByTestId("mock-assets-page")).toBeInTheDocument();
  expect(screen.queryByTestId("management-page")).not.toBeInTheDocument();
});

it("keeps the existing default route on the management page", async () => {
  window.history.replaceState(null, "", "/");
  render(<App api={api()} />);
  expect(await screen.findByTestId("management-page")).toBeInTheDocument();
  expect(screen.queryByTestId("mock-assets-page")).not.toBeInTheDocument();
});

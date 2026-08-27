import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import App from "./App";
import type { AuthApi } from "./api/auth-api";
import type { SessionResponse } from "./api/generated/control";

const chunk = vi.hoisted(() => ({ loaded: vi.fn() }));

vi.mock("./pages/AccountInventoryPage", () => {
  chunk.loaded();
  return { default: () => <main data-testid="mock-account-inventory-page">账号清单 chunk</main> };
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
    created_at: "2026-08-27T00:00:00Z",
    updated_at: "2026-08-27T00:00:00Z",
  },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "c".repeat(32),
  created_at: "2026-08-27T00:00:00Z",
  last_activity_at: "2026-08-27T00:00:00Z",
  idle_expires_at: "2099-08-27T00:30:00Z",
  absolute_expires_at: "2099-08-27T12:00:00Z",
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

it("lazy-loads the account inventory page only after authenticated navigation", async () => {
  render(<App api={api()} />);
  expect(await screen.findByTestId("management-page")).toBeInTheDocument();
  expect(chunk.loaded).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "账号清单" }));
  expect(await screen.findByTestId("mock-account-inventory-page")).toBeInTheDocument();
  expect(window.location.pathname).toBe("/account-inventory");
});

it("restores an authenticated direct visit without putting filters in the URL", async () => {
  window.history.replaceState(null, "", "/account-inventory");
  render(<App api={api()} />);
  expect(await screen.findByTestId("mock-account-inventory-page")).toBeInTheDocument();
  expect(window.location.search).toBe("");
});

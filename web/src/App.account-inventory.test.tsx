import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import App from "./App";
import type { AuthApi } from "./api/auth-api";
import type { SessionResponse } from "./api/generated/control";

vi.mock("./pages/TopologyPage", () => ({
  default: () => <main data-testid="mock-topology-page">Node Topology chunk</main>,
}));

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
});

it("keeps account inventory out of the management navigation", async () => {
  render(<App api={api()} />);
  expect(await screen.findByTestId("management-page")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "账号清单" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Node Topology" }));
  expect(window.location.pathname).toBe("/topology");
});

it.each(["/account-inventory", "/account-inventory?instance_id=node-a"]) (
  "does not load the removed account inventory route (%s)", async (path) => {
    window.history.replaceState(null, "", path);
    render(<App api={api()} />);
    expect(await screen.findByTestId("management-page")).toBeInTheDocument();
    expect(screen.queryByTestId("account-inventory-page")).not.toBeInTheDocument();
    expect(screen.queryByTestId("mock-topology-page")).not.toBeInTheDocument();
    expect(window.location.pathname).toBe("/account-inventory");
    expect(window.location.search).toBe(path.includes("?") ? "?instance_id=node-a" : "");
  },
);

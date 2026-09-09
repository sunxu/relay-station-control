import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccountDetailsDrawer } from "./AccountDetailsDrawer";
import type { AccountAvailabilityApi } from "../api/account-availability-types";
import type { AccountRequestHistoryApi } from "../api/account-request-history-types";
import type { AccountListRow } from "./AccountList";

const NODE = "11111111-1111-4111-8111-111111111111";
const ACCOUNT = "openai:a@example.invalid";
const row: AccountListRow = {
  account_key: ACCOUNT, email: "a@example.invalid", provider: "openai", basic_status: "reported_active", lifecycle: "present", quality: "good",
  request_count: 1, success_count: 1, failure_count: 0, success_rate: 1, p95_latency_ms: 10, last_success_at: null, last_failure_at: null, last_failure_class: null,
  recent_requests: [], availability: { state: "TOKEN_INVALID", reason: "token_invalid", since: "2026-09-09T00:00:00Z" },
};

function renderDrawer(api: AccountRequestHistoryApi & AccountAvailabilityApi, accountKey: string = ACCOUNT, selectedRow: AccountListRow = row) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AccountDetailsDrawer api={api} instanceId={NODE} row={selectedRow} accountKey={accountKey} onClose={vi.fn()} onUnauthorized={vi.fn()} /></QueryClientProvider>);
}

function api(overrides: Partial<AccountRequestHistoryApi & AccountAvailabilityApi> = {}) {
  return {
    requestHistory: vi.fn().mockResolvedValue({ instance_id: NODE, account_key: ACCOUNT, items: [], next_cursor: null }),
    accountAvailabilityOccurrences: vi.fn().mockResolvedValue({ instance_id: NODE, items: [], next_cursor: null }),
    ...overrides,
  } as AccountRequestHistoryApi & AccountAvailabilityApi;
}

describe("AccountDetailsDrawer availability", () => {
  it("shows active occurrences and paginates", async () => {
    const clientApi = api({ accountAvailabilityOccurrences: vi.fn().mockResolvedValueOnce({ instance_id: NODE, items: [{ occurrence_id: "occ-1", instance_id: NODE, account_key: ACCOUNT, reason: "token_invalid", severity: "Critical", status: "ACTIVE", first_seen_at: "2026-09-09T00:00:00Z", last_failure_at: "2026-09-09T00:01:00Z", confirmed_at: "2026-09-09T00:02:00Z", resolved_at: null }], next_cursor: "next" }).mockResolvedValue({ instance_id: NODE, items: [], next_cursor: null }) });
    renderDrawer(clientApi);
    fireEvent.click(await screen.findByRole("tab", { name: "可用性事件" }));
    expect(await screen.findByText("token_invalid")).toBeInTheDocument();
    expect(screen.getByText("Critical")).toBeInTheDocument();
    expect(screen.getAllByText(/2026-09-09/).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "可用性下一页" }));
    await waitFor(() => expect(clientApi.accountAvailabilityOccurrences).toHaveBeenLastCalledWith(NODE, ACCOUNT, "ACTIVE", "next", expect.any(AbortSignal)));
  });

  it("keeps empty and unavailable distinct", async () => {
    const emptyApi = api();
    renderDrawer(emptyApi);
    fireEvent.click(await screen.findByRole("tab", { name: "可用性事件" }));
    expect(await screen.findByText("当前没有 Active 可用性事件")).toBeInTheDocument();
    const unavailableApi = api({ accountAvailabilityOccurrences: vi.fn().mockRejectedValue(new Error("unavailable")) });
    renderDrawer(unavailableApi);
    fireEvent.click(await screen.findAllByRole("tab", { name: "可用性事件" }).then((tabs) => tabs.at(-1)!));
    expect(await screen.findAllByText("读取不可用（unavailable）")).not.toHaveLength(0);
  });

  it("does not expose actions", async () => {
    renderDrawer(api());
    fireEvent.click(await screen.findByRole("tab", { name: "可用性事件" }));
    expect(screen.queryByRole("button", { name: /disable|delete|bind|rebind|unbind|修改|删除/i })).not.toBeInTheDocument();
  });

  it("explains that current unknown or disabled does not erase active history", async () => {
    renderDrawer(api(), ACCOUNT, { ...row, availability: { state: "UNKNOWN", reason: "pending_confirmation", since: null } });
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.getByText(/既有 ACTIVE 可用性事件仍保留/)).toBeInTheDocument();
  });
});

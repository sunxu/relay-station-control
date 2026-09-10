import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccountDetailsDrawer } from "./AccountDetailsDrawer";
import { formatDateTime } from "../time";
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

function renderDrawer(api: AccountRequestHistoryApi & AccountAvailabilityApi, accountKey: string = ACCOUNT, selectedRow: AccountListRow = row, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(<QueryClientProvider client={client}><AccountDetailsDrawer api={api} instanceId={NODE} row={selectedRow} accountKey={accountKey} onClose={vi.fn()} onUnauthorized={vi.fn()} /></QueryClientProvider>);
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
    const clientApi = api({ accountAvailabilityOccurrences: vi.fn().mockResolvedValueOnce({ instance_id: NODE, items: [
      { occurrence_id: "occ-1", instance_id: NODE, account_key: ACCOUNT, reason: "token_invalid", severity: "Critical", status: "ACTIVE", first_seen_at: "2026-09-09T00:00:00Z", last_failure_at: "2026-09-09T00:01:00Z", confirmed_at: "2026-09-09T00:02:00Z", resolved_at: null },
      { occurrence_id: "occ-2", instance_id: NODE, account_key: ACCOUNT, reason: "forbidden", severity: "Warning", status: "ACTIVE", first_seen_at: "2026-09-10T00:00:00Z", last_failure_at: "2026-09-10T00:01:00Z", confirmed_at: "2026-09-10T00:02:00Z", resolved_at: null },
    ], next_cursor: "next" }).mockResolvedValue({ instance_id: NODE, items: [], next_cursor: null }) });
    renderDrawer(clientApi);
    fireEvent.click(await screen.findByRole("tab", { name: "可用性事件" }));
    expect(await screen.findByText("token_invalid")).toBeInTheDocument();
    expect(screen.getAllByRole("row")[1]).toHaveTextContent("token_invalid");
    expect(screen.getAllByRole("row")[2]).toHaveTextContent("forbidden");
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

describe("AccountDetailsDrawer token diagnostics", () => {
  it("keeps account B history when account A responds late", async () => {
    type History = Awaited<ReturnType<AccountRequestHistoryApi["requestHistory"]>>;
    let finishA!: (value: History) => void;
    const item = { occurred_at: "2026-09-11T01:00:00Z", model: "model", success: true, failure_class: null, duration_ms: 1, request_id: "B-history" };
    const second = { ...row, account_key: "antigravity:b@example.invalid", email: "b@example.invalid", token_state: "INVALID" as const };
    const clientApi = api({ requestHistory: vi.fn()
      .mockReturnValueOnce(new Promise<History>((resolve) => { finishA = resolve; }))
      .mockResolvedValue({ instance_id: NODE, account_key: second.account_key, items: [item], next_cursor: null }) });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const rendered = renderDrawer(clientApi, ACCOUNT, row, client);
    await waitFor(() => expect(clientApi.requestHistory).toHaveBeenCalledWith(NODE, ACCOUNT, undefined, expect.any(AbortSignal)));
    rendered.rerender(<QueryClientProvider client={client}><AccountDetailsDrawer api={clientApi} instanceId={NODE} row={second} accountKey={second.account_key} onClose={vi.fn()} onUnauthorized={vi.fn()} /></QueryClientProvider>);
    expect(await screen.findByText("B-history")).toBeInTheDocument();
    expect(clientApi.requestHistory).toHaveBeenLastCalledWith(NODE, second.account_key, undefined, expect.any(AbortSignal));
    await act(async () => finishA({ instance_id: NODE, account_key: ACCOUNT, items: [{ ...item, request_id: "A-late-history" }], next_cursor: null }));
    expect(screen.queryByText("A-late-history")).not.toBeInTheDocument();
    expect(screen.getByText("B-history")).toBeInTheDocument();
  });

  it("keeps token diagnostics read-only and ignores credential material", async () => {
    const canary = crypto.randomUUID();
    const selectedRow = { ...row, token_state: "UNKNOWN" as const, expected_valid_until: null,
      access_token: canary, refresh_token: canary, authorization: canary, auth_file: canary };
    renderDrawer(api(), ACCOUNT, selectedRow);
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.getByText("Token Health")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain(canary);
    expect(screen.queryByRole("button", { name: /refresh|repair|reauth|upload|delete|disable|刷新|修复|认证|上传|删除|禁用/i })).not.toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeNull();
    expect(screen.queryByText(/access_token|refresh_token|authorization|auth_file|Token Expiration|Expires At/)).not.toBeInTheDocument();
  });

  it.each([
    ["VALID", "green"],
    ["INVALID", "red"],
    ["UNKNOWN", undefined],
  ] as const)("renders Token Health %s without recalculating it", async (tokenState, color) => {
    renderDrawer(api(), ACCOUNT, { ...row, provider: "antigravity", token_state: tokenState, expected_valid_until: "2026-09-11T01:00:00Z" });
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    const token = screen.getByText(tokenState);
    expect(token).toBeInTheDocument();
    if (color) expect(token.closest(".ant-tag")).toHaveClass(`ant-tag-${color}`);
    expect(screen.getByText("Expected Valid Until")).toBeInTheDocument();
    expect(screen.getByText(formatDateTime("2026-09-11T01:00:00Z"))).toBeInTheDocument();
  });

  it("keeps missing diagnostics visible and does not derive token state from timestamps", async () => {
    renderDrawer(api(), ACCOUNT, { ...row, provider: "antigravity", token_state: "UNKNOWN", expected_valid_until: null, last_refresh_at: "2000-01-01T00:00:00Z" });
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.getByText("UNKNOWN")).toBeInTheDocument();
    expect(screen.getByText("Token Health").parentElement?.parentElement).toHaveTextContent("UNKNOWN");
    expect(screen.getByText("Expected Valid Until").parentElement?.parentElement).toHaveTextContent("—");
  });

  it("preserves server INVALID even when Expected Valid Until is in the future", async () => {
    renderDrawer(api(), ACCOUNT, { ...row, provider: "antigravity", token_state: "INVALID", expected_valid_until: "2099-01-01T00:00:00Z" });
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.getByText("INVALID")).toBeInTheDocument();
    expect(screen.getByText(formatDateTime("2099-01-01T00:00:00Z"))).toBeInTheDocument();
  });

  it("replaces token diagnostics when switching accounts without extra token reads", async () => {
    const clientApi = api();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const first = { ...row, provider: "antigravity", token_state: "VALID" as const, expected_valid_until: "2026-09-11T01:00:00Z" };
    const second = { ...row, account_key: "antigravity:b@example.invalid", email: "b@example.invalid", provider: "antigravity", token_state: "INVALID" as const, expected_valid_until: "2027-01-01T00:00:00Z" };
    const missing = { ...second, token_state: undefined, expected_valid_until: null };
    const rendered = renderDrawer(clientApi, ACCOUNT, first, client);
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.getByText("VALID")).toBeInTheDocument();
    rendered.rerender(<QueryClientProvider client={client}><AccountDetailsDrawer api={clientApi} instanceId={NODE} row={second} accountKey={second.account_key} onClose={vi.fn()} onUnauthorized={vi.fn()} /></QueryClientProvider>);
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.queryByText("VALID")).not.toBeInTheDocument();
    expect(screen.queryByText(formatDateTime(first.expected_valid_until))).not.toBeInTheDocument();
    expect(screen.getByText("INVALID")).toBeInTheDocument();
    expect(screen.getByText(formatDateTime(second.expected_valid_until))).toBeInTheDocument();
    rendered.rerender(<QueryClientProvider client={client}><AccountDetailsDrawer api={clientApi} instanceId={NODE} row={missing} accountKey={missing.account_key} onClose={vi.fn()} onUnauthorized={vi.fn()} /></QueryClientProvider>);
    fireEvent.click(await screen.findByRole("tab", { name: "采集信息" }));
    expect(screen.queryByText("INVALID")).not.toBeInTheDocument();
    expect(screen.queryByText(formatDateTime(second.expected_valid_until))).not.toBeInTheDocument();
    expect(screen.getByText("Expected Valid Until").parentElement?.parentElement).toHaveTextContent("—");
    expect(clientApi.requestHistory).toHaveBeenCalledTimes(2);
    expect(clientApi.accountAvailabilityOccurrences).not.toHaveBeenCalled();
  });
});

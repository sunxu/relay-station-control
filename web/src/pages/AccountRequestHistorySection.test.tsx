import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountRequestHistorySection } from "./AccountRequestHistorySection";
import type { AccountRequestHistoryApi } from "../api/account-request-history-types";
import { TopologyApiError } from "../api/topology-types";

const NODE = "11111111-1111-4111-8111-111111111111";
const ACCOUNT = "openai:alice@example.invalid";
const ACCOUNT2 = "openai:bob@example.invalid";
const event = { occurred_at: "2026-09-08T01:02:03Z", model: "gpt-5", success: true, failure_class: null, duration_ms: 123, request_id: "req-1" };
function api(result: unknown = { instance_id: NODE, account_key: ACCOUNT, items: [], next_cursor: null }): AccountRequestHistoryApi {
  return { requestHistory: vi.fn().mockResolvedValue(result) };
}
function renderSection(historyApi: AccountRequestHistoryApi, accountKey?: string, instanceId = NODE, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return { client, ...render(<QueryClientProvider client={client}><AccountRequestHistorySection api={historyApi} instanceId={instanceId} accountKey={accountKey} onUnauthorized={vi.fn()} /></QueryClientProvider>) };
}

it("shows the unselected state without requesting history", () => {
  const historyApi = api();
  renderSection(historyApi);
  expect(screen.getByText("请选择账号查看请求历史")).toBeInTheDocument();
  expect(historyApi.requestHistory).not.toHaveBeenCalled();
});

it("shows independent loading", () => {
  const historyApi = { requestHistory: vi.fn().mockReturnValue(new Promise(() => {})) };
  renderSection(historyApi, ACCOUNT);
  expect(screen.getByRole("status", { name: "正在读取请求历史" })).toBeInTheDocument();
});

it("renders populated success and failed records with nullable fields", async () => {
  const historyApi = api({ instance_id: NODE, account_key: ACCOUNT, next_cursor: null, items: [event, { occurred_at: "2026-09-08T01:03:03Z", model: "", success: false, failure_class: "rate_limit", duration_ms: null, request_id: "" }] });
  renderSection(historyApi, ACCOUNT);
  expect(await screen.findByText("gpt-5")).toBeInTheDocument();
  expect(screen.getByText("Success")).toBeInTheDocument();
  expect(screen.getByText("Failed")).toBeInTheDocument();
  expect(screen.getByText("rate_limit")).toBeInTheDocument();
  expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(3);
  expect(screen.getByText("123 ms")).toBeInTheDocument();
  expect(screen.getByText("req-1")).toBeInTheDocument();
});

it("shows a successful empty result", async () => {
  renderSection(api(), ACCOUNT);
  expect(await screen.findByText("最近 7 天暂无请求历史")).toBeInTheDocument();
});

it("shows unavailable and retries", async () => {
  const historyApi = { requestHistory: vi.fn().mockRejectedValueOnce(new TopologyApiError(503)).mockResolvedValue({ instance_id: NODE, account_key: ACCOUNT, items: [], next_cursor: null }) };
  renderSection(historyApi, ACCOUNT);
  const alert = await screen.findByText("读取不可用（unavailable）");
  fireEvent.click(alert.closest(".ant-alert")!.querySelector("button")!);
  await waitFor(() => expect(screen.getByText("最近 7 天暂无请求历史")).toBeInTheDocument());
  expect(historyApi.requestHistory).toHaveBeenCalledTimes(2);
});

it("shows not found separately from empty", async () => {
  const historyApi = api();
  historyApi.requestHistory = vi.fn().mockRejectedValue(new TopologyApiError(404));
  renderSection(historyApi, ACCOUNT);
  expect(await screen.findByText("账号不存在（not found）")).toBeInTheDocument();
  expect(screen.queryByText("最近 7 天暂无请求历史")).not.toBeInTheDocument();
});

it("clears the session on 401", async () => {
  const onUnauthorized = vi.fn();
  const historyApi = { requestHistory: vi.fn().mockRejectedValue(new TopologyApiError(401)) };
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AccountRequestHistorySection api={historyApi} instanceId={NODE} accountKey={ACCOUNT} onUnauthorized={onUnauthorized} /></QueryClientProvider>);
  await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
});

it("requests the next page with cursor and returns home", async () => {
  const historyApi = { requestHistory: vi.fn().mockResolvedValueOnce({ instance_id: NODE, account_key: ACCOUNT, items: [event], next_cursor: "next" }).mockResolvedValue({ instance_id: NODE, account_key: ACCOUNT, items: [], next_cursor: null }) };
  renderSection(historyApi, ACCOUNT);
  await screen.findByText("req-1");
  fireEvent.click(screen.getByRole("button", { name: "History 下一页" }));
  await waitFor(() => expect(historyApi.requestHistory).toHaveBeenLastCalledWith(NODE, ACCOUNT, "next", expect.any(AbortSignal)));
  await screen.findByText("最近 7 天暂无请求历史");
  fireEvent.click(screen.getByRole("button", { name: "History 首页" }));
  await waitFor(() => expect(historyApi.requestHistory).toHaveBeenLastCalledWith(NODE, ACCOUNT, undefined, expect.any(AbortSignal)));
});

it("resets pagination when account changes", async () => {
  const historyApi = { requestHistory: vi.fn().mockResolvedValue({ instance_id: NODE, account_key: ACCOUNT, items: [event], next_cursor: "next" }) };
  const view = renderSection(historyApi, ACCOUNT);
  await screen.findByText("req-1");
  fireEvent.click(screen.getByRole("button", { name: "History 下一页" }));
  await waitFor(() => expect(historyApi.requestHistory).toHaveBeenLastCalledWith(NODE, ACCOUNT, "next", expect.any(AbortSignal)));
  view.rerender(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AccountRequestHistorySection api={historyApi} instanceId={NODE} accountKey={ACCOUNT2} onUnauthorized={vi.fn()} /></QueryClientProvider>);
  await waitFor(() => expect(historyApi.requestHistory).toHaveBeenLastCalledWith(NODE, ACCOUNT2, undefined, expect.any(AbortSignal)));
});

it("cancels and isolates a late response after Node changes", async () => {
  let resolveOld!: (value: unknown) => void;
  const old = new Promise((resolve) => { resolveOld = resolve; });
  let oldSignal!: AbortSignal;
  const historyApi: AccountRequestHistoryApi = { requestHistory: vi.fn((node: string, _account: string, _cursor?: string, signal?: AbortSignal) => { if (node === NODE) { oldSignal = signal!; return old as Promise<never>; } return Promise.resolve({ instance_id: "22222222-2222-4222-8222-222222222222", account_key: ACCOUNT, items: [{ ...event, request_id: "new" }], next_cursor: null }); }) };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = renderSection(historyApi, ACCOUNT, NODE, client);
  await waitFor(() => expect(historyApi.requestHistory).toHaveBeenCalled());
  const nextNode = "22222222-2222-4222-8222-222222222222";
  view.rerender(<QueryClientProvider client={client}><AccountRequestHistorySection api={historyApi} instanceId={nextNode} accountKey={ACCOUNT} onUnauthorized={vi.fn()} /></QueryClientProvider>);
  expect(oldSignal.aborted).toBe(true);
  expect(await screen.findByText("new")).toBeInTheDocument();
  resolveOld({ instance_id: NODE, account_key: ACCOUNT, items: [{ ...event, request_id: "old" }], next_cursor: null });
  await waitFor(() => expect(screen.queryByText("old")).not.toBeInTheDocument());
});

it("ignores a late 401 from the old Node after switching", async () => {
  let rejectOld!: (reason: unknown) => void;
  const old = new Promise<never>((_, reject) => { rejectOld = reject; });
  const nextNode = "22222222-2222-4222-8222-222222222222";
  let oldSignal!: AbortSignal;
  const historyApi: AccountRequestHistoryApi = { requestHistory: vi.fn((node: string, _account: string, _cursor?: string, signal?: AbortSignal) => {
    if (node === NODE) { oldSignal = signal!; return old; }
    return Promise.resolve({ instance_id: nextNode, account_key: ACCOUNT, items: [{ ...event, request_id: "new-node" }], next_cursor: null });
  }) };
  const onUnauthorized = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><AccountRequestHistorySection api={historyApi} instanceId={NODE} accountKey={ACCOUNT} onUnauthorized={onUnauthorized} /></QueryClientProvider>);
  await waitFor(() => expect(historyApi.requestHistory).toHaveBeenCalled());
  view.rerender(<QueryClientProvider client={client}><AccountRequestHistorySection api={historyApi} instanceId={nextNode} accountKey={ACCOUNT} onUnauthorized={onUnauthorized} /></QueryClientProvider>);
  expect(oldSignal.aborted).toBe(true);
  expect(await screen.findByText("new-node")).toBeInTheDocument();
  rejectOld(new TopologyApiError(401));
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(onUnauthorized).not.toHaveBeenCalled();
});

it("exposes no mutation actions", async () => {
  renderSection(api({ instance_id: NODE, account_key: ACCOUNT, items: [event], next_cursor: null }), ACCOUNT);
  await screen.findByText("req-1");
  expect(screen.queryByRole("button", { name: /bind|rebind|unbind|disable|删除|修改/i })).not.toBeInTheDocument();
});

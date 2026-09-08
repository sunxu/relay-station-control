import { StrictMode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TopologyView } from "./TopologyView";
import type { AssetApi } from "../api/asset-types";
import type { TopologyApi } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const page = (id = A) => ({ instance_id: id, window: "15m" as const, items: [], next_cursor: null });
function fixture() {
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [], nextCursor: null }),
    node: vi.fn().mockImplementation(async (id) => ({ instanceId: id, displayName: id, capabilities: ["management_account_inventory_read"] })) } as unknown as AssetApi;
  const api = {
    accountList: vi.fn().mockResolvedValue(page()),
    providers: vi.fn().mockResolvedValue({ providers: [], observed_at: "2026-09-09T00:00:00Z" }),
    binding: vi.fn().mockResolvedValue({ resolution: "unbound", context_source: "none" }),
    currentDuplicates: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    history: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    incidents: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    requestHistory: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
  } as unknown as TopologyApi;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const unauthorized = vi.fn();
  function mount(initialInstanceId?: string) {
    return render(<StrictMode><QueryClientProvider client={client}><TopologyView api={api} assetApi={assetApi} initialInstanceId={initialInstanceId} csrfToken="csrf" onUnauthorized={unauthorized} inventoryApi={{ query: vi.fn(), capacity: vi.fn() }} /></QueryClientProvider></StrictMode>);
  }
  return { api, client, mount, unauthorized };
}
afterEach(() => window.history.replaceState(null, "", "/"));

it("loads the Node default page once in StrictMode and never replays on filter edits", async () => {
  const { api, client, mount } = fixture();
  window.history.replaceState(null, "", `/topology?instance_id=${A}`);
  mount(A);
  await screen.findByText("没有 Inventory 账号或匹配账号");
  expect(api.accountList).toHaveBeenCalledTimes(1);
  expect(api.accountList).toHaveBeenLastCalledWith(A, expect.objectContaining({ lifecycle: "present", window: "15m", limit: 25, cursor: undefined }), "csrf", expect.any(AbortSignal));
  fireEvent.change(screen.getByLabelText("邮箱精确筛选"), { target: { value: " Canary@EXAMPLE.INVALID " } });
  fireEvent.change(screen.getByLabelText("Provider 精确筛选"), { target: { value: " OpenAI " } });
  expect(api.accountList).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(2));
  expect(api.accountList).toHaveBeenLastCalledWith(A, expect.objectContaining({ email: "canary@example.invalid", provider: "openai", cursor: undefined }), "csrf", expect.any(AbortSignal));
  expect(window.location.search).toBe(`?instance_id=${A}`);
  expect(JSON.stringify(client.getQueryCache().getAll().map(q => q.queryKey))).not.toContain("canary");
});

it("offers environment capacity without a selected Node and makes no account request", async () => {
  const { api, mount } = fixture();
  mount();
  expect(screen.getByRole("button", { name: "刷新容量" })).toBeInTheDocument();
  await act(async () => { await Promise.resolve(); });
  expect(api.accountList).not.toHaveBeenCalled();
});

it("aborts account reads on popstate Node changes and when Node is cleared", async () => {
  const { api, mount } = fixture();
  let resolveA!: (value: ReturnType<typeof page>) => void;
  api.accountList = vi.fn().mockImplementationOnce(() => new Promise<ReturnType<typeof page>>(resolve => { resolveA = resolve; })).mockImplementation(() => new Promise(() => {}));
  mount(A);
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(1));
  const signalA = vi.mocked(api.accountList!).mock.calls[0]?.[3]!;
  act(() => { window.history.pushState(null, "", `/topology?instance_id=${B}`); window.dispatchEvent(new PopStateEvent("popstate")); });
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(2));
  expect(signalA.aborted).toBe(true);
  const signalB = vi.mocked(api.accountList!).mock.calls[1]?.[3]!;
  await act(async () => { resolveA(page(A)); });
  expect(screen.queryByText("没有 Inventory 账号或匹配账号")).not.toBeInTheDocument();
  act(() => { window.history.pushState(null, "", "/topology"); window.dispatchEvent(new PopStateEvent("popstate")); });
  expect(signalB.aborted).toBe(true);
  expect(api.accountList).toHaveBeenCalledTimes(2);
});

it.each([[400, "筛选条件或分页凭据无效"], [403, "当前会话无权"], [404, "所选 Node 不存在"], [409, "所选 Node 不支持"], [503, "unavailable"]] as const)("keeps HTTP %i distinct from empty and allows explicit retry", async (status, message) => {
  const { api, mount } = fixture();
  api.accountList = vi.fn().mockRejectedValueOnce(new TopologyApiError(status)).mockResolvedValue(page());
  mount(A);
  expect(await screen.findByText(new RegExp(message))).toBeInTheDocument();
  expect(api.accountList).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("没有 Inventory 账号或匹配账号")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /重\s*试/ }));
  expect(await screen.findByText("没有 Inventory 账号或匹配账号")).toBeInTheDocument();
  expect(api.accountList).toHaveBeenCalledTimes(2);
});

it("submits basic status and page size explicitly and keeps previous-page cursors consistent", async () => {
  const { api, mount } = fixture();
  api.accountList = vi.fn().mockImplementation(async (id, filters) => ({ ...page(id), next_cursor: !filters.cursor ? "page-two" : filters.cursor === "page-two" ? "page-three" : null }));
  mount(A);
  await screen.findByText("没有 Inventory 账号或匹配账号");
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "最后报告基础状态" }));
  fireEvent.click(await screen.findByText("disabled", { selector: ".ant-select-item-option-content" }));
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "每页账号数" }));
  fireEvent.click(await screen.findByText("50 / 页", { selector: ".ant-select-item-option-content" }));
  expect(api.accountList).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(2));
  expect(api.accountList).toHaveBeenLastCalledWith(A, expect.objectContaining({ basicStatus: "disabled", limit: 50, cursor: undefined }), "csrf", expect.any(AbortSignal));
  await screen.findByText("没有 Inventory 账号或匹配账号");
  fireEvent.click(screen.getByRole("button", { name: "账号下一页" }));
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(3));
  await screen.findByText("没有 Inventory 账号或匹配账号");
  fireEvent.click(screen.getByRole("button", { name: "账号下一页" }));
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(4));
  await screen.findByText("没有 Inventory 账号或匹配账号");
  fireEvent.click(screen.getByRole("button", { name: "账号上一页" }));
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(5));
  expect(api.accountList).toHaveBeenLastCalledWith(A, expect.objectContaining({ basicStatus: "disabled", limit: 50, cursor: "page-two" }), "csrf", expect.any(AbortSignal));
  await screen.findByText("没有 Inventory 账号或匹配账号");
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "每页账号数" }));
  fireEvent.click(await screen.findByText("100 / 页", { selector: ".ant-select-item-option-content" }));
  expect(api.accountList).toHaveBeenCalledTimes(5);
  fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
  await waitFor(() => expect(api.accountList).toHaveBeenCalledTimes(6));
  expect(api.accountList).toHaveBeenLastCalledWith(A, expect.objectContaining({ basicStatus: "disabled", limit: 100, cursor: undefined }), "csrf", expect.any(AbortSignal));
  expect(await screen.findByRole("button", { name: "账号上一页" })).toBeDisabled();
});

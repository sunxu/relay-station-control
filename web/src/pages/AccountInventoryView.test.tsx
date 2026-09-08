import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode, type ReactNode } from "react";
import type { AccountInventoryApi, AccountInventoryItem } from "../api/account-inventory-types";
import { AccountInventoryApiError } from "../api/account-inventory-types";
import type { AssetApi, NodeAsset } from "../api/asset-types";
import { AccountInventoryView } from "./AccountInventoryView";

const instanceId = "00000000-0000-4000-8000-000000000101";
const node: NodeAsset = {
  instanceId,
  displayName: "Inventory Node",
  nodeType: "cliproxyapi",
  driverContractVersion: "v1",
  managementEndpoint: "https://node.invalid",
  secretConfigured: true,
  capabilities: ["management_account_inventory_read"],
  monitoringActive: true,
  monitoringEffectiveFrom: "2026-08-27T00:00:00Z",
  monitoringEffectiveTo: null,
};

const account: AccountInventoryItem = {
  instanceId,
  provider: "openai",
  email: "operator@example.invalid",
  basicStatus: "reported_active",
  lifecycle: "present",
  consecutiveMissingCount: 0,
  firstSeenAt: "2026-08-27T00:00:00Z",
  lastSeenAt: "2026-08-27T01:00:00Z",
  missingSince: null,
  outOfScopeSince: null,
  lastRefreshAt: "2026-08-27T00:55:00Z",
  nextRetryAt: null,
  sourceUpdatedAt: "2026-08-27T00:54:00Z",
  providerLastCompleteAt: "2026-08-27T01:00:00Z",
  providerDegraded: false,
  snapshotFreshness: "fresh",
};

function assetApi(): AssetApi {
  return {
    nodes: vi.fn().mockResolvedValue({ items: [node], nextCursor: null }),
  } as unknown as AssetApi;
}

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>{children}</QueryClientProvider>;
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

async function selectNode() {
  fireEvent.mouseDown(await screen.findByLabelText("Relay Node"));
  fireEvent.click(await screen.findByText(/Inventory Node ·/));
}

describe("account inventory read-only view", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/account-inventory");
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 1024 });
  });

  it("automatically loads the linked Node exactly once under StrictMode", async () => {
    window.history.replaceState(null, "", `/account-inventory?instance_id=${instanceId}`);
    const api: AccountInventoryApi = { query: vi.fn().mockResolvedValue({ items: [account], nextCursor: null }) };
    render(<StrictMode><AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} /></StrictMode>, { wrapper: Wrapper });

    expect(await screen.findByText(`Inventory Node · ${instanceId}`)).toBeInTheDocument();
    await waitFor(() => expect(api.query).toHaveBeenCalledWith("csrf-proof", expect.objectContaining({ instanceId, cursor: undefined })));
    expect(await screen.findByText("operator@example.invalid")).toBeInTheDocument();
    expect(api.query).toHaveBeenCalledTimes(1);
    fireEvent.change(screen.getByLabelText("Provider 精确筛选"), { target: { value: "openai" } });
    expect(screen.getByText("设置筛选后点击查询")).toBeInTheDocument();
    expect(api.query).toHaveBeenCalledTimes(1);
  });

  it("does not query accounts without a linked Node", async () => {
    const api: AccountInventoryApi = { query: vi.fn() };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await screen.findByLabelText("Relay Node");
    expect(api.query).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /查\s*询/ })).toBeDisabled();
  });

  it("shows a linked query failure and recovers only on explicit retry", async () => {
    window.history.replaceState(null, "", `/account-inventory?instance_id=${instanceId}`);
    const query = vi.fn().mockRejectedValueOnce(new Error("unavailable"))
      .mockResolvedValueOnce({ items: [account], nextCursor: null });
    render(<AccountInventoryView api={{ query }} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    expect(await screen.findByText("账号清单读取失败")).toBeInTheDocument();
    expect(query).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    expect(await screen.findByText("operator@example.invalid")).toBeInTheDocument();
    expect(query).toHaveBeenCalledTimes(2);
  });

  it("queries exact normalized filters, displays current semantics and persists no sensitive state", async () => {
    const api: AccountInventoryApi = { query: vi.fn().mockResolvedValue({ items: [account], nextCursor: "encrypted-page-two" }) };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await selectNode();

    fireEvent.change(screen.getByLabelText("Provider 精确筛选"), { target: { value: " OpenAI " } });
    fireEvent.change(screen.getByLabelText("邮箱精确筛选"), { target: { value: " Operator@Example.Invalid " } });
    fireEvent.mouseDown(screen.getByLabelText("生命周期"));
    fireEvent.click(await screen.findByText("present", { selector: ".ant-select-item-option-content" }));
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));

    await waitFor(() => expect(api.query).toHaveBeenCalledWith("csrf-proof", expect.objectContaining({
      instanceId,
      provider: "openai",
      lifecycle: "present",
      email: "operator@example.invalid",
      cursor: undefined,
      limit: 50,
    })));
    expect(await screen.findByText("operator@example.invalid")).toBeInTheDocument();
    expect(screen.getAllByText("最后报告基础状态")).not.toHaveLength(0);
    expect(screen.getByText("fresh")).toBeInTheDocument();
    expect(window.location.href).not.toContain("Operator");
    expect(window.localStorage.length).toBe(0);
    expect(window.sessionStorage.length).toBe(0);
    expect(screen.queryByRole("button", { name: /导出|删除|修改|补采|提升|复制/ })).not.toBeInTheDocument();
  });

  it("pages in both directions and clears the old cursor when a filter changes", async () => {
    const query = vi.fn()
      .mockResolvedValueOnce({ items: [account], nextCursor: "encrypted-page-two" })
      .mockResolvedValueOnce({ items: [{ ...account, email: "second@example.invalid" }], nextCursor: null })
      .mockResolvedValueOnce({ items: [account], nextCursor: "encrypted-page-two" });
    const api: AccountInventoryApi = { query };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await selectNode();
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    await screen.findByText("operator@example.invalid");

    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await screen.findByText("second@example.invalid");
    expect(query).toHaveBeenLastCalledWith("csrf-proof", expect.objectContaining({ cursor: "encrypted-page-two" }));
    fireEvent.click(screen.getByRole("button", { name: "上一页" }));
    await waitFor(() => expect(query).toHaveBeenLastCalledWith("csrf-proof", expect.objectContaining({ cursor: undefined })));

    fireEvent.change(screen.getByLabelText("邮箱精确筛选"), { target: { value: "new@example.invalid" } });
    expect(screen.getByText("设置筛选后点击查询")).toBeInTheDocument();
    expect(screen.queryByText("operator@example.invalid")).not.toBeInTheDocument();
  });

  it("shows fixed unsupported and unavailable errors without raw details", async () => {
    const canaries = [
      "https://endpoint-canary.invalid/private", "secret-reference-canary", "secret-value-canary",
      "poll-id-canary", "policy-id-canary", "version-canary", "commit-canary", "raw-error-canary",
    ];
    const error = new AccountInventoryApiError(409, { code: "conflict", message: canaries.join("|"), request_id: "request-fixed" });
    const api: AccountInventoryApi = { query: vi.fn().mockRejectedValue(error) };
    const client = queryClient();
    const rendered = render(
      <QueryClientProvider client={client}>
        <AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />
      </QueryClientProvider>,
    );
    await selectNode();
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    const card = screen.getByTestId("account-inventory-card");
    expect(await within(card).findByText("所选 Node 不支持账号清单查询。")).toBeInTheDocument();
    for (const canary of canaries) expect(card.textContent).not.toContain(canary);
    rendered.unmount();
    await waitFor(() => expect(client.getMutationCache().getAll().every((mutation) => mutation.state.error == null)).toBe(true));
    const finalUIArtifact = JSON.stringify({
      dom: document.body.textContent,
      href: window.location.href,
      history: window.history.state,
      localStorage: { ...window.localStorage },
      sessionStorage: { ...window.sessionStorage },
      queryCache: client.getQueryCache().getAll(),
      mutationErrors: client.getMutationCache().getAll().map((mutation) => mutation.state.error),
    });
    for (const canary of canaries) expect(finalUIArtifact).not.toContain(canary);
  });

  it("hands a 401 to the authentication flow", async () => {
    const onUnauthorized = vi.fn();
    const api: AccountInventoryApi = { query: vi.fn().mockRejectedValue(new AccountInventoryApiError(401, { code: "unauthorized", message: "fixed", request_id: "fixed" })) };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={onUnauthorized} />, { wrapper: Wrapper });
    await selectNode();
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("rejects an expired cursor without exposing it and restarts at the first page after a filter reset", async () => {
    const expiredCursor = "opaque-expired-cursor-canary";
    const rawErrorCanary = "cursor plaintext:openai:private@example.invalid";
    const query = vi.fn()
      .mockResolvedValueOnce({ items: [account], nextCursor: expiredCursor })
      .mockRejectedValueOnce(new AccountInventoryApiError(400, {
        code: "validation_failed", message: rawErrorCanary, request_id: "request-fixed",
      }))
      .mockResolvedValueOnce({ items: [account], nextCursor: null });
    const api: AccountInventoryApi = { query };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await selectNode();
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    await screen.findByText("operator@example.invalid");

    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByText("筛选条件或分页凭据无效，请清除筛选并重新查询。")).toBeInTheDocument();
    const card = screen.getByTestId("account-inventory-card");
    expect(card.textContent).not.toContain(expiredCursor);
    expect(card.textContent).not.toContain(rawErrorCanary);
    expect(query).toHaveBeenLastCalledWith("csrf-proof", expect.objectContaining({ cursor: expiredCursor }));

    fireEvent.change(screen.getByLabelText("邮箱精确筛选"), { target: { value: " reset@example.invalid " } });
    expect(screen.getByText("设置筛选后点击查询")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "上一页" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("邮箱精确筛选"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    await waitFor(() => expect(query).toHaveBeenLastCalledWith("csrf-proof", expect.objectContaining({
      cursor: undefined,
      email: undefined,
    })));
  });

  it("discards email, cursor history and mutation data when the view unmounts", async () => {
    const sensitiveEmail = "unmount-canary@example.invalid";
    const sensitiveCursor = "unmount-opaque-cursor";
    const query = vi.fn().mockResolvedValue({ items: [{ ...account, email: sensitiveEmail }], nextCursor: sensitiveCursor });
    const api: AccountInventoryApi = { query };
    const client = queryClient();
    const renderView = () => render(
      <QueryClientProvider client={client}>
        <AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />
      </QueryClientProvider>,
    );
    const first = renderView();
    await selectNode();
    fireEvent.change(screen.getByLabelText("邮箱精确筛选"), { target: { value: sensitiveEmail } });
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    expect(await screen.findByText(sensitiveEmail)).toBeInTheDocument();
    first.unmount();

    await waitFor(() => expect(client.getMutationCache().getAll().every((mutation) => mutation.state.data === undefined)).toBe(true));
    expect(client.getQueryCache().getAll()).toHaveLength(0);
    const persistedState = JSON.stringify({
      href: window.location.href,
      history: window.history.state,
      localStorage: { ...window.localStorage },
      sessionStorage: { ...window.sessionStorage },
      queryCache: client.getQueryCache().getAll(),
      mutationData: client.getMutationCache().getAll().map((mutation) => mutation.state.data),
    });
    expect(persistedState).not.toContain(sensitiveEmail);
    expect(persistedState).not.toContain(sensitiveCursor);
    renderView();
    expect(await screen.findByLabelText("邮箱精确筛选")).toHaveValue("");
    expect(screen.queryByText(sensitiveEmail)).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain(sensitiveCursor);
    await selectNode();
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    await waitFor(() => expect(query).toHaveBeenLastCalledWith("csrf-proof", expect.objectContaining({
      cursor: undefined,
      email: undefined,
    })));
  });

  it("keeps all filters, paging controls and the scrollable table accessible at a narrow viewport", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 360 });
    window.dispatchEvent(new Event("resize"));
    const api: AccountInventoryApi = {
      query: vi.fn().mockResolvedValue({ items: [account], nextCursor: "opaque-page-two" }),
    };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    const nodeSelect = await screen.findByRole("combobox", { name: "Relay Node" });
    const providerInput = screen.getByRole("textbox", { name: "Provider 精确筛选" });
    const lifecycleSelect = screen.getByRole("combobox", { name: "生命周期" });
    const statusSelect = screen.getByRole("combobox", { name: "最后报告基础状态" });
    const emailInput = screen.getByRole("textbox", { name: "邮箱精确筛选" });
    const pageSizeSelect = screen.getByRole("combobox", { name: "每页账号数" });
    for (const control of [nodeSelect, providerInput, lifecycleSelect, statusSelect, emailInput, pageSizeSelect]) {
      control.focus();
      expect(control).toHaveFocus();
      expect(control).toHaveAccessibleName();
      expect(control.tabIndex).toBe(0);
    }
    const filterGroup = screen.getByLabelText("账号清单过滤器");
    expect(filterGroup).toContainElement(nodeSelect);
    expect(filterGroup).toContainElement(emailInput);
    expect(filterGroup).toContainElement(pageSizeSelect);

    await selectNode();
    const queryButton = screen.getByRole("button", { name: /查\s*询/ });
    expect(queryButton).toBeEnabled();
    expect(queryButton.tabIndex).toBe(0);
    queryButton.focus();
    expect(queryButton).toHaveFocus();
    fireEvent.click(queryButton);
    const table = await screen.findByRole("table");
    expect(table).toBeInTheDocument();
    expect(document.querySelector(".ant-table-content")).toHaveStyle({ overflowX: "auto" });
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled();
  });

  it("exposes no export, copy, detail, selection or mutation surface", async () => {
    const api: AccountInventoryApi = { query: vi.fn().mockResolvedValue({ items: [account], nextCursor: null }) };
    render(<AccountInventoryView api={api} assetApi={assetApi()} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await selectNode();
    fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
    const table = await screen.findByRole("table");
    const forbidden = /历史|压缩|导出|下载|复制|详情|查看详情|批量|选择全部|编辑|修改|删除|补采|重试采集|promotion|提升/i;
    expect(screen.queryByRole("button", { name: forbidden })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: forbidden })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: forbidden })).not.toBeInTheDocument();
    expect(within(table).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(within(table).queryByRole("button")).not.toBeInTheDocument();
    expect(within(table).queryByRole("link")).not.toBeInTheDocument();
    expect(within(table).queryByText("操作", { selector: "th" })).not.toBeInTheDocument();
    expect(within(table).getAllByRole("columnheader").map((header) => header.textContent)).toEqual([
      "邮箱", "Provider", "最后报告基础状态", "生命周期", "最近出现", "最近刷新", "下次重试", "Provider 快照",
    ]);
  });
});

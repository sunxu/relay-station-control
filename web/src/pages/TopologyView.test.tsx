import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { TopologyView } from "./TopologyView";
import type { AssetApi, NodeAsset } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import type { TopologyApi } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const unknownNode = "99999999-9999-4999-8999-999999999999";
const observedAt = "2026-09-07T00:00:00Z";

function makeNode(instanceId: string, displayName: string): NodeAsset {
  return { instanceId, displayName, nodeType: "relay", driverContractVersion: "v1", managementEndpoint: "https://node.invalid", secretConfigured: true, capabilities: [], monitoringActive: true, monitoringEffectiveFrom: null, monitoringEffectiveTo: null };
}

function emptyTopology(): TopologyApi {
  return {
    accountQuality: vi.fn().mockResolvedValue({ instance_id: A, window: "15m", items: [], next_cursor: null }),
    requestHistory: vi.fn().mockResolvedValue({ instance_id: A, account_key: "openai:a@example.invalid", items: [], next_cursor: null }),
    providers: vi.fn().mockResolvedValue({ instance_id: A, observed_at: observedAt, providers: [] }),
    binding: vi.fn().mockResolvedValue({ relay_node_id: A, resolution: "unbound", directory_freshness: "fresh", context_source: "none", observed_at: observedAt }),
    currentDuplicates: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    evidence: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    history: vi.fn().mockResolvedValue({ involvement: "historical", instance_id: A, observed_at: observedAt, items: [], next_cursor: null }),
  };
}

function renderView(api: TopologyApi, assetApi: AssetApi, initialInstanceId: string | undefined = A, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return { client, ...render(<QueryClientProvider client={client}><TopologyView api={api} assetApi={assetApi} initialInstanceId={initialInstanceId} onUnauthorized={vi.fn()} /></QueryClientProvider>) };
}

it("keeps a late A response from replacing the selected B Node", async () => {
  const a = makeNode(A, "Node A");
  const b = makeNode(B, "Node B");
  let resolveA!: (value: NodeAsset) => void;
  const delayedA = new Promise<NodeAsset>((resolve) => { resolveA = resolve; });
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [a, b], nextCursor: null }), node: vi.fn((id: string) => id === A ? delayedA : Promise.resolve(b)) } as unknown as AssetApi;
  const api = emptyTopology();
  let resolveProvidersA!: (value: Awaited<ReturnType<TopologyApi["providers"]>>) => void;
  const delayedProvidersA = new Promise<Awaited<ReturnType<TopologyApi["providers"]>>>((resolve) => { resolveProvidersA = resolve; });
  const providersA: Awaited<ReturnType<TopologyApi["providers"]>> = { instance_id: A, observed_at: observedAt, providers: [{ provider: "provider-a", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: observedAt, snapshot_freshness: "fresh", health_scheduled_at: observedAt, health_degraded: false, health_reason: null }] };
  const providersB: Awaited<ReturnType<TopologyApi["providers"]>> = { instance_id: B, observed_at: observedAt, providers: [{ provider: "provider-b", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: observedAt, snapshot_freshness: "fresh", health_scheduled_at: observedAt, health_degraded: false, health_reason: null }] };
  let signalA!: AbortSignal;
  api.providers = vi.fn((id: string, signal?: AbortSignal) => { if (id === A) { signalA = signal!; return delayedProvidersA; } return Promise.resolve(providersB); });
  renderView(api, assetApi);

  const combo = await screen.findByRole("combobox", { name: "Relay Node" });
  fireEvent.mouseDown(combo);
  fireEvent.click(await screen.findByText(`Node B · ${B}`));
  expect(await screen.findByText("Node：Node B")).toBeInTheDocument();
  expect(await screen.findByText("provider-b")).toBeInTheDocument();
  expect(signalA.aborted).toBe(true);
  resolveA(a);
  resolveProvidersA(providersA);
  await waitFor(() => expect(screen.getByText("Node：Node B")).toBeInTheDocument());
  expect(screen.queryByText("Node：Node A")).not.toBeInTheDocument();
  expect(screen.getByText("provider-b")).toBeInTheDocument();
  expect(screen.queryByText("provider-a")).not.toBeInTheDocument();
});

it("renders independent provider freshness and health, plus unbound binding", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.providers = vi.fn().mockResolvedValue({ instance_id: A, observed_at: observedAt, providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: observedAt, snapshot_freshness: "fresh", health_scheduled_at: observedAt, health_degraded: true, health_reason: "transport_failed" }] });
  renderView(api, assetApi);

  expect((await screen.findAllByText("fresh")).length).toBeGreaterThan(0);
  expect(screen.getByText("degraded")).toBeInTheDocument();
  expect(screen.getByText("transport_failed")).toBeInTheDocument();
  expect(screen.getByText("unbound")).toBeInTheDocument();
});

it.each(["resolved", "unresolved", "unknown", "unbound"] as const)("renders Binding resolution %s and lossless last_known context", async (resolution) => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.binding = vi.fn().mockResolvedValue({ relay_node_id: A, resolution, directory_freshness: "fresh", context_source: "last_known", gateway_instance_id: "gateway-1", gateway_account_id: "9223372036854775807", account_context: { account_id: "9223372036854775807", name: "last-known", platform: "test", type: "standard", status: "active" }, observed_at: observedAt });
  renderView(api, assetApi);

  expect(await screen.findByText(resolution)).toBeInTheDocument();
  expect(screen.getByText((_, element) => element?.textContent === "Context source：last_known")).toBeInTheDocument();
  expect(screen.getAllByText((_, element) => element?.textContent?.includes("9223372036854775807") ?? false).length).toBeGreaterThan(0);
});

it("retains an unknown selected UUID instead of falling back to a listed Node", async () => {
  const a = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [a], nextCursor: null }), node: vi.fn().mockRejectedValue(new AssetApiError(404)) } as unknown as AssetApi;
  renderView(emptyTopology(), assetApi, unknownNode);

  expect(await screen.findByText(`Node：${unknownNode}`)).toBeInTheDocument();
  expect(screen.getByText(`Instance ID：${unknownNode}`)).toBeInTheDocument();
  expect(screen.queryByText("Node：Node A")).not.toBeInTheDocument();
});

it("shows a local 503 as an error instead of an empty Provider state", async () => {
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [makeNode(A, "Node A")], nextCursor: null }), node: vi.fn().mockResolvedValue(makeNode(A, "Node A")) } as unknown as AssetApi;
  const api = emptyTopology();
  api.providers = vi.fn().mockRejectedValue(new TopologyApiError(503));
  renderView(api, assetApi);

  expect(await screen.findByText("读取不可用（unavailable）")).toBeInTheDocument();
  expect(screen.queryByText("没有应监控或已持有 state 的 Provider")).not.toBeInTheDocument();
});

it("keeps resolved history visible when current affected Nodes are empty", async () => {
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [makeNode(A, "Node A")], nextCursor: null }), node: vi.fn().mockResolvedValue(makeNode(A, "Node A")) } as unknown as AssetApi;
  const api = emptyTopology();
  api.history = vi.fn().mockResolvedValue({ involvement: "historical", instance_id: A, observed_at: observedAt, next_cursor: null, items: [{ occurrence_id: "occ-1", environment_id: "env-1", account_key: "acct-1", conflict_type: "duplicate", status: "RESOLVED", severity: "Critical", first_seen_at: observedAt, last_seen_at: observedAt, resolved_at: observedAt, evidence_state: "complete", last_fully_verified_at: observedAt, latest_evaluation_id: "eval-1", affected_nodes: [] }] });
  renderView(api, assetApi);

  expect(await screen.findByText("RESOLVED")).toBeInTheDocument();
  expect(screen.getAllByText("当前集合为空").length).toBeGreaterThan(0);
  expect(screen.getAllByText("current").length).toBeGreaterThan(0);
  expect(screen.getByText(/historical involvement/)).toBeInTheDocument();
});

it("clears the query cache and calls onUnauthorized after a 401", async () => {
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [makeNode(A, "Node A")], nextCursor: null }), node: vi.fn().mockResolvedValue(makeNode(A, "Node A")) } as unknown as AssetApi;
  const api = emptyTopology();
  api.providers = vi.fn().mockRejectedValue(new TopologyApiError(401));
  const onUnauthorized = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["private-canary"], "secret");
  render(<QueryClientProvider client={client}><TopologyView api={api} assetApi={assetApi} initialInstanceId={A} onUnauthorized={onUnauthorized} /></QueryClientProvider>);

  await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
  expect(client.getQueryData(["private-canary"])).toBeUndefined();
});

it("renders account quality metrics and an unknown zero-request row", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockResolvedValue({ instance_id: A, window: "15m", next_cursor: null, items: [
    { account_key: "openai:a@example.invalid", email: "a@example.invalid", provider: "openai", quality: "good", request_count: 20, success_count: 20, failure_count: 0, success_rate: 1, p95_latency_ms: 120, last_success_at: observedAt, last_failure_at: null, last_failure_class: null },
    { account_key: "openai:b@example.invalid", email: "b@example.invalid", provider: "openai", quality: "unknown", request_count: 0, success_count: 0, failure_count: 0, success_rate: null, p95_latency_ms: null, last_success_at: null, last_failure_at: null, last_failure_class: null },
  ] });
  renderView(api, assetApi);
  expect(await screen.findByText("a@example.invalid")).toBeInTheDocument();
  expect(screen.getByText("100.0%")).toBeInTheDocument();
  expect(screen.getByText("120 ms")).toBeInTheDocument();
  expect(screen.getByText("Unknown")).toBeInTheDocument();
  const unknownRow = screen.getByText("b@example.invalid").closest("tr")!;
  expect(within(unknownRow).getByText("0")).toBeInTheDocument();
  expect(within(unknownRow).getAllByText("—")).toHaveLength(4);
  expect(within(screen.getByRole("region", { name: "Account Quality" })).queryByRole("button", { name: /bind|disable|delete|quota|inspect/i })).not.toBeInTheDocument();
});

it("passes window/provider/quality filters and keeps quality pagination bounded", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.providers = vi.fn().mockResolvedValue({ instance_id: A, observed_at: observedAt, providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: observedAt, snapshot_freshness: "fresh", health_scheduled_at: observedAt, health_degraded: false, health_reason: null }] });
  const quality = vi.fn()
    .mockResolvedValueOnce({ instance_id: A, window: "15m", next_cursor: "quality-page-2", items: [{ account_key: "openai:a@example.invalid", email: "a@example.invalid", provider: "openai", quality: "degraded", request_count: 5, success_count: 4, failure_count: 1, success_rate: .8, p95_latency_ms: 400, last_success_at: null, last_failure_at: observedAt, last_failure_class: "upstream" }] })
    .mockResolvedValue({ instance_id: A, window: "1h", next_cursor: null, items: [] });
  api.accountQuality = quality;
  renderView(api, assetApi);
  await screen.findByText("a@example.invalid");
  expect(within(screen.getByRole("region", { name: "Account Quality" })).getByText("upstream")).toBeInTheDocument();
  expect(within(screen.getByRole("region", { name: "Account Quality" })).getByText("2026-09-07 00:00:00 UTC")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "质量下一页" }));
  await waitFor(() => expect(quality).toHaveBeenCalledWith(A, "15m", undefined, undefined, "quality-page-2", expect.any(AbortSignal)));
  const windowSelect = screen.getByRole("combobox", { name: "质量窗口" });
  fireEvent.mouseDown(windowSelect);
  fireEvent.click(await screen.findByText("最近 1 小时"));
  const providerSelect = screen.getByRole("combobox", { name: "质量 Provider" });
  fireEvent.mouseDown(providerSelect);
  fireEvent.click(screen.getAllByText("openai").at(-1)!);
  const qualitySelect = screen.getByRole("combobox", { name: "质量分类" });
  fireEvent.mouseDown(qualitySelect);
  fireEvent.click(await screen.findByText("Bad"));
  await waitFor(() => expect(quality).toHaveBeenCalledWith(A, "1h", "openai", "bad", undefined, expect.any(AbortSignal)));
  expect(screen.getByText("没有 Inventory 账号或匹配账号")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "质量下一页" })).toBeDisabled();
});

it("shows unavailable quality reads instead of empty", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockRejectedValue(new TopologyApiError(503));
  renderView(api, assetApi);
  expect(await screen.findByText("读取不可用（unavailable）")).toBeInTheDocument();
  expect(screen.queryByText("没有 Inventory 账号或匹配账号")).not.toBeInTheDocument();
});

it("keeps the selected Node quality isolated from a late previous response", async () => {
  const a = makeNode(A, "Node A");
  const b = makeNode(B, "Node B");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [a, b], nextCursor: null }), node: vi.fn((id: string) => Promise.resolve(id === A ? a : b)) } as unknown as AssetApi;
  const api = emptyTopology();
  let resolveA!: (value: Awaited<ReturnType<TopologyApi["accountQuality"]>>) => void;
  const delayedA = new Promise<Awaited<ReturnType<TopologyApi["accountQuality"]>>>((resolve) => { resolveA = resolve; });
  api.accountQuality = vi.fn((id: string) => id === A ? delayedA : Promise.resolve({ instance_id: B, window: "15m" as const, next_cursor: null, items: [{ account_key: "openai:b@example.invalid", email: "b@example.invalid", provider: "openai", quality: "good" as const, request_count: 1, success_count: 1, failure_count: 0, success_rate: 1, p95_latency_ms: 10, last_success_at: observedAt, last_failure_at: null, last_failure_class: null }] })) as unknown as TopologyApi["accountQuality"];
  renderView(api, assetApi);
  const combo = await screen.findByRole("combobox", { name: "Relay Node" });
  fireEvent.mouseDown(combo);
  fireEvent.click(await screen.findByText(`Node B · ${B}`));
  expect(await screen.findByText("b@example.invalid")).toBeInTheDocument();
  resolveA({ instance_id: A, window: "15m", next_cursor: null, items: [{ account_key: "openai:a@example.invalid", email: "a@example.invalid", provider: "openai", quality: "bad", request_count: 1, success_count: 0, failure_count: 1, success_rate: 0, p95_latency_ms: 10, last_success_at: null, last_failure_at: observedAt, last_failure_class: "auth" }] });
  await waitFor(() => expect(screen.queryByText("a@example.invalid")).not.toBeInTheDocument());
});

it("shows account quality loading independently", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockReturnValue(new Promise(() => {}));
  renderView(api, assetApi);
  expect(await screen.findByRole("status", { name: "正在读取账号质量" })).toBeInTheDocument();
  expect(screen.queryByText("没有 Inventory 账号或匹配账号")).not.toBeInTheDocument();
});

it("shows a successful empty account quality result distinctly", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockResolvedValue({ instance_id: A, window: "15m", next_cursor: null, items: [] });
  renderView(api, assetApi);
  expect(await screen.findByText("没有 Inventory 账号或匹配账号")).toBeInTheDocument();
  expect(screen.queryByText("读取不可用（unavailable）")).not.toBeInTheDocument();
});

it("recovers an unavailable account quality read with retry", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockRejectedValueOnce(new TopologyApiError(503)).mockResolvedValue({ instance_id: A, window: "15m", next_cursor: null, items: [] });
  renderView(api, assetApi);
  expect(await screen.findByText("读取不可用（unavailable）")).toBeInTheDocument();
  const qualityAlert = screen.getByText("读取不可用（unavailable）").closest(".ant-alert");
  fireEvent.click(qualityAlert!.querySelector("button")!);
  await waitFor(() => expect(screen.getByText("没有 Inventory 账号或匹配账号")).toBeInTheDocument());
});

it("clears the session when account quality returns 401", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockRejectedValue(new TopologyApiError(401));
  const onUnauthorized = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TopologyView api={api} assetApi={assetApi} initialInstanceId={A} onUnauthorized={onUnauthorized} /></QueryClientProvider>);
  await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
});

it("keeps Provider source failure explicit for quality filtering", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.providers = vi.fn().mockRejectedValue(new TopologyApiError(503));
  api.accountQuality = vi.fn().mockResolvedValue({ instance_id: A, window: "15m", next_cursor: null, items: [] });
  renderView(api, assetApi);
  expect(await screen.findByText("Provider 筛选来源不可用")).toBeInTheDocument();
  expect(screen.getByText("没有 Inventory 账号或匹配账号")).toBeInTheDocument();
});

it("opens request history for the selected account row", async () => {
  const asset = makeNode(A, "Node A");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [asset], nextCursor: null }), node: vi.fn().mockResolvedValue(asset) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn().mockResolvedValue({ instance_id: A, window: "15m", next_cursor: null, items: [{ account_key: "openai:a@example.invalid", email: "a@example.invalid", provider: "openai", quality: "good", request_count: 1, success_count: 1, failure_count: 0, success_rate: 1, p95_latency_ms: 20, last_success_at: observedAt, last_failure_at: null, last_failure_class: null }] });
  api.requestHistory = vi.fn().mockResolvedValue({ instance_id: A, account_key: "openai:a@example.invalid", items: [{ occurred_at: observedAt, model: "gpt-5", success: true, failure_class: null, duration_ms: 20, request_id: "history-1" }], next_cursor: null });
  renderView(api, assetApi);
  fireEvent.click(await screen.findByRole("button", { name: "查看 History" }));
  expect(await screen.findByText("history-1")).toBeInTheDocument();
  expect(api.requestHistory).toHaveBeenCalledWith(A, "openai:a@example.invalid", undefined, expect.any(AbortSignal));
});

it("clears selected account history when switching Node", async () => {
  const a = makeNode(A, "Node A");
  const b = makeNode(B, "Node B");
  const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [a, b], nextCursor: null }), node: vi.fn((id: string) => Promise.resolve(id === A ? a : b)) } as unknown as AssetApi;
  const api = emptyTopology();
  api.accountQuality = vi.fn((id: string) => Promise.resolve({ instance_id: id, window: "15m" as const, next_cursor: null, items: id === A ? [{ account_key: "openai:a@example.invalid", email: "a@example.invalid", provider: "openai", quality: "good" as const, request_count: 1, success_count: 1, failure_count: 0, success_rate: 1, p95_latency_ms: 20, last_success_at: observedAt, last_failure_at: null, last_failure_class: null }] : [] })) as unknown as TopologyApi["accountQuality"];
  let oldSignal!: AbortSignal;
  let resolveHistory!: (value: Awaited<ReturnType<TopologyApi["requestHistory"]>>) => void;
  const pendingHistory = new Promise<Awaited<ReturnType<TopologyApi["requestHistory"]>>>((resolve) => { resolveHistory = resolve; });
  api.requestHistory = vi.fn((id: string, _account: string, _cursor?: string, signal?: AbortSignal) => { if (id === A) { oldSignal = signal!; return pendingHistory; } return Promise.resolve({ instance_id: B, account_key: "openai:a@example.invalid", items: [], next_cursor: null }); });
  renderView(api, assetApi);
  fireEvent.click(await screen.findByRole("button", { name: "查看 History" }));
  await waitFor(() => expect(api.requestHistory).toHaveBeenCalledWith(A, "openai:a@example.invalid", undefined, expect.any(AbortSignal)));
  const combo = await screen.findByRole("combobox", { name: "Relay Node" });
  fireEvent.mouseDown(combo);
  fireEvent.click(await screen.findByText(`Node B · ${B}`));
  expect(oldSignal.aborted).toBe(true);
  expect(await screen.findByText("请选择账号查看请求历史")).toBeInTheDocument();
  expect(api.requestHistory).toHaveBeenCalledTimes(1);
  resolveHistory({ instance_id: A, account_key: "openai:a@example.invalid", items: [{ occurred_at: observedAt, model: "old", success: true, failure_class: null, duration_ms: 1, request_id: "late" }], next_cursor: null });
  await waitFor(() => expect(screen.queryByText("late")).not.toBeInTheDocument());
});

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

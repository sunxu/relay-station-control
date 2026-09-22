import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MonitoringView } from "./MonitoringView";
import type { AssetApi, NodeAsset } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import type { TopologyApi } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
import type { AccountInventoryApi } from "../api/account-inventory-types";
import { AccountInventoryApiError } from "../api/account-inventory-types";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const observedAt = "2026-09-07T00:00:00Z";

function node(instanceId: string, displayName: string): NodeAsset {
  return { instanceId, displayName, nodeType: "relay", driverContractVersion: "v1", managementEndpoint: "https://node.invalid", secretConfigured: true, capabilities: [], monitoringActive: true, monitoringEffectiveFrom: null, monitoringEffectiveTo: null, lifecycleStatus: "active", revision: "1", retiredAt: null, retiredBy: null, retireReason: null };
}
function topology(): TopologyApi {
  return { providers: vi.fn().mockResolvedValue({ instance_id: A, observed_at: observedAt, providers: [] }), binding: vi.fn().mockResolvedValue({ relay_node_id: A, resolution: "unbound", directory_freshness: "fresh", context_source: "none", observed_at: observedAt }), currentDuplicates: vi.fn().mockResolvedValue({ items: [], next_cursor: null }), history: vi.fn().mockResolvedValue({ involvement: "historical", instance_id: A, observed_at: observedAt, items: [], next_cursor: null }), evidence: vi.fn().mockResolvedValue({ items: [], next_cursor: null }) } as unknown as TopologyApi;
}
function inventory(capacity?: AccountInventoryApi["capacity"]): AccountInventoryApi {
  const fallback = vi.fn().mockResolvedValue({ status: "ready", enabled: true, eligibleNodeCount: 2, effectiveCapacity: 10, concurrency: 2, requestTimeoutMs: 1000, finalizeTimeoutMs: 1000, lifecycleTimeoutMs: 1000, claimTimeoutMs: 1000, dispatchMarginMs: 100, pollStartGraceMs: 100, evaluatedSlot: observedAt, evaluatedAt: observedAt });
  return { query: vi.fn(), capacity: capacity ?? fallback };
}
function renderView(api: TopologyApi, assetApi: AssetApi, initialInstanceId = A, onUnauthorized = vi.fn(), inventoryApi?: AccountInventoryApi) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { client, onUnauthorized, ...render(<FrontendFoundationProvider><QueryClientProvider client={client}><MonitoringView api={api} assetApi={assetApi} inventoryApi={inventoryApi} initialInstanceId={initialInstanceId} csrfToken="csrf" onUnauthorized={onUnauthorized} /></QueryClientProvider></FrontendFoundationProvider>) };
}

describe("MonitoringView", () => {
  it("keeps monitoring read-only and does not mount account or probe surfaces", async () => {
    const api = topology();
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    renderView(api, assetApi);
    expect(await screen.findByTestId("monitoring-view")).toBeInTheDocument();
    expect(screen.queryByTestId("account-workspace")).not.toBeInTheDocument();
    expect(api.providers).toHaveBeenCalledWith(A, expect.any(AbortSignal));
  });
  it("isolates provider failure from binding diagnostics", async () => {
    const api = topology(); api.providers = vi.fn().mockRejectedValue(new TopologyApiError(503));
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    renderView(api, assetApi);
    expect(await screen.findByText("unbound")).toBeInTheDocument();
    expect(screen.getByText("Unavailable")).toBeInTheDocument();
  });
  it("expires the authenticated session for provider 401", async () => {
    const api = topology(); api.providers = vi.fn().mockRejectedValue(new TopologyApiError(401));
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    const { client, onUnauthorized } = renderView(api, assetApi);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    expect(client.getQueryCache().getAll()).toHaveLength(0);
  });
  it("keeps a non-401 diagnostic failure in the page", async () => {
    const api = topology(); api.binding = vi.fn().mockRejectedValue(new TopologyApiError(503));
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    const { onUnauthorized } = renderView(api, assetApi);
    await screen.findByText("Unavailable");
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it("keeps selected context without inventing account data", async () => {
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [], nextCursor: null }), node: vi.fn().mockRejectedValue(new AssetApiError(404)) } as unknown as AssetApi;
    renderView(topology(), assetApi, B);
    expect(await screen.findByText(B)).toBeInTheDocument();
    expect(screen.queryByTestId("account-workspace")).not.toBeInTheDocument();
  });
  it("expires the authenticated session when the node list returns 401", async () => {
    const assetApi = { nodes: vi.fn().mockRejectedValue(new AssetApiError(401)), node: vi.fn() } as unknown as AssetApi;
    const { client, onUnauthorized } = renderView(topology(), assetApi);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    expect(client.getQueryCache().getAll()).toHaveLength(0);
  });
  it("expires the authenticated session when the selected node returns 401", async () => {
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockRejectedValue(new AssetApiError(401)) } as unknown as AssetApi;
    const { onUnauthorized } = renderView(topology(), assetApi);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });
  it("renders provider states and duplicate diagnostics without deriving global totals", async () => {
    const api = topology();
    api.providers = vi.fn().mockResolvedValue({ instance_id: A, observed_at: observedAt, providers: [
      { provider: "fresh-provider", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: observedAt, snapshot_freshness: "fresh", health_scheduled_at: observedAt, health_degraded: false, health_reason: null },
      { provider: "stale-provider", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: observedAt, snapshot_freshness: "stale", health_scheduled_at: observedAt, health_degraded: true, health_reason: "transport_failed" },
      { provider: "unknown-provider", monitoring_status: "active", state: null, current_scheduled_at: null, last_complete_at: null, snapshot_freshness: "unknown", health_scheduled_at: null, health_degraded: null, health_reason: null },
    ] });
    api.currentDuplicates = vi.fn().mockResolvedValue({ items: [{ occurrence_id: "occ-1", account_key: "account-1", status: "ACTIVE", severity: "high", evidence_state: "fresh", last_fully_verified_at: observedAt, last_seen_at: observedAt, resolved_at: null, affected_nodes: [] }], next_cursor: "current-2" });
    api.history = vi.fn().mockResolvedValue({ involvement: "historical", instance_id: A, observed_at: observedAt, items: [{ occurrence_id: "occ-2", account_key: "account-2", status: "RESOLVED", severity: "low", evidence_state: "fresh", last_fully_verified_at: observedAt, last_seen_at: observedAt, resolved_at: observedAt, affected_nodes: [] }], next_cursor: "history-2" });
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    renderView(api, assetApi);
    expect(await screen.findByText("fresh-provider")).toBeInTheDocument();
    expect(screen.getByText("transport_failed")).toBeInTheDocument();
    expect(screen.getByText("account-1")).toBeInTheDocument();
    expect(screen.getByText("account-2")).toBeInTheDocument();
    expect(screen.getAllByText("The current set is empty").length).toBeGreaterThan(0);
    fireEvent.click(screen.getByTestId("monitoring-current-next"));
    fireEvent.click(screen.getByTestId("monitoring-history-next"));
    expect(api.currentDuplicates).toHaveBeenCalledWith(A, "current-2", expect.any(AbortSignal));
    expect(api.history).toHaveBeenCalledWith(A, undefined, "history-2", expect.any(AbortSignal));
  });
  it("uses explicit capacity refresh and keeps capacity failure isolated", async () => {
    const api = topology();
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    const capacity = vi.fn().mockResolvedValue({ status: "disabled", enabled: false, eligibleNodeCount: 0, effectiveCapacity: 0, concurrency: 0, requestTimeoutMs: 1000, finalizeTimeoutMs: 1000, lifecycleTimeoutMs: 1000, claimTimeoutMs: 1000, dispatchMarginMs: 100, pollStartGraceMs: 100, evaluatedSlot: observedAt, evaluatedAt: observedAt } as never);
    renderView(api, assetApi, A, vi.fn(), inventory(capacity));
    expect(capacity).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("monitoring-capacity-refresh"));
    await waitFor(() => expect(capacity).toHaveBeenCalledTimes(1));
    expect(screen.getByText(/已禁用|Disabled/)).toBeInTheDocument();
  });
  it("expands evidence explicitly and expires the session for evidence 401", async () => {
    const api = topology();
    api.currentDuplicates = vi.fn().mockResolvedValue({ items: [{ occurrence_id: "occ-1", account_key: "account-1", status: "ACTIVE", severity: "high", evidence_state: "fresh", last_fully_verified_at: observedAt, last_seen_at: observedAt, resolved_at: null, affected_nodes: [] }], next_cursor: null });
    api.evidence = vi.fn().mockRejectedValue(new TopologyApiError(401));
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    const onUnauthorized = vi.fn();
    renderView(api, assetApi, A, onUnauthorized);
    await screen.findByText("account-1");
    fireEvent.click(screen.getByTestId("monitoring-evidence-expand-occ-1"));
    await waitFor(() => expect(api.evidence).toHaveBeenCalledWith("occ-1", undefined, expect.any(AbortSignal)));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });
  async function expectUnauthorized(field: "providers" | "binding" | "currentDuplicates" | "history") {
    const api = topology();
    api[field] = vi.fn().mockRejectedValue(new TopologyApiError(401)) as never;
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    const { onUnauthorized } = renderView(api, assetApi);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  }
  it("expires the session for providers 401", () => expectUnauthorized("providers"));
  it("expires the session for binding 401", () => expectUnauthorized("binding"));
  it("expires the session for current duplicates 401", () => expectUnauthorized("currentDuplicates"));
  it("expires the session for history 401", () => expectUnauthorized("history"));
  it("expires the session for capacity 401", async () => {
    const api = topology();
    const assetApi = { nodes: vi.fn().mockResolvedValue({ items: [node(A, "A")], nextCursor: null }), node: vi.fn().mockResolvedValue(node(A, "A")) } as unknown as AssetApi;
    const onUnauthorized = vi.fn();
    const capacity = vi.fn().mockRejectedValue(new AccountInventoryApiError(401, { code: "unauthorized", message: "unauthorized", request_id: "fixture" }));
    renderView(api, assetApi, A, onUnauthorized, inventory(capacity));
    fireEvent.click(screen.getByTestId("monitoring-capacity-refresh"));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });
});

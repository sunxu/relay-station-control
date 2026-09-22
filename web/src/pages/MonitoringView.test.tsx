import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MonitoringView } from "./MonitoringView";
import type { AssetApi, NodeAsset } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import type { TopologyApi } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
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
function renderView(api: TopologyApi, assetApi: AssetApi, initialInstanceId = A, onUnauthorized = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { client, onUnauthorized, ...render(<FrontendFoundationProvider><QueryClientProvider client={client}><MonitoringView api={api} assetApi={assetApi} initialInstanceId={initialInstanceId} csrfToken="csrf" onUnauthorized={onUnauthorized} /></QueryClientProvider></FrontendFoundationProvider>) };
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
});

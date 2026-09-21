import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { AssetApi, NodeAsset } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";
import { NodeManagementView } from "./NodeManagementView";

const node: NodeAsset = { instanceId: "00000000-0000-4000-8000-000000000101", displayName: "Primary", nodeType: "cliproxyapi", driverContractVersion: "v1", managementEndpoint: "http://node.invalid:8317", secretConfigured: true, capabilities: ["management_health_read"], monitoringActive: true, monitoringEffectiveFrom: "2026-08-25T10:00:00Z", monitoringEffectiveTo: null, lifecycleStatus: "active", revision: "1", retiredAt: null, retiredBy: null, retireReason: null };
function makeApi(): AssetApi {
  return { environment: vi.fn(), gateway: vi.fn(), nodes: vi.fn().mockResolvedValue({ items: [node], nextCursor: null }), node: vi.fn().mockResolvedValue(node), nodeDetail: vi.fn().mockResolvedValue({ asset: node, predecessor: null, successor: null }), drivers: vi.fn().mockResolvedValue([{ nodeType: "cliproxyapi", driverContractVersion: "v1", displayName: "CLIProxyAPI", status: "active", capabilities: ["management_health_read"] }]), currentProviderPolicy: vi.fn(), registerNode: vi.fn(), editNode: vi.fn(), replaceNode: vi.fn(), retireNode: vi.fn(), health: vi.fn().mockResolvedValue({ result: "success", reachable: true, reason: "none", latencyMs: 1 }), connectionTest: vi.fn(), monitoringEnable: vi.fn(), monitoringDisable: vi.fn() };
}
function renderView(api: AssetApi, onUnauthorized = vi.fn()) {
  return render(<FrontendFoundationProvider initialLocale="zh-CN"><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><NodeManagementView api={api} csrfToken="csrf" onUnauthorized={onUnauthorized} /></QueryClientProvider></FrontendFoundationProvider>);
}

describe("NodeManagementView ownership", () => {
  it("renders Node controls without auxiliary cards or automatic probes", async () => {
    const api = makeApi();
    renderView(api);
    expect(await screen.findByTestId("nodes-registry")).toBeInTheDocument();
    expect(await screen.findByTestId(`node-details-${node.instanceId}`)).toBeInTheDocument();
    expect(screen.queryByTestId("gateway-card")).not.toBeInTheDocument();
    expect(api.health).not.toHaveBeenCalled();
    expect(api.connectionTest).not.toHaveBeenCalled();
  });

  it("routes node-list 401 to the shared authenticated-session boundary", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.nodes).mockRejectedValue(new AssetApiError(401));
    renderView(api, onUnauthorized);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("routes driver 401 to the shared authenticated-session boundary", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.drivers!).mockRejectedValue(new AssetApiError(401));
    renderView(api, onUnauthorized);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("routes detail 401 to the shared authenticated-session boundary", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.nodeDetail!).mockRejectedValue(new AssetApiError(401));
    renderView(api, onUnauthorized);
    fireEvent.click(await screen.findByTestId(`node-details-${node.instanceId}`));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("routes lifecycle mutation 401 to the shared authenticated-session boundary", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.registerNode!).mockRejectedValue(new AssetApiError(401));
    renderView(api, onUnauthorized);
    const register = await screen.findByTestId("node-register");
    await waitFor(() => expect(register).not.toBeDisabled());
    fireEvent.click(register);
    fireEvent.change(screen.getByTestId("node-form-display-name"), { target: { value: "New node" } });
    fireEvent.change(screen.getByTestId("node-form-management-endpoint"), { target: { value: "http://node:8317" } });
    fireEvent.click(screen.getByTestId("node-form-submit"));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("routes explicit health 401 to the shared authenticated-session boundary", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.health!).mockRejectedValue(new AssetApiError(401));
    renderView(api, onUnauthorized);
    fireEvent.click(await screen.findByTestId(`node-details-${node.instanceId}`));
    fireEvent.click(await screen.findByTestId("node-health-button"));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("routes monitoring command 401 to the shared authenticated-session boundary", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.monitoringEnable!).mockRejectedValue(new AssetApiError(401));
    renderView(api, onUnauthorized);
    fireEvent.click(await screen.findByTestId(`node-details-${node.instanceId}`));
    fireEvent.click(await screen.findByTestId("node-monitoring-enable-button"));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("preserves stale-detail protection when switching Nodes", async () => {
    const api = makeApi();
    const second = { ...node, instanceId: "00000000-0000-4000-8000-000000000102", displayName: "Secondary" };
    vi.mocked(api.nodes).mockResolvedValue({ items: [node, second], nextCursor: null });
    let resolveFirst!: (value: { asset: NodeAsset; predecessor: null; successor: null }) => void;
    let resolveSecond!: (value: { asset: NodeAsset; predecessor: null; successor: null }) => void;
    vi.mocked(api.nodeDetail!).mockImplementation((id) => new Promise((resolve) => { if (id === node.instanceId) resolveFirst = resolve; else resolveSecond = resolve; }));
    renderView(api);
    await screen.findByText("Secondary");
    fireEvent.click(screen.getByTestId(`node-details-${node.instanceId}`));
    fireEvent.click(screen.getByTestId(`node-details-${second.instanceId}`));
    await waitFor(() => expect(api.nodeDetail).toHaveBeenCalledTimes(2));
    resolveSecond({ asset: second, predecessor: null, successor: null });
    expect(await screen.findByTestId("node-management-operations")).toBeInTheDocument();
    resolveFirst({ asset: node, predecessor: null, successor: null });
    await waitFor(() => expect(screen.getByTestId("node-management-operations")).toBeInTheDocument());
    expect(screen.getByText("Secondary")).toBeInTheDocument();
  });
});

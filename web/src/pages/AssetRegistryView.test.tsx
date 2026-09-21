import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { AssetApiError, type AssetApi, type DriverAsset } from "../api/asset-types";
import type { GatewayAdminApi } from "../api/gateway-api";
import type { GatewayAsset, GatewayAssetDetailResponse } from "../api/generated/control";
import { AssetRegistryView, buildCredentialPatch } from "./AssetRegistryView";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";

const driver: DriverAsset = { nodeType: "cliproxyapi", driverContractVersion: "v1", displayName: "CLIProxyAPI", status: "active", capabilities: ["management_health_read"] };
const gateway: GatewayAsset = {
  instance_id: "00000000-0000-4000-8000-000000000001",
  lifecycle_status: "active",
  revision: "4",
  display_name: "Primary Gateway",
  management_endpoint: "http://gateway.invalid:8317",
  secret_configured: true,
  created_at: "2026-08-25T08:00:00Z",
  updated_at: "2026-08-25T09:00:00Z",
  retired_at: null,
  retired_by: null,
  retire_reason: null,
};
function api(): AssetApi {
  return {
    environment: vi.fn().mockResolvedValue({ environmentId: "production", environmentType: "production", displayName: "Relay Station" }),
    gateway: vi.fn().mockResolvedValue({ status: "not_configured", gateway: null }),
    nodes: vi.fn(),
    node: vi.fn(),
    drivers: vi.fn().mockResolvedValue([driver]),
    currentProviderPolicy: vi.fn().mockResolvedValue({ status: "not_configured", policy: null }),
  };
}
function gatewayApi(): GatewayAdminApi {
  return {
    list: vi.fn().mockResolvedValue({ items: [gateway], next_cursor: null, gateway_counts: { active: 1, retired: 0, total: 1 } }),
    detail: vi.fn().mockResolvedValue({ asset: gateway, predecessor: null, successor: null } satisfies GatewayAssetDetailResponse),
    register: vi.fn(),
    edit: vi.fn().mockResolvedValue({ asset: { ...gateway, revision: "5", display_name: "Edited Gateway" } }),
    retire: vi.fn(),
    replace: vi.fn(),
    health: vi.fn(),
    connectionTest: vi.fn(),
  };
}
function Wrapper({ children }: { children: ReactNode }) {
  return <FrontendFoundationProvider initialLocale="zh-CN"><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider></FrontendFoundationProvider>;
}

describe("AssetRegistryView ownership", () => {
  it("renders auxiliary cards without mounting Node API", async () => {
    const assetApi = api();
    render(<AssetRegistryView api={assetApi} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    expect(await screen.findByTestId("environment-card")).toBeInTheDocument();
    expect(screen.getByTestId("drivers-card")).toBeInTheDocument();
    expect(screen.queryByTestId("nodes-registry")).not.toBeInTheDocument();
    expect(assetApi.nodes).not.toHaveBeenCalled();
  });

  it("keeps Gateway/Environment/Policy ownership on the auxiliary surface", async () => {
    const assetApi = api();
    render(<AssetRegistryView api={assetApi} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await waitFor(() => expect(assetApi.drivers).toHaveBeenCalled());
    expect(screen.getByTestId("environment-card")).toBeInTheDocument();
    expect(screen.getByTestId("gateway-card")).toBeInTheDocument();
    expect(screen.getByTestId("policy-card")).toBeInTheDocument();
  });

  it("builds explicit credential tri-state without truthiness coercion", () => {
    expect(buildCredentialPatch("management_credential", "keep", "stale-secret")).toEqual({});
    expect(buildCredentialPatch("management_credential", "set", "new-secret")).toEqual({ management_credential: "new-secret" });
    expect(buildCredentialPatch("management_credential", "clear")).toEqual({ management_credential: null });
    expect(buildCredentialPatch("directory_credential", "set", "new-secret")).toEqual({ directory_credential: "new-secret" });
  });

  it.each([
    ["environment", (api: AssetApi) => vi.mocked(api.environment).mockRejectedValue(new AssetApiError(401))],
    ["gateway", (api: AssetApi) => vi.mocked(api.gateway).mockRejectedValue(new AssetApiError(401))],
    ["drivers", (api: AssetApi) => vi.mocked(api.drivers).mockRejectedValue(new AssetApiError(401))],
  ])("routes %s 401 to the authenticated-session boundary", async (_name, reject) => {
    const assetApi = api();
    const onUnauthorized = vi.fn();
    reject(assetApi);
    render(<AssetRegistryView api={assetApi} onUnauthorized={onUnauthorized} />, { wrapper: Wrapper });
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("routes provider policy 401 to the authenticated-session boundary", async () => {
    const assetApi = api();
    const onUnauthorized = vi.fn();
    vi.mocked(assetApi.currentProviderPolicy).mockRejectedValue(new AssetApiError(401));
    render(<AssetRegistryView api={assetApi} onUnauthorized={onUnauthorized} />, { wrapper: Wrapper });
    fireEvent.mouseDown(await screen.findByTestId("asset-provider-policy-scope"));
    fireEvent.click(await screen.findByText("cliproxyapi / v1"));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it("does not expire the session for a non-401 auxiliary read failure", async () => {
    const assetApi = api();
    const onUnauthorized = vi.fn();
    vi.mocked(assetApi.environment).mockRejectedValue(new AssetApiError(503));
    render(<AssetRegistryView api={assetApi} onUnauthorized={onUnauthorized} />, { wrapper: Wrapper });
    await waitFor(() => expect(assetApi.environment).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("requires a new credential when editing with the set action", async () => {
    const assetApi = api();
    const gatewayApiMock = gatewayApi();
    render(<AssetRegistryView api={assetApi} gatewayApi={gatewayApiMock} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    const card = await screen.findByTestId("gateway-management-card");
    fireEvent.click(await within(card).findByTestId(`gateway-edit-${gateway.instance_id}`));
    fireEvent.mouseDown(await screen.findByTestId("gateway-credential-action"));
    fireEvent.click(await screen.findByText("设置新 credential"));
    fireEvent.click(screen.getByTestId("gateway-form-submit"));
    expect(await screen.findByText("请输入新的 credential")).toBeInTheDocument();
    expect(gatewayApiMock.edit).not.toHaveBeenCalled();
    fireEvent.change(screen.getByTestId("gateway-credential-input"), { target: { value: "credential-canary" } });
    fireEvent.click(screen.getByTestId("gateway-form-submit"));
    await waitFor(() => expect(gatewayApiMock.edit).toHaveBeenCalledWith(gateway.instance_id, expect.objectContaining({ directory_credential: "credential-canary" }), "csrf"));
    expect(document.body.textContent).not.toContain("credential-canary");
  });
});

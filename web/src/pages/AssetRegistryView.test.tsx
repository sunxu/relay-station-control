import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { AssetApi, DriverAsset } from "../api/asset-types";
import { AssetRegistryView, buildCredentialPatch } from "./AssetRegistryView";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";

const driver: DriverAsset = { nodeType: "cliproxyapi", driverContractVersion: "v1", displayName: "CLIProxyAPI", status: "active", capabilities: ["management_health_read"] };
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
});

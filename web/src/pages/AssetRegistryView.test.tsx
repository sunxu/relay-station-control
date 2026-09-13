import { formatDateTime } from "../time";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { AssetApi, DriverAsset, GatewayState, NodeAsset, ProviderPolicyState } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import { AssetRegistryView } from "./AssetRegistryView";

const drivers: DriverAsset[] = [{
  nodeType: "cliproxyapi",
  driverContractVersion: "v1",
  displayName: "CLIProxyAPI Driver",
  status: "active",
  capabilities: ["management_health_read", "management_account_inventory_read"],
}];

const firstNode: NodeAsset = {
  instanceId: "00000000-0000-4000-8000-000000000101",
  displayName: "Singapore Node",
  nodeType: "cliproxyapi",
  driverContractVersion: "v1",
  managementEndpoint: "https://node.invalid:8317",
  secretConfigured: true,
  capabilities: ["management_health_read"],
  monitoringActive: true,
  monitoringEffectiveFrom: "2026-08-25T10:00:00Z",
  monitoringEffectiveTo: null,
	lifecycleStatus: "active",
	revision: "1",
	retiredAt: null,
	retiredBy: null,
	retireReason: null,
};

const gateway: GatewayState = {
  status: "configured",
  gateway: {
    instanceId: "00000000-0000-4000-8000-000000000001",
    displayName: "Primary Gateway",
    managementEndpoint: "https://gateway.invalid:8443",
    secretConfigured: true,
    createdAt: "2026-08-25T08:00:00Z",
    updatedAt: "2026-08-25T09:00:00Z",
  },
};

const policy: ProviderPolicyState = {
  status: "configured",
  policy: {
    policyVersionId: "00000000-0000-4000-8000-000000000201",
    nodeType: "cliproxyapi",
    driverContractVersion: "v1",
    activeProviders: ["anthropic", "openai"],
    outOfScopeProviders: ["gemini"],
    effectiveFrom: "2026-08-25T10:00:00Z",
    effectiveTo: null,
    createdAt: "2026-08-25T09:00:00Z",
  },
};

function makeApi(): AssetApi {
  return {
    environment: vi.fn().mockResolvedValue({ environmentId: "development", environmentType: "production", displayName: "Phase 1" }),
    gateway: vi.fn().mockResolvedValue(gateway),
    nodes: vi.fn().mockResolvedValue({ items: [firstNode], nextCursor: null }),
    node: vi.fn().mockResolvedValue(firstNode),
    drivers: vi.fn().mockResolvedValue(drivers),
    currentProviderPolicy: vi.fn().mockResolvedValue(policy),
  };
}

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

describe("asset registry read-only view", () => {
  it("renders independent empty states without editing controls", async () => {
    const api = makeApi();
    vi.mocked(api.gateway).mockResolvedValue({ status: "not_configured", gateway: null });
    vi.mocked(api.nodes).mockResolvedValue({ items: [], nextCursor: null });
    vi.mocked(api.drivers).mockResolvedValue([]);
    vi.mocked(api.currentProviderPolicy).mockResolvedValue({ status: "not_configured", policy: null });

    render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByText("尚未登记 Gateway")).toBeInTheDocument();
    expect(screen.getByText("尚未登记 Driver")).toBeInTheDocument();
    expect(screen.getByText("请先登记 Driver")).toBeInTheDocument();
    expect(screen.getByText("当前过滤条件下没有 Relay Node")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /创建|编辑|删除|登记|保存|启用|禁用/ })).not.toBeInTheDocument();
  });

  it("distinguishes an unconfigured policy from a missing Driver scope", async () => {
    const api = makeApi();
    vi.mocked(api.currentProviderPolicy).mockResolvedValue({ status: "not_configured", policy: null });
    render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    expect(await screen.findByText("尚未配置当前 Provider 策略")).toBeInTheDocument();
    expect(api.currentProviderPolicy).toHaveBeenCalledWith({ nodeType: "cliproxyapi", driverContractVersion: "v1" });
  });

  it("renders normalized cards, local times and non-clickable endpoints without secret references", async () => {
    const api = makeApi();
    vi.mocked(api.gateway).mockResolvedValue({
      ...gateway,
      gateway: { ...gateway.gateway!, reader_secret_ref: "vault://CANARY-GATEWAY-SECRET" },
    } as GatewayState);
    vi.mocked(api.nodes).mockResolvedValue({
      items: [{ ...firstNode, reader_secret_ref: "vault://CANARY-NODE-SECRET" } as NodeAsset],
      nextCursor: null,
    });

    render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByText("Primary Gateway")).toBeInTheDocument();
    expect(screen.getByText("Singapore Node")).toBeInTheDocument();
    expect(screen.getByText("CLIProxyAPI Driver")).toBeInTheDocument();
    expect(screen.getAllByText(formatDateTime("2026-08-25T09:00:00Z")).length).toBeGreaterThan(0);
    expect(document.body.textContent).not.toContain("CANARY-GATEWAY-SECRET");
    expect(document.body.textContent).not.toContain("CANARY-NODE-SECRET");
    expect(document.querySelector('a[href^="https://gateway.invalid"]')).toBeNull();
    expect(document.querySelector('a[href^="https://node.invalid"]')).toBeNull();
  });

  it("applies combined filters, resets the cursor and pages in both directions", async () => {
    const api = makeApi();
    vi.mocked(api.nodes).mockImplementation(async (filters) => ({
      items: [{ ...firstNode, instanceId: filters.cursor ? "00000000-0000-4000-8000-000000000102" : firstNode.instanceId }],
      nextCursor: filters.cursor ? null : "cursor-page-two",
    }));
    render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    await screen.findByText("Singapore Node");

    fireEvent.mouseDown(screen.getByLabelText("Node 类型"));
    fireEvent.click(await screen.findByText("cliproxyapi", { selector: ".ant-select-item-option-content" }));
    await waitFor(() => expect(api.nodes).toHaveBeenLastCalledWith(expect.objectContaining({ nodeType: "cliproxyapi" })));
    fireEvent.mouseDown(screen.getByLabelText("Capability"));
    fireEvent.click(await screen.findByText("management_health_read", { selector: ".ant-select-item-option-content" }));
    await waitFor(() => expect(api.nodes).toHaveBeenLastCalledWith(expect.objectContaining({ capability: "management_health_read" })));
    fireEvent.mouseDown(screen.getByLabelText("监控状态"));
    fireEvent.click(await screen.findByText("监控已激活", { selector: ".ant-select-item-option-content" }));

    await waitFor(() => expect(api.nodes).toHaveBeenLastCalledWith(expect.objectContaining({
      nodeType: "cliproxyapi",
      capability: "management_health_read",
      monitoringActive: true,
      cursor: undefined,
      limit: 50,
    })));

    await waitFor(() => expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.nodes).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: "cursor-page-two" })));
    expect(screen.getByRole("button", { name: "上一页" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "上一页" }));
    await waitFor(() => expect(api.nodes).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: undefined })));
  });

  it("shows a bounded resource failure, removes internal detail and retries only on click", async () => {
    const api = makeApi();
    vi.mocked(api.gateway).mockRejectedValueOnce(new Error("postgres://user:password@db/vault/CANARY"));
    render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    const card = screen.getByTestId("gateway-card");
    expect(await within(card).findByText("读取失败")).toBeInTheDocument();
    expect(card.textContent).not.toContain("postgres://");
    expect(card.textContent).not.toContain("CANARY");
    expect(api.gateway).toHaveBeenCalledTimes(1);

    vi.mocked(api.gateway).mockResolvedValue(gateway);
    fireEvent.click(within(card).getByRole("button", { name: /重\s*试/ }));
    expect(await within(card).findByText("Primary Gateway")).toBeInTheDocument();
    expect(api.gateway).toHaveBeenCalledTimes(2);
  });

  it("does not present a previously successful value after a 503 refresh", async () => {
    const api = makeApi();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <AssetRegistryView api={api} onUnauthorized={vi.fn()} />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Primary Gateway")).toBeInTheDocument();

    vi.mocked(api.gateway).mockRejectedValue(new AssetApiError(503));
    await client.refetchQueries({ queryKey: ["asset-registry", "gateway"] });
    const card = screen.getByTestId("gateway-card");
    expect(await within(card).findByText("读取失败")).toBeInTheDocument();
    expect(within(card).queryByText("Primary Gateway")).not.toBeInTheDocument();
  });

  it("hands a 401 to the existing authentication flow", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    vi.mocked(api.drivers).mockRejectedValue(new AssetApiError(401));
    render(<AssetRegistryView api={api} onUnauthorized={onUnauthorized} />, { wrapper: Wrapper });
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
  });

	it("owns Node lifecycle controls while Stage 3 operations remain absent", async () => {
		const api = makeApi();
		api.registerNode = vi.fn().mockResolvedValue(undefined);
		api.editNode = vi.fn().mockResolvedValue(undefined);
		api.retireNode = vi.fn().mockResolvedValue(undefined);
		api.replaceNode = vi.fn().mockResolvedValue(undefined);
		api.nodeDetail = vi.fn().mockResolvedValue({ asset: firstNode, predecessor: null, successor: null });
		render(<AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

		expect(await screen.findByRole("button", { name: "登记 Node" })).toBeInTheDocument();
		await screen.findByText("Singapore Node");
		expect(screen.getByRole("button", { name: /编\s*辑/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Replace" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retire" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Health|Connection Test|Monitoring|启用监控|禁用监控/ })).not.toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: /详\s*情/ }));
		expect(await screen.findByText("Node 详情")).toBeInTheDocument();
		expect(api.nodeDetail).toHaveBeenCalledWith(firstNode.instanceId);
	}, 15_000);
});

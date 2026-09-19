import { formatDateTime } from "../foundation/format";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { AssetApi, DriverAsset, GatewayState, NodeAsset, NodeMonitoringResult, NodeProbeResult, ProviderPolicyState } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import { AssetRegistryView, buildCredentialPatch } from "./AssetRegistryView";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";
import { LocaleSwitcher } from "../foundation/LocaleSwitcher";

const drivers: DriverAsset[] = [{
  nodeType: "cliproxyapi",
  driverContractVersion: "v1",
  displayName: "CLIProxyAPI Driver",
  status: "active",
  capabilities: ["management_health_read", "management_account_inventory_read"],
}];

const catalogDrivers: DriverAsset[] = [
  ...drivers,
  { nodeType: "custom-node", driverContractVersion: "contract-b", displayName: "Custom B", status: "active", capabilities: ["cap-b"] },
  { nodeType: "custom-node", driverContractVersion: "contract-a", displayName: "Custom A", status: "active", capabilities: ["cap-a", "cap-shared"] },
  { nodeType: "retired-node", driverContractVersion: "retired", displayName: "Retired", status: "retired", capabilities: ["cap-retired"] },
];

const firstNode: NodeAsset = {
  instanceId: "00000000-0000-4000-8000-000000000101",
  displayName: "Singapore Node",
  nodeType: "cliproxyapi",
  driverContractVersion: "v1",
  managementEndpoint: "http://node.invalid:8317",
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

const healthyProbe: NodeProbeResult = { result: "success", reachable: true, reason: "none", latencyMs: 12 };
const monitoringEnabled: NodeMonitoringResult = {
  result: "enabled",
  instanceId: firstNode.instanceId,
  lifecycleStatus: "active",
  revision: firstNode.revision,
  boundary: "2026-08-25T10:01:00Z",
  monitoringActive: true,
  monitoringActivationId: "00000000-0000-4000-8000-000000000301",
  effectiveFrom: "2026-08-25T10:01:00Z",
  effectiveTo: null,
  closedMonitoringCount: 0,
  cancelledFutureMonitoringCount: 0,
};

function makeApi(): AssetApi {
  return {
    environment: vi.fn().mockResolvedValue({ environmentId: "development", environmentType: "production", displayName: "Phase 1" }),
    gateway: vi.fn().mockResolvedValue(gateway),
    nodes: vi.fn().mockResolvedValue({ items: [firstNode], nextCursor: null }),
    node: vi.fn().mockResolvedValue(firstNode),
    nodeDetail: vi.fn().mockResolvedValue({ asset: firstNode, predecessor: null, successor: null }),
    drivers: vi.fn().mockResolvedValue(drivers),
    currentProviderPolicy: vi.fn().mockResolvedValue(policy),
    health: vi.fn().mockResolvedValue(healthyProbe),
    connectionTest: vi.fn().mockResolvedValue(healthyProbe),
    monitoringEnable: vi.fn().mockResolvedValue(monitoringEnabled),
    monitoringDisable: vi.fn().mockResolvedValue({ ...monitoringEnabled, result: "disabled", monitoringActive: false, monitoringActivationId: null, effectiveFrom: null }),
  };
}

function Wrapper({ children }: { children: ReactNode }) {
  return <FrontendFoundationProvider initialLocale="zh-CN"><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider></FrontendFoundationProvider>;
}

describe("asset registry read-only view", () => {
	it("builds explicit credential tri-state without truthiness coercion", () => {
		expect(buildCredentialPatch("management_credential", "keep", "stale-secret")).toEqual({});
		expect(buildCredentialPatch("management_credential", "set", "new-secret")).toEqual({ management_credential: "new-secret" });
		expect(buildCredentialPatch("management_credential", "set", "")).toEqual({ management_credential: "" });
		expect(buildCredentialPatch("management_credential", "clear")).toEqual({ management_credential: null });
		expect(buildCredentialPatch("directory_credential", "keep", "stale-secret")).toEqual({});
		expect(buildCredentialPatch("directory_credential", "set", "new-secret")).toEqual({ directory_credential: "new-secret" });
		expect(buildCredentialPatch("directory_credential", "set", "")).toEqual({ directory_credential: "" });
		expect(buildCredentialPatch("directory_credential", "clear")).toEqual({ directory_credential: null });
	});

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

	it("renders normalized cards, local times and non-clickable endpoints without credentials", async () => {
    const api = makeApi();
    vi.mocked(api.gateway).mockResolvedValue({
      ...gateway,
      gateway: { ...gateway.gateway! },
    } as GatewayState);
    vi.mocked(api.nodes).mockResolvedValue({
      items: [{ ...firstNode }],
      nextCursor: null,
    });

    render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByText("Primary Gateway")).toBeInTheDocument();
    expect(screen.getByText("Singapore Node")).toBeInTheDocument();
    expect(screen.getByText("CLIProxyAPI Driver")).toBeInTheDocument();
    expect(screen.getAllByText(formatDateTime("2026-08-25T09:00:00Z", "zh-CN")).length).toBeGreaterThan(0);
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
      <FrontendFoundationProvider initialLocale="zh-CN">
        <QueryClientProvider client={client}>
          <AssetRegistryView api={api} onUnauthorized={vi.fn()} />
        </QueryClientProvider>
      </FrontendFoundationProvider>,
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

	it("shows explicit Stage 3 operations only inside active Node detail", async () => {
		const api = makeApi();
		api.registerNode = vi.fn().mockResolvedValue(undefined);
		api.editNode = vi.fn().mockResolvedValue(undefined);
		api.retireNode = vi.fn().mockResolvedValue(undefined);
		api.replaceNode = vi.fn().mockResolvedValue(undefined);
		render(<AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

		expect(await screen.findByRole("button", { name: "登记 Node" })).toBeInTheDocument();
		await screen.findByText("Singapore Node");
		expect(screen.getByRole("button", { name: /编\s*辑/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Replace" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retire" })).toBeInTheDocument();
		expect(screen.queryByTestId("node-management-operations")).not.toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: /详.{0,2}情/ }));
		expect(await screen.findByText("Node 详情")).toBeInTheDocument();
		expect(screen.getByTestId("node-health-button")).toBeInTheDocument();
		expect(screen.getByTestId("node-connection-test-button")).toBeInTheDocument();
		expect(screen.getByTestId("node-monitoring-enable-button")).toBeInTheDocument();
		expect(screen.getByTestId("node-monitoring-disable-button")).toBeInTheDocument();
		expect(api.nodeDetail).toHaveBeenCalledWith(firstNode.instanceId);
	}, 15_000);

  it("updates mounted Node columns and retire copy when locale changes", async () => {
    const api = makeApi();
    api.retireNode = vi.fn().mockResolvedValue(undefined);
    render(<><LocaleSwitcher /><AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} /></>, { wrapper: Wrapper });

    expect((await screen.findAllByRole("columnheader", { name: "Node 类型" })).length).toBeGreaterThan(0);
    fireEvent.change(screen.getByTestId("locale-selector"), { target: { value: "en" } });
    expect((await screen.findAllByRole("columnheader", { name: "Node type" })).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "Retire" }));
    expect(await screen.findByText("Retire this Node?")).toBeInTheDocument();
    expect(screen.queryByText("Retire this Gateway?")).not.toBeInTheDocument();
  });

	it("accepts internal Docker HTTP endpoints and rejects HTTPS for Node registration", async () => {
		const api = makeApi();
		api.registerNode = vi.fn().mockResolvedValue(undefined);
		vi.mocked(api.nodes).mockResolvedValue({ items: [], nextCursor: null });
		render(<AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

		await waitFor(() => expect(screen.getByRole("button", { name: "登记 Node" })).toBeEnabled());
		fireEvent.click(screen.getByRole("button", { name: "登记 Node" }));
		fireEvent.change(screen.getByLabelText("显示名称"), { target: { value: "Docker Node" } });
		fireEvent.change(screen.getByLabelText("Management endpoint"), { target: { value: "http://node:8317" } });
		fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));
		await waitFor(() => expect(api.registerNode).toHaveBeenCalledWith(expect.objectContaining({ management_endpoint: "http://node:8317", node_type: "cliproxyapi", driver_contract_version: "v1", capabilities: drivers[0]!.capabilities }), "csrf-proof"));

		vi.mocked(api.registerNode).mockClear();
		await waitFor(() => expect(screen.getByRole("button", { name: "登记 Node" })).toBeEnabled());
		fireEvent.click(screen.getByRole("button", { name: "登记 Node" }));
		fireEvent.change(screen.getByLabelText("显示名称"), { target: { value: "HTTPS Node" } });
		fireEvent.change(screen.getByLabelText("Management endpoint"), { target: { value: "https://node:8317" } });
		fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));
		expect(await screen.findByText("仅支持 http://")).toBeInTheDocument();
		expect(api.registerNode).not.toHaveBeenCalled();
	});

	it("uses Driver Catalog values and keeps the generated UUID stable across a failed submit", async () => {
		const api = makeApi();
		api.registerNode = vi.fn().mockRejectedValue(new Error("retry"));
		vi.mocked(api.drivers).mockResolvedValue(catalogDrivers);
		vi.mocked(api.nodes).mockResolvedValue({ items: [], nextCursor: null });
		render(<AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

		await waitFor(() => expect(screen.getByRole("button", { name: "登记 Node" })).toBeEnabled());
		fireEvent.click(screen.getByRole("button", { name: "登记 Node" }));
		const instanceId = screen.getByTestId("node-register-instance-id") as HTMLInputElement;
		expect(instanceId.value).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i);
		expect(instanceId).toHaveAttribute("readonly");

		fireEvent.mouseDown(screen.getByTestId("node-register-node-type"));
		fireEvent.click(await screen.findByText("custom-node", { selector: ".ant-select-item-option-content" }));
		fireEvent.mouseDown(screen.getByTestId("node-register-driver-contract"));
		expect(await screen.findByText("contract-a", { selector: ".ant-select-item-option-content" })).toBeInTheDocument();
		expect(screen.getByText("contract-b", { selector: ".ant-select-item-option-content" })).toBeInTheDocument();
		fireEvent.click(screen.getByText("contract-a", { selector: ".ant-select-item-option-content" }));
		fireEvent.mouseDown(screen.getByTestId("node-register-capabilities"));
		expect(await screen.findByText("cap-a", { selector: ".ant-select-item-option-content" })).toBeInTheDocument();
		expect(screen.queryByText("cap-b", { selector: ".ant-select-item-option-content" })).not.toBeInTheDocument();
		fireEvent.keyDown(screen.getByTestId("node-register-capabilities"), { key: "Escape" });
		fireEvent.change(screen.getByLabelText("显示名称"), { target: { value: "Catalog Node" } });
		fireEvent.change(screen.getByLabelText("Management endpoint"), { target: { value: "http://node:8317" } });
		fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));
		await waitFor(() => expect(api.registerNode).toHaveBeenCalledWith(expect.objectContaining({ new_instance_id: instanceId.value, node_type: "custom-node", driver_contract_version: "contract-a", capabilities: ["cap-a", "cap-shared"] }), "csrf-proof"));
		expect(screen.getByTestId("node-register-instance-id")).toHaveValue(instanceId.value);
	});

	it("resets contract and capabilities when Node Type changes", async () => {
		const api = makeApi();
		api.registerNode = vi.fn().mockResolvedValue(undefined);
		vi.mocked(api.drivers).mockResolvedValue(catalogDrivers);
		vi.mocked(api.nodes).mockResolvedValue({ items: [], nextCursor: null });
		render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
		await waitFor(() => expect(screen.getByRole("button", { name: "登记 Node" })).toBeEnabled());
		fireEvent.click(screen.getByRole("button", { name: "登记 Node" }));
		fireEvent.mouseDown(screen.getByTestId("node-register-node-type"));
		fireEvent.click(await screen.findByText("custom-node", { selector: ".ant-select-item-option-content" }));
		fireEvent.mouseDown(screen.getByTestId("node-register-driver-contract"));
		fireEvent.click(await screen.findByText("contract-b", { selector: ".ant-select-item-option-content" }));
		fireEvent.mouseDown(screen.getByTestId("node-register-node-type"));
		fireEvent.click(await screen.findByText("cliproxyapi", { selector: ".ant-select-item-option-content" }));
		expect(screen.getByTestId("node-register-driver-contract")).toHaveTextContent("v1");
		expect(screen.getByTestId("node-register-capabilities")).toHaveTextContent("management_health_read");
		expect(screen.getByTestId("node-register-capabilities")).not.toHaveTextContent("cap-b");
	});

	it("runs probes only after explicit clicks and confirms immediate disable", async () => {
		const api = makeApi();
		render(<AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
		await screen.findByText("Singapore Node");
		expect(api.health).not.toHaveBeenCalled();
		expect(api.connectionTest).not.toHaveBeenCalled();
		expect(api.monitoringEnable).not.toHaveBeenCalled();
		expect(api.monitoringDisable).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: /详.{0,2}情/ }));
		const detail = await screen.findByTestId("node-management-operations");
		fireEvent.click(within(detail).getByTestId("node-health-button"));
		expect(await within(detail).findByTestId("node-health-result")).toBeInTheDocument();
		fireEvent.click(within(detail).getByTestId("node-connection-test-button"));
		expect(await within(detail).findByTestId("node-connection-test-result")).toBeInTheDocument();
		fireEvent.click(within(detail).getByTestId("node-monitoring-disable-button"));
		expect(await screen.findByText("这会立即关闭当前监控，并取消已有的未来监控预约。"));
		const confirmButtons = screen.getAllByRole("button", { name: /停.{0,2}用/ });
		fireEvent.click(confirmButtons[confirmButtons.length - 1]!);
		expect(await within(detail).findByTestId("node-monitoring-result")).toHaveTextContent("disabled");
		expect(api.health).toHaveBeenCalledTimes(1);
		expect(api.connectionTest).toHaveBeenCalledWith(firstNode.instanceId, "csrf-proof");
		expect(api.monitoringDisable).toHaveBeenCalledWith(firstNode.instanceId, expect.any(String), "csrf-proof");
	});

	it("reuses the same monitoring command UUID after an unknown failure", async () => {
		const api = makeApi();
		vi.mocked(api.monitoringEnable!).mockRejectedValueOnce(new AssetApiError(503)).mockResolvedValue(monitoringEnabled);
		render(<AssetRegistryView api={api} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
		await screen.findByText("Singapore Node");
		fireEvent.click(screen.getByRole("button", { name: /详.{0,2}情/ }));
		const detail = await screen.findByTestId("node-management-operations");
		fireEvent.click(within(detail).getByTestId("node-monitoring-enable-button"));
		await waitFor(() => expect(api.monitoringEnable).toHaveBeenCalledTimes(1));
		fireEvent.click(within(detail).getByTestId("node-monitoring-enable-button"));
		await waitFor(() => expect(api.monitoringEnable).toHaveBeenCalledTimes(2));
		expect(api.monitoringEnable).toHaveBeenNthCalledWith(2, firstNode.instanceId, (api.monitoringEnable as ReturnType<typeof vi.fn>).mock.calls[0]![1], "csrf-proof");
	});

	it("ignores late detail responses after switching Nodes", async () => {
		const secondNode = { ...firstNode, instanceId: "00000000-0000-4000-8000-000000000102", displayName: "Tokyo Node" };
		const api = makeApi();
		vi.mocked(api.nodes).mockResolvedValue({ items: [firstNode, secondNode], nextCursor: null });
		let resolveFirst!: (value: { asset: NodeAsset; predecessor: null; successor: null }) => void;
		let resolveSecond!: (value: { asset: NodeAsset; predecessor: null; successor: null }) => void;
		vi.mocked(api.nodeDetail!).mockImplementation((id) => new Promise((resolve) => {
			if (id === firstNode.instanceId) resolveFirst = resolve;
			else resolveSecond = resolve;
		}));
		render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
		await screen.findByText("Tokyo Node");
		const detailButtons = screen.getAllByRole("button", { name: /详.{0,2}情/ });
		fireEvent.click(detailButtons[0]!);
		fireEvent.click(detailButtons[1]!);
		resolveSecond({ asset: secondNode, predecessor: null, successor: null });
		expect(await screen.findByText("Tokyo Node")).toBeInTheDocument();
		resolveFirst({ asset: firstNode, predecessor: null, successor: null });
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.getByText("Tokyo Node")).toBeInTheDocument();
		expect(screen.queryByText("Singapore Node", { selector: ".ant-modal-title" })).not.toBeInTheDocument();
	});

	it("does not expose Stage 3 controls for retired Nodes", async () => {
		const retiredNode = { ...firstNode, lifecycleStatus: "retired" as const, monitoringActive: false, retiredAt: "2026-08-25T11:00:00Z", retiredBy: "admin", retireReason: "administrator_retire" as const };
		const api = makeApi();
		vi.mocked(api.nodes).mockResolvedValue({ items: [retiredNode], nextCursor: null });
		vi.mocked(api.nodeDetail!).mockResolvedValue({ asset: retiredNode, predecessor: null, successor: null });
		render(<AssetRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
		await screen.findByText("Singapore Node");
		fireEvent.click(screen.getByRole("button", { name: /详.{0,2}情/ }));
		expect(await screen.findByText("Node 详情")).toBeInTheDocument();
		expect(screen.queryByTestId("node-management-operations")).not.toBeInTheDocument();
		expect(api.health).not.toHaveBeenCalled();
		expect(api.connectionTest).not.toHaveBeenCalled();
	});
});

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { AssetApi } from "../api/asset-types";
import type { GatewayAdminApi } from "../api/gateway-api";
import type { GatewayAsset, GatewayAssetDetailResponse } from "../api/generated/control";
import { AssetRegistryView } from "./AssetRegistryView";

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

function assetApi(): AssetApi {
  return {
    environment: vi.fn().mockResolvedValue({ environmentId: "development", environmentType: "production", displayName: "Phase 6" }),
    gateway: vi.fn().mockResolvedValue({ status: "configured", gateway: null }),
    nodes: vi.fn().mockResolvedValue({ items: [], nextCursor: null }),
    node: vi.fn(),
    drivers: vi.fn().mockResolvedValue([]),
    currentProviderPolicy: vi.fn(),
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
    health: vi.fn().mockResolvedValue({ instance_id: gateway.instance_id, result: "healthy", observed_at: "2026-08-25T09:00:00Z" }),
    connectionTest: vi.fn().mockResolvedValue({ instance_id: gateway.instance_id, result: "healthy", observed_at: "2026-08-25T09:00:00Z" }),
  };
}

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

describe("Gateway management controls", () => {
  it("continues loading history with next_cursor and resets the cursor when the lifecycle filter changes", async () => {
    const api = assetApi();
    const gatewayApiMock = gatewayApi();
    const second: GatewayAsset = { ...gateway, instance_id: "00000000-0000-4000-8000-000000000002", lifecycle_status: "retired" };
    vi.mocked(gatewayApiMock.list).mockImplementation(async (lifecycle = "active", cursor) => cursor
      ? { items: [second], next_cursor: null, gateway_counts: { active: 0, retired: 1, total: 1 } }
      : { items: [gateway], next_cursor: "cursor-2", gateway_counts: { active: 1, retired: 0, total: 1 } });
    render(<AssetRegistryView api={api} gatewayApi={gatewayApiMock} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    const card = await screen.findByTestId("gateway-management-card");
    await within(card).findByText("Primary Gateway");
    fireEvent.click(within(card).getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(gatewayApiMock.list).toHaveBeenLastCalledWith("active", "cursor-2"));
    expect(await within(card).findByText("00000000-0000-4000-8000-000000000002")).toBeInTheDocument();

    fireEvent.mouseDown(within(card).getByRole("combobox", { name: "Gateway 生命周期过滤" }));
    fireEvent.click(await screen.findByText("历史 Gateway"));
    await waitFor(() => expect(gatewayApiMock.list).toHaveBeenLastCalledWith("retired", undefined));
  });

  it("supports health, connection test, replace, and retire actions", async () => {
    const api = assetApi();
    const gatewayApiMock = gatewayApi();
    vi.mocked(gatewayApiMock.register).mockResolvedValue({ asset: gateway });
    vi.mocked(gatewayApiMock.replace).mockResolvedValue({ old_asset: { ...gateway, lifecycle_status: "retired" }, new_asset: gateway });
    vi.mocked(gatewayApiMock.retire).mockResolvedValue({ asset: { ...gateway, lifecycle_status: "retired" } });
    render(<AssetRegistryView api={api} gatewayApi={gatewayApiMock} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    const card = await screen.findByTestId("gateway-management-card");
    await within(card).findByText("Primary Gateway");

    fireEvent.click(within(card).getByRole("button", { name: "Health" }));
    await waitFor(() => expect(gatewayApiMock.health).toHaveBeenCalledWith(gateway.instance_id));
    fireEvent.click(within(card).getByRole("button", { name: "Connection Test" }));
    await waitFor(() => expect(gatewayApiMock.connectionTest).toHaveBeenCalledWith(gateway.instance_id, "csrf-proof"));

    fireEvent.click(within(card).getByRole("button", { name: "Replace" }));
    fireEvent.change(await screen.findByLabelText("新 Instance ID"), { target: { value: "00000000-0000-4000-8000-000000000003" } });
    fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(gatewayApiMock.replace).toHaveBeenCalledWith(gateway.instance_id, expect.objectContaining({ expected_revision: "4" }), "csrf-proof"));

    const retireButton = within(card).getByRole("button", { name: /Retire/ });
    await waitFor(() => expect(retireButton).not.toBeDisabled());
    fireEvent.click(retireButton);
    await waitFor(() => expect(screen.getByText("确认退役此 Gateway？")).toBeInTheDocument());
    const confirmRetire = document.querySelector(".ant-popconfirm-buttons .ant-btn-primary");
    expect(confirmRetire).toBeTruthy();
    fireEvent.click(confirmRetire!);
    await waitFor(() => expect(gatewayApiMock.retire).toHaveBeenCalledWith(gateway.instance_id, expect.objectContaining({ expected_revision: "4" }), "csrf-proof"));
  }, 15_000);

  it("registers a Gateway when the current slot is empty", async () => {
    const api = assetApi();
    const gatewayApiMock = gatewayApi();
    vi.mocked(gatewayApiMock.list).mockResolvedValue({ items: [], next_cursor: null, gateway_counts: { active: 0, retired: 0, total: 0 } });
    vi.mocked(gatewayApiMock.register).mockResolvedValue({ asset: gateway });
    render(<AssetRegistryView api={api} gatewayApi={gatewayApiMock} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    const card = await screen.findByTestId("gateway-management-card");
    fireEvent.click(await within(card).findByRole("button", { name: "登记 Gateway" }));
    fireEvent.change(await screen.findByLabelText("新 Instance ID"), { target: { value: gateway.instance_id } });
    fireEvent.change(screen.getByLabelText("显示名称"), { target: { value: "Primary Gateway" } });
    fireEvent.change(screen.getByLabelText("Management endpoint"), { target: { value: gateway.management_endpoint } });
    fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));
    await waitFor(() => expect(gatewayApiMock.register).toHaveBeenCalledWith(expect.objectContaining({ new_instance_id: gateway.instance_id, management_endpoint: gateway.management_endpoint }), "csrf-proof"));
  });

  it("submits an edit with the current revision and CSRF token", async () => {
    const api = assetApi();
    const gatewayApiMock = gatewayApi();
    render(<AssetRegistryView api={api} gatewayApi={gatewayApiMock} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    const card = await screen.findByTestId("gateway-management-card");
    await within(card).findByText("Primary Gateway");
    fireEvent.click(within(card).getByRole("button", { name: /编\s*辑/ }));
    const name = await screen.findByLabelText("显示名称");
    fireEvent.change(name, { target: { value: "Edited Gateway" } });
    fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));

    await waitFor(() => expect(gatewayApiMock.edit).toHaveBeenCalledWith(gateway.instance_id, expect.objectContaining({ expected_revision: "4", display_name: "Edited Gateway" }), "csrf-proof"));
    expect(document.body.textContent).not.toContain("vault://");
  });

  it("keeps retired history visible and exposes lineage through detail", async () => {
    const api = assetApi();
    const gatewayApiMock = gatewayApi();
    const retired: GatewayAsset = { ...gateway, lifecycle_status: "retired", singleton_id: undefined, retired_at: "2026-08-25T10:00:00Z", retired_by: "admin", retire_reason: "replacement" } as GatewayAsset;
    vi.mocked(gatewayApiMock.list).mockResolvedValue({ items: [retired], next_cursor: null, gateway_counts: { active: 0, retired: 1, total: 1 } });
    vi.mocked(gatewayApiMock.detail).mockResolvedValue({ asset: retired, predecessor: { old_instance_id: "old", new_instance_id: retired.instance_id, replaced_at: "2026-08-25T10:00:00Z", replaced_by: "admin", command_id: "command" }, successor: null });
    render(<AssetRegistryView api={api} gatewayApi={gatewayApiMock} csrfToken="csrf-proof" onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    const card = await screen.findByTestId("gateway-management-card");
    await within(card).findByText("Primary Gateway");
    fireEvent.click(within(card).getByRole("button", { name: /详\s*情/ }));
    expect(await screen.findByText("old")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "编辑" })).not.toBeInTheDocument();
  });
});

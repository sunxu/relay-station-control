import {
  getCurrentProviderInventoryPolicy,
  getEnvironment,
  getGatewayAsset,
  getNodeAsset,
  listNodeAssets,
  listNodeDrivers,
  registerNodeAsset, editNodeAsset, retireNodeAsset, replaceNodeAsset,
} from "./generated/control";
import type {
  CurrentProviderInventoryPolicyResponse,
  EnvironmentAsset as GeneratedEnvironmentAsset,
  GatewayAsset as GeneratedGatewayAsset,
  GatewayAssetResponse,
  NodeAsset as GeneratedNodeAsset,
  NodeAssetListResponse,
  NodeCapability,
  NodeDriverListResponse,
  NodeAssetDetailResponse,
} from "./generated/control";
import { AssetApiError } from "./asset-types";
import type {
  AssetApi,
  DriverAsset,
  DriverScope,
  EnvironmentAsset,
  GatewayAsset,
  GatewayState,
  NodeAsset,
  NodeFilters,
  NodePage,
  NodeDetail,
  ProviderPolicyState,
} from "./asset-types";

type GeneratedResponse<T> = { data: T | unknown; status: number };

const requestOptions: RequestInit = {
  cache: "no-store",
  credentials: "same-origin",
};

function unwrap<T>(response: GeneratedResponse<T>): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  throw new AssetApiError(response.status);
}

function mapEnvironment(value: GeneratedEnvironmentAsset): EnvironmentAsset {
  return {
    environmentId: value.environment_id,
    environmentType: value.environment_type,
    displayName: value.name,
  };
}

function mapGateway(value: GeneratedGatewayAsset): GatewayAsset {
  return {
    instanceId: value.instance_id,
    displayName: value.display_name,
    managementEndpoint: value.management_endpoint,
    secretConfigured: value.secret_configured,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  };
}

function mapNode(value: GeneratedNodeAsset): NodeAsset {
  return {
    instanceId: value.instance_id,
    displayName: value.display_name,
    nodeType: value.node_type,
    driverContractVersion: value.driver_contract_version,
    managementEndpoint: value.management_endpoint,
    secretConfigured: value.secret_configured,
    capabilities: [...value.capabilities],
    monitoringActive: value.monitoring.monitoring_active,
    monitoringEffectiveFrom: value.monitoring.effective_from ?? null,
    monitoringEffectiveTo: value.monitoring.effective_to ?? null,
    lifecycleStatus: value.lifecycle_status,
    revision: value.revision,
    retiredAt: value.retired_at ?? null,
    retiredBy: value.retired_by ?? null,
    retireReason: value.retire_reason ?? null,
  };
}

function mapNodeDetail(value: NodeAssetDetailResponse): NodeDetail {
  const lineage = (item: NodeAssetDetailResponse["predecessor"]) => item ? {
    oldInstanceId: item.old_instance_id,
    newInstanceId: item.new_instance_id,
    replacedAt: item.replaced_at,
  } : null;
  return { asset: mapNode(value.asset), predecessor: lineage(value.predecessor), successor: lineage(value.successor) };
}

export const generatedAssetApi: AssetApi = {
  async environment() {
    return mapEnvironment(unwrap<GeneratedEnvironmentAsset>(await getEnvironment(requestOptions)));
  },

  async gateway(): Promise<GatewayState> {
    const response = unwrap<GatewayAssetResponse>(await getGatewayAsset(requestOptions));
    return response.status === "registered" && response.gateway
      ? { status: "configured", gateway: mapGateway(response.gateway) }
      : { status: "not_configured", gateway: null };
  },

  async nodes(filters: NodeFilters): Promise<NodePage> {
    const response = unwrap<NodeAssetListResponse>(await listNodeAssets({
      limit: filters.limit,
      cursor: filters.cursor,
      node_type: filters.nodeType,
      capability: filters.capability as NodeCapability | undefined,
      monitoring_active: filters.monitoringActive,
      lifecycle: filters.lifecycle,
    }, requestOptions));
    return { items: response.items.map(mapNode), nextCursor: response.next_cursor ?? null };
  },

  async node(instanceId: string) {
    return mapNodeDetail(unwrap<NodeAssetDetailResponse>(await getNodeAsset(instanceId, requestOptions))).asset;
  },

  async nodeDetail(instanceId: string) {
    return mapNodeDetail(unwrap<NodeAssetDetailResponse>(await getNodeAsset(instanceId, requestOptions)));
  },

  async drivers(): Promise<DriverAsset[]> {
    const response = unwrap<NodeDriverListResponse>(await listNodeDrivers(requestOptions));
    return response.items.map((driver) => ({
      nodeType: driver.node_type,
      driverContractVersion: driver.driver_contract_version,
      displayName: driver.display_name,
      status: driver.lifecycle_status,
      capabilities: [...driver.capabilities],
    }));
  },

  async currentProviderPolicy(scope: DriverScope): Promise<ProviderPolicyState> {
    const response = unwrap<CurrentProviderInventoryPolicyResponse>(await getCurrentProviderInventoryPolicy({
      node_type: scope.nodeType,
      driver_contract_version: scope.driverContractVersion,
    }, requestOptions));
    return response.status === "configured" && response.policy ? {
      status: "configured",
      policy: {
        policyVersionId: response.policy.version_id,
        nodeType: response.node_type,
        driverContractVersion: response.driver_contract_version,
        activeProviders: [...response.policy.active_providers],
        outOfScopeProviders: [...response.policy.out_of_scope_providers],
        effectiveFrom: response.policy.effective_from,
        effectiveTo: response.policy.effective_to ?? null,
        createdAt: response.policy.created_at,
      },
    } : { status: "not_configured", policy: null };
  },
  async registerNode(data, csrf) { unwrap(await registerNodeAsset(data, {...requestOptions, headers:{"X-CSRF-Token":csrf}})); },
  async editNode(id, data, csrf) { unwrap(await editNodeAsset(id,data,{...requestOptions,headers:{"X-CSRF-Token":csrf}})); },
  async retireNode(id,data,csrf) { unwrap(await retireNodeAsset(id,data,{...requestOptions,headers:{"X-CSRF-Token":csrf}})); },
  async replaceNode(id,data,csrf) { unwrap(await replaceNodeAsset(id,data,{...requestOptions,headers:{"X-CSRF-Token":csrf}})); },
};

export interface EnvironmentAsset {
  environmentId: string;
  environmentType: string;
  displayName: string;
}

export interface GatewayAsset {
  instanceId: string;
  displayName: string;
  managementEndpoint: string;
  secretConfigured: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface GatewayState {
  status: "configured" | "not_configured";
  gateway: GatewayAsset | null;
}

export interface DriverAsset {
  nodeType: string;
  driverContractVersion: string;
  displayName: string;
  status: string;
  capabilities: string[];
}

export interface NodeAsset {
  instanceId: string;
  displayName: string;
  nodeType: string;
  driverContractVersion: string;
  managementEndpoint: string;
  secretConfigured: boolean;
  capabilities: string[];
  monitoringActive: boolean;
  monitoringEffectiveFrom: string | null;
  monitoringEffectiveTo: string | null;
  lifecycleStatus: "active" | "retired";
  revision: string;
  retiredAt: string | null;
  retiredBy: string | null;
  retireReason: "administrator_retire" | "replacement" | null;
}

export interface NodeFilters {
  lifecycle?: "active" | "retired" | "all";
  nodeType?: string;
  capability?: string;
  monitoringActive?: boolean;
  cursor?: string;
  limit?: number;
}

export interface NodePage {
  items: NodeAsset[];
  nextCursor: string | null;
}

export interface NodeLineage {
  oldInstanceId: string;
  newInstanceId: string;
  replacedAt: string;
}

export interface NodeDetail {
  asset: NodeAsset;
  predecessor: NodeLineage | null;
  successor: NodeLineage | null;
}

export interface ProviderPolicyAsset {
  policyVersionId: string;
  nodeType: string;
  driverContractVersion: string;
  activeProviders: string[];
  outOfScopeProviders: string[];
  effectiveFrom: string;
  effectiveTo: string | null;
  createdAt: string;
}

export interface ProviderPolicyState {
  status: "configured" | "not_configured";
  policy: ProviderPolicyAsset | null;
}

export interface DriverScope {
  nodeType: string;
  driverContractVersion: string;
}

export interface AssetApi {
  environment(): Promise<EnvironmentAsset>;
  gateway(): Promise<GatewayState>;
  nodes(filters: NodeFilters): Promise<NodePage>;
  node(instanceId: string): Promise<NodeAsset>;
  nodeDetail?(instanceId: string): Promise<NodeDetail>;
  drivers(): Promise<DriverAsset[]>;
  currentProviderPolicy(scope: DriverScope): Promise<ProviderPolicyState>;
  registerNode?(data: import("./generated/control").NodeRegisterRequest, csrf: string): Promise<void>;
  editNode?(id: string, data: import("./generated/control").NodeEditRequest, csrf: string): Promise<void>;
  retireNode?(id: string, data: import("./generated/control").NodeRetireRequest, csrf: string): Promise<void>;
  replaceNode?(id: string, data: import("./generated/control").NodeReplaceRequest, csrf: string): Promise<void>;
}

export class AssetApiError extends Error {
  readonly status: number;

  constructor(status: number) {
    super("asset request failed");
    this.name = "AssetApiError";
    this.status = status;
  }
}

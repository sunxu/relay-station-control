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
}

export interface NodeFilters {
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
  drivers(): Promise<DriverAsset[]>;
  currentProviderPolicy(scope: DriverScope): Promise<ProviderPolicyState>;
}

export class AssetApiError extends Error {
  readonly status: number;

  constructor(status: number) {
    super("asset request failed");
    this.name = "AssetApiError";
    this.status = status;
  }
}

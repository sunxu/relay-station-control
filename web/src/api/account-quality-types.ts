import type { NodeAccountQualityQueryRequestBasicStatus, NodeAccountQualityItem, NodeAccountQualityResponse, GetNodeAccountQualityQuality, GetNodeAccountQualityWindow, GetNodeAccountQualityLifecycle } from "./generated/control";

export type AccountQualityWindow = GetNodeAccountQualityWindow;
export type AccountQualityFilter = GetNodeAccountQualityQuality;
export type AccountQualityLifecycle = GetNodeAccountQualityLifecycle;
export type AccountQualityItem = NodeAccountQualityItem;
export type AccountQualityResponse = NodeAccountQualityResponse;

export interface AccountQualityApi {
  accountQuality(instanceId: string, window: AccountQualityWindow, provider?: string, quality?: AccountQualityFilter, cursor?: string, signal?: AbortSignal, lifecycle?: AccountQualityLifecycle): Promise<AccountQualityResponse>;
}

export interface AccountListFilters {
  instanceId: string;
  window?: AccountQualityWindow;
  provider?: string;
  quality?: AccountQualityFilter;
  lifecycle?: AccountQualityLifecycle;
  basicStatus?: NodeAccountQualityQueryRequestBasicStatus;
  email?: string;
  cursor?: string;
  limit?: number;
}

export interface AccountListApi {
  accountList(instanceId: string, filters: Omit<AccountListFilters, "instanceId">, csrfToken: string, signal?: AbortSignal): Promise<AccountQualityResponse>;
}

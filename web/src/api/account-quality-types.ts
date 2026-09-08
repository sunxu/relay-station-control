import type { NodeAccountQualityItem, NodeAccountQualityResponse, GetNodeAccountQualityQuality, GetNodeAccountQualityWindow } from "./generated/control";

export type AccountQualityWindow = GetNodeAccountQualityWindow;
export type AccountQualityFilter = GetNodeAccountQualityQuality;
export type AccountQualityItem = NodeAccountQualityItem;
export type AccountQualityResponse = NodeAccountQualityResponse;

export interface AccountQualityApi {
  accountQuality(instanceId: string, window: AccountQualityWindow, provider?: string, quality?: AccountQualityFilter, cursor?: string, signal?: AbortSignal): Promise<AccountQualityResponse>;
}

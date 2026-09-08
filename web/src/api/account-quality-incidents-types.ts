import type { NodeAccountQualityIncidentItem, NodeAccountQualityIncidentResponse } from "./generated/control";

export type AccountQualityIncidentItem = NodeAccountQualityIncidentItem;
export type AccountQualityIncidentResponse = NodeAccountQualityIncidentResponse;
export type IncidentFailureClass = "auth" | "quota" | "rate_limit" | "upstream";

export interface AccountQualityIncidentsApi {
  incidents(instanceId: string, provider?: string, failureClass?: IncidentFailureClass, cursor?: string, signal?: AbortSignal): Promise<AccountQualityIncidentResponse>;
}

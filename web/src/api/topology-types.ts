import type {
  CrossNodeDuplicateOccurrenceEvidenceListResponse,
  CrossNodeDuplicateOccurrenceListResponse,
  CrossNodeDuplicateOccurrenceSummary,
  NodeDuplicateHistoryResponse,
  NodeInventoryProviderState,
  NodeInventoryProviderStatesResponse,
  NodeRelayBindingResponse,
} from "./generated/control";
import type { AccountListApi, AccountQualityApi } from "./account-quality-types";
import type { AccountRequestHistoryApi } from "./account-request-history-types";
import type { AccountQualityIncidentsApi } from "./account-quality-incidents-types";
import type { AccountAvailabilityApi } from "./account-availability-types";

export type TopologyProviderState = NodeInventoryProviderState;
export type TopologyOccurrence = CrossNodeDuplicateOccurrenceSummary;
export type TopologyBinding = NodeRelayBindingResponse;

export interface TopologyApi extends AccountQualityApi, AccountListApi, AccountRequestHistoryApi, AccountQualityIncidentsApi, AccountAvailabilityApi {
  providers(instanceId: string, signal?: AbortSignal): Promise<NodeInventoryProviderStatesResponse>;
  binding(instanceId: string, signal?: AbortSignal): Promise<NodeRelayBindingResponse>;
  currentDuplicates(instanceId: string, cursor?: string, signal?: AbortSignal): Promise<CrossNodeDuplicateOccurrenceListResponse>;
  evidence(occurrenceId: string, cursor?: string, signal?: AbortSignal): Promise<CrossNodeDuplicateOccurrenceEvidenceListResponse>;
  history(instanceId: string, status?: "ACTIVE" | "RESOLVED", cursor?: string, signal?: AbortSignal): Promise<NodeDuplicateHistoryResponse>;
}

export class TopologyApiError extends Error {
  constructor(readonly status: number) { super("topology request failed"); }
}

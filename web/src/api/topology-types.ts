import type {
  CrossNodeDuplicateOccurrenceEvidenceListResponse,
  CrossNodeDuplicateOccurrenceListResponse,
  CrossNodeDuplicateOccurrenceSummary,
  NodeDuplicateHistoryResponse,
  NodeInventoryProviderState,
  NodeInventoryProviderStatesResponse,
  NodeRelayBindingResponse,
} from "./generated/control";
import type { AccountQualityApi } from "./account-quality-types";

export type TopologyProviderState = NodeInventoryProviderState;
export type TopologyOccurrence = CrossNodeDuplicateOccurrenceSummary;
export type TopologyBinding = NodeRelayBindingResponse;

export interface TopologyApi extends AccountQualityApi {
  providers(instanceId: string, signal?: AbortSignal): Promise<NodeInventoryProviderStatesResponse>;
  binding(instanceId: string, signal?: AbortSignal): Promise<NodeRelayBindingResponse>;
  currentDuplicates(instanceId: string, cursor?: string, signal?: AbortSignal): Promise<CrossNodeDuplicateOccurrenceListResponse>;
  evidence(occurrenceId: string, cursor?: string, signal?: AbortSignal): Promise<CrossNodeDuplicateOccurrenceEvidenceListResponse>;
  history(instanceId: string, status?: "ACTIVE" | "RESOLVED", cursor?: string, signal?: AbortSignal): Promise<NodeDuplicateHistoryResponse>;
}

export class TopologyApiError extends Error {
  constructor(readonly status: number) { super("topology request failed"); }
}

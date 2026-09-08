import { getNodeAccountQuality, queryNodeAccountQuality, listNodeAccountRequestHistory, listNodeAccountQualityIncidents, getNodeInventoryProviderStates, getNodeRelayBinding, listCrossNodeDuplicateOccurrences, listCrossNodeDuplicateOccurrenceEvidence, listNodeDuplicateHistory } from "./generated/control";
import type { GetNodeAccountQualityParams, NodeAccountQualityQueryRequest, NodeAccountQualityResponse, ListNodeAccountRequestHistoryParams, NodeAccountRequestHistoryResponse, ListNodeAccountQualityIncidentsParams, NodeAccountQualityIncidentResponse } from "./generated/control";
import type { IncidentFailureClass } from "./account-quality-incidents-types";
import type { AccountQualityFilter, AccountQualityLifecycle, AccountQualityWindow } from "./account-quality-types";
import type { TopologyApi } from "./topology-types";
import { TopologyApiError } from "./topology-types";

async function read<T>(pending: Promise<{ data: unknown; status: number }>): Promise<T> {
  const response = await pending;
  if (response.status < 200 || response.status >= 300) throw new TopologyApiError(response.status);
  return response.data as T;
}

export const generatedTopologyApi: TopologyApi = {
  accountList: (id, filters, csrfToken, signal) => {
    const body: NodeAccountQualityQueryRequest = {
      window: filters.window ?? "15m",
      provider: filters.provider,
      lifecycle: filters.lifecycle,
      basic_status: filters.basicStatus,
      quality: filters.quality,
      email: filters.email,
      cursor: filters.cursor,
      limit: filters.limit ?? 50,
    };
    return read<NodeAccountQualityResponse>(queryNodeAccountQuality(id, body, { signal, cache: "no-store", credentials: "same-origin", headers: { "X-CSRF-Token": csrfToken } }));
  },
  accountQuality: (id: string, window: AccountQualityWindow, provider?: string, quality?: AccountQualityFilter, cursor?: string, signal?: AbortSignal, lifecycle?: AccountQualityLifecycle) => {
    const params: GetNodeAccountQualityParams = { window, provider, quality, cursor, limit: 25, lifecycle };
    return read<NodeAccountQualityResponse>(getNodeAccountQuality(id, params, { signal, cache: "no-store", credentials: "same-origin" }));
  },
  requestHistory: (id: string, accountKey: string, cursor?: string, signal?: AbortSignal) => {
    const params: ListNodeAccountRequestHistoryParams = { account_key: accountKey, cursor, limit: 25 };
    return read<NodeAccountRequestHistoryResponse>(listNodeAccountRequestHistory(id, params, { signal, cache: "no-store", credentials: "same-origin" }));
  },
  incidents: (id: string, provider?: string, failureClass?: IncidentFailureClass, cursor?: string, signal?: AbortSignal) => {
    const params: ListNodeAccountQualityIncidentsParams = { provider, failure_class: failureClass, cursor, limit: 25 };
    return read<NodeAccountQualityIncidentResponse>(listNodeAccountQualityIncidents(id, params, { signal, cache: "no-store", credentials: "same-origin" }));
  },
  providers: (id, signal) => read(getNodeInventoryProviderStates(id, { signal, cache: "no-store", credentials: "same-origin" })),
  binding: (id, signal) => read(getNodeRelayBinding(id, { signal, cache: "no-store", credentials: "same-origin" })),
  currentDuplicates: (id, cursor, signal) => read(listCrossNodeDuplicateOccurrences({ status: "ACTIVE", instance_id: id, cursor, limit: 25 }, { signal, cache: "no-store", credentials: "same-origin" })),
  evidence: (id, cursor, signal) => read(listCrossNodeDuplicateOccurrenceEvidence(id, { cursor, limit: 25 }, { signal, cache: "no-store", credentials: "same-origin" })),
  history: (id, status, cursor, signal) => {
    return read(listNodeDuplicateHistory(id, { status, cursor, limit: 25 }, { signal, cache: "no-store", credentials: "same-origin" }));
  },
};

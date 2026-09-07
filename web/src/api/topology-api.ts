import { getNodeInventoryProviderStates, getNodeRelayBinding, listCrossNodeDuplicateOccurrences, listCrossNodeDuplicateOccurrenceEvidence, listNodeDuplicateHistory } from "./generated/control";
import type { TopologyApi } from "./topology-types";
import { TopologyApiError } from "./topology-types";

async function read<T>(pending: Promise<{ data: unknown; status: number }>): Promise<T> {
  const response = await pending;
  if (response.status < 200 || response.status >= 300) throw new TopologyApiError(response.status);
  return response.data as T;
}

export const generatedTopologyApi: TopologyApi = {
  providers: (id, signal) => read(getNodeInventoryProviderStates(id, { signal, cache: "no-store", credentials: "same-origin" })),
  binding: (id, signal) => read(getNodeRelayBinding(id, { signal, cache: "no-store", credentials: "same-origin" })),
  currentDuplicates: (id, cursor, signal) => read(listCrossNodeDuplicateOccurrences({ status: "ACTIVE", instance_id: id, cursor, limit: 25 }, { signal, cache: "no-store", credentials: "same-origin" })),
  evidence: (id, cursor, signal) => read(listCrossNodeDuplicateOccurrenceEvidence(id, { cursor, limit: 25 }, { signal, cache: "no-store", credentials: "same-origin" })),
  history: (id, status, cursor, signal) => {
    return read(listNodeDuplicateHistory(id, { status, cursor, limit: 25 }, { signal, cache: "no-store", credentials: "same-origin" }));
  },
};

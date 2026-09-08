import { useQuery } from "@tanstack/react-query";
import type { AccountQualityIncidentsApi, IncidentFailureClass } from "./account-quality-incidents-types";

const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;
export function useAccountQualityIncidents(api: AccountQualityIncidentsApi, instanceId?: string, provider?: string, failureClass?: IncidentFailureClass, cursor?: string) {
  return useQuery({
    queryKey: ["topology", "account-quality-incidents", instanceId, provider, failureClass, cursor],
    queryFn: ({ signal }) => api.incidents(instanceId!, provider, failureClass, cursor, signal),
    enabled: Boolean(instanceId),
    ...options,
  });
}

import { useQuery } from "@tanstack/react-query";
import type { AccountRequestHistoryApi } from "./account-request-history-types";

const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;

export function useAccountRequestHistory(api: AccountRequestHistoryApi, instanceId?: string, accountKey?: string, cursor?: string) {
  return useQuery({
    queryKey: ["topology", "account-request-history", instanceId, accountKey, cursor],
    queryFn: ({ signal }) => api.requestHistory(instanceId!, accountKey!, cursor, signal),
    enabled: Boolean(instanceId && accountKey),
    ...options,
  });
}

import { useQuery } from "@tanstack/react-query";
import type { AccountAvailabilityApi, AccountAvailabilityOccurrenceStatus } from "./account-availability-types";

const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;

export function useAccountAvailabilityOccurrences(
  api: AccountAvailabilityApi,
  instanceId?: string,
  accountKey?: string,
  status?: AccountAvailabilityOccurrenceStatus,
  cursor?: string,
) {
  return useQuery({
    queryKey: ["topology", "account-availability-occurrences", instanceId, accountKey, status, cursor],
    queryFn: ({ signal }) => api.accountAvailabilityOccurrences(instanceId!, accountKey!, status, cursor, signal),
    enabled: Boolean(instanceId && accountKey),
    ...options,
  });
}

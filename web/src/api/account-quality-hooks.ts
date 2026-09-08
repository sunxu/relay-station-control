import { useQuery } from "@tanstack/react-query";
import type { AccountQualityApi, AccountQualityFilter, AccountQualityWindow } from "./account-quality-types";

const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;

export function useTopologyAccountQuality(api: AccountQualityApi, instanceId?: string, window: AccountQualityWindow = "15m", provider?: string, quality?: AccountQualityFilter, cursor?: string) {
  return useQuery({
    queryKey: ["topology", "account-quality", instanceId, window, provider, quality, cursor],
    queryFn: ({ signal }) => api.accountQuality(instanceId!, window, provider, quality, cursor, signal),
    enabled: Boolean(instanceId),
    ...options,
  });
}

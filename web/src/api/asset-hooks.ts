import { useQuery } from "@tanstack/react-query";
import type { AssetApi, DriverScope, NodeFilters } from "./asset-types";

const queryPolicy = {
  gcTime: 0,
  retry: false,
  staleTime: 0,
  refetchOnMount: "always" as const,
  refetchOnReconnect: false,
  refetchOnWindowFocus: false,
};

export function useEnvironmentAsset(api: AssetApi, enabled = true) {
  return useQuery({ queryKey: ["asset-registry", "environment"], queryFn: () => api.environment(), enabled, ...queryPolicy });
}

export function useGatewayAsset(api: AssetApi, enabled = true) {
  return useQuery({ queryKey: ["asset-registry", "gateway"], queryFn: () => api.gateway(), enabled, ...queryPolicy });
}

export function useNodeAssets(api: AssetApi, filters: NodeFilters, enabled = true) {
  return useQuery({
    queryKey: ["asset-registry", "nodes", filters],
    queryFn: () => api.nodes(filters),
    enabled,
    ...queryPolicy,
  });
}

export function useNodeAsset(api: AssetApi, instanceId?: string) {
  return useQuery({
    queryKey: ["asset-registry", "node", instanceId],
    queryFn: () => api.node(instanceId!),
    enabled: Boolean(instanceId),
    ...queryPolicy,
  });
}

export function useDriverAssets(api: AssetApi, enabled = true) {
  return useQuery({ queryKey: ["asset-registry", "drivers"], queryFn: () => api.drivers(), enabled, ...queryPolicy });
}

export function useCurrentProviderPolicy(api: AssetApi, scope?: DriverScope, enabled = true) {
  return useQuery({
    queryKey: ["asset-registry", "provider-policy", scope],
    queryFn: () => api.currentProviderPolicy(scope!),
    enabled: Boolean(scope) && enabled,
    ...queryPolicy,
  });
}

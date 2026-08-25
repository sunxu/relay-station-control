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

export function useEnvironmentAsset(api: AssetApi) {
  return useQuery({ queryKey: ["asset-registry", "environment"], queryFn: () => api.environment(), ...queryPolicy });
}

export function useGatewayAsset(api: AssetApi) {
  return useQuery({ queryKey: ["asset-registry", "gateway"], queryFn: () => api.gateway(), ...queryPolicy });
}

export function useNodeAssets(api: AssetApi, filters: NodeFilters) {
  return useQuery({
    queryKey: ["asset-registry", "nodes", filters],
    queryFn: () => api.nodes(filters),
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

export function useDriverAssets(api: AssetApi) {
  return useQuery({ queryKey: ["asset-registry", "drivers"], queryFn: () => api.drivers(), ...queryPolicy });
}

export function useCurrentProviderPolicy(api: AssetApi, scope?: DriverScope) {
  return useQuery({
    queryKey: ["asset-registry", "provider-policy", scope],
    queryFn: () => api.currentProviderPolicy(scope!),
    enabled: Boolean(scope),
    ...queryPolicy,
  });
}

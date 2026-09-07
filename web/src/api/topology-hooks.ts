import { useQuery } from "@tanstack/react-query";
import type { TopologyApi } from "./topology-types";

const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;
export function useTopologyProviders(api: TopologyApi, id?: string) { return useQuery({ queryKey: ["topology", "providers", id], queryFn: ({ signal }) => api.providers(id!, signal), enabled: Boolean(id), ...options }); }
export function useTopologyBinding(api: TopologyApi, id?: string) { return useQuery({ queryKey: ["topology", "binding", id], queryFn: ({ signal }) => api.binding(id!, signal), enabled: Boolean(id), ...options }); }
export function useTopologyCurrentDuplicates(api: TopologyApi, id?: string, cursor?: string) { return useQuery({ queryKey: ["topology", "current-duplicates", id, cursor], queryFn: ({ signal }) => api.currentDuplicates(id!, cursor, signal), enabled: Boolean(id), ...options }); }
export function useTopologyEvidence(api: TopologyApi, id?: string, cursor?: string) { return useQuery({ queryKey: ["topology", "evidence", id, cursor], queryFn: ({ signal }) => api.evidence(id!, cursor, signal), enabled: Boolean(id), ...options }); }
export function useTopologyHistory(api: TopologyApi, id?: string, status?: "ACTIVE" | "RESOLVED", cursor?: string) { return useQuery({ queryKey: ["topology", "history", id, status, cursor], queryFn: ({ signal }) => api.history(id!, status, cursor, signal), enabled: Boolean(id), ...options }); }

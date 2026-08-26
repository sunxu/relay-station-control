import { useQuery } from "@tanstack/react-query";
import type { JobApi, JobFilters } from "./job-types";

const queryPolicy = {
  gcTime: 0,
  retry: false,
  staleTime: 0,
  refetchOnMount: "always" as const,
  refetchOnReconnect: false,
  refetchOnWindowFocus: false,
};

export function useJobs(api: JobApi, filters: JobFilters) {
  return useQuery({
    queryKey: ["durable-jobs", "list", filters],
    queryFn: () => api.jobs(filters),
    ...queryPolicy,
  });
}

export function useJob(api: JobApi, jobId?: string) {
  return useQuery({
    queryKey: ["durable-jobs", "detail", jobId],
    queryFn: () => api.job(jobId!),
    enabled: Boolean(jobId),
    ...queryPolicy,
  });
}

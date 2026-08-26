import { getJob, listJobs } from "./generated/control";
import type {
  JobDetail as GeneratedJobDetail,
  JobLifecycleEvent as GeneratedJobLifecycleEvent,
  JobListResponse,
  JobSummary as GeneratedJobSummary,
} from "./generated/control";
import { JobApiError } from "./job-types";
import type { JobApi, JobDetail, JobLifecycleEvent, JobSummary } from "./job-types";

type GeneratedResponse<T> = { data: T | unknown; status: number };

const requestOptions: RequestInit = {
  cache: "no-store",
  credentials: "same-origin",
};

function unwrap<T>(response: GeneratedResponse<T>): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  throw new JobApiError(response.status);
}

function mapSummary(value: GeneratedJobSummary): JobSummary {
  return {
    jobId: value.job_id,
    operationId: value.operation_id,
    jobKind: value.job_kind,
    status: value.status,
    attemptCount: value.attempt_count,
    maxAttempts: value.max_attempts,
    availableAt: value.available_at,
    startedAt: value.started_at ?? null,
    completedAt: value.completed_at ?? null,
    cancelRequested: value.cancel_requested,
    errorCode: value.error_code ?? null,
    outboxStatus: value.outbox_status,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  };
}

function mapEvent(value: GeneratedJobLifecycleEvent): JobLifecycleEvent {
  return {
    sequence: value.sequence,
    eventType: value.event_type,
    fromStatus: value.from_status ?? null,
    toStatus: value.to_status,
    attemptCount: value.attempt_count,
    actorType: value.actor_type,
    reasonCode: value.reason_code ?? null,
    errorCode: value.error_code ?? null,
    occurredAt: value.occurred_at,
  };
}

function mapDetail(value: GeneratedJobDetail): JobDetail {
  return { ...mapSummary(value), events: value.events.map(mapEvent) };
}

export const generatedJobApi: JobApi = {
  async jobs(filters) {
    const response = unwrap<JobListResponse>(await listJobs({
      job_kind: filters.jobKind,
      status: filters.status,
      created_from: filters.createdFrom,
      created_to: filters.createdTo,
      cursor: filters.cursor,
      limit: filters.limit,
    }, requestOptions));
    return { items: response.items.map(mapSummary), nextCursor: response.next_cursor ?? null };
  },

  async job(jobId) {
    return mapDetail(unwrap<GeneratedJobDetail>(await getJob(jobId, requestOptions)));
  },
};

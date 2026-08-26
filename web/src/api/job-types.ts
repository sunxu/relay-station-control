export const jobStatuses = [
  "pending", "running", "verifying", "retry_wait", "rolling_back",
  "succeeded", "failed", "rolled_back", "cancelled",
] as const;

export type JobStatus = typeof jobStatuses[number];
export type JobOutboxStatus = "pending" | "publishing" | "sent" | "suppressed" | "retry_wait" | "failed";

export interface JobSummary {
  jobId: string;
  operationId: string;
  jobKind: string;
  status: JobStatus;
  attemptCount: number;
  maxAttempts: number;
  availableAt: string;
  startedAt: string | null;
  completedAt: string | null;
  cancelRequested: boolean;
  errorCode: string | null;
  outboxStatus: JobOutboxStatus;
  createdAt: string;
  updatedAt: string;
}

export interface JobLifecycleEvent {
  sequence: number;
  eventType: string;
  fromStatus: JobStatus | null;
  toStatus: JobStatus;
  attemptCount: number;
  actorType: string;
  reasonCode: string | null;
  errorCode: string | null;
  occurredAt: string;
}

export interface JobDetail extends JobSummary {
  events: JobLifecycleEvent[];
}

export interface JobFilters {
  jobKind?: string;
  status?: JobStatus;
  createdFrom?: string;
  createdTo?: string;
  cursor?: string;
  limit?: number;
}

export interface JobPage {
  items: JobSummary[];
  nextCursor: string | null;
}

export interface JobApi {
  jobs(filters: JobFilters): Promise<JobPage>;
  job(jobId: string): Promise<JobDetail>;
}

export class JobApiError extends Error {
  readonly status: number;

  constructor(status: number) {
    super("job request failed");
    this.name = "JobApiError";
    this.status = status;
  }
}

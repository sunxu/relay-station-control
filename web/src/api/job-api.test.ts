import { beforeEach, describe, expect, it, vi } from "vitest";
import { getJob, listJobs } from "./generated/control";
import { generatedJobApi } from "./job-api";
import { JobApiError } from "./job-types";

vi.mock("./generated/control", () => ({ listJobs: vi.fn(), getJob: vi.fn() }));

const generatedSummary = {
  job_id: "00000000-0000-4000-8000-000000000101",
  operation_id: "00000000-0000-4000-8000-000000000201",
  job_kind: "synthetic.noop",
  status: "pending" as const,
  attempt_count: 0,
  max_attempts: 3,
  available_at: "2026-08-25T10:00:00Z",
  started_at: null,
  completed_at: null,
  cancel_requested: false,
  error_code: null,
  outbox_status: "suppressed" as const,
  created_at: "2026-08-25T10:00:00Z",
  updated_at: "2026-08-25T10:00:00Z",
};

beforeEach(() => vi.clearAllMocks());

describe("generated durable job client adapter", () => {
  it("uses same-origin no-store reads and maps only public fields", async () => {
    vi.mocked(listJobs).mockResolvedValue({ data: { items: [generatedSummary], next_cursor: "next" }, status: 200, headers: new Headers() });
    vi.mocked(getJob).mockResolvedValue({
      data: {
        ...generatedSummary,
        payload: "SECRET-CANARY",
        error_summary: "ERROR-CANARY",
        events: [{
          sequence: 1, event_type: "enqueued", from_status: null, to_status: "pending", attempt_count: 0,
          actor_type: "service", reason_code: "job_enqueued", error_code: null, occurred_at: "2026-08-25T10:00:00Z",
        }],
      },
      status: 200,
      headers: new Headers(),
    } as Awaited<ReturnType<typeof getJob>>);

    const page = await generatedJobApi.jobs({ jobKind: "synthetic.noop", status: "pending", limit: 50 });
    const detail = await generatedJobApi.job(generatedSummary.job_id);
    expect(page).toEqual({ items: [expect.objectContaining({ jobId: generatedSummary.job_id, jobKind: "synthetic.noop" })], nextCursor: "next" });
    expect(detail.events).toEqual([expect.objectContaining({ eventType: "enqueued", occurredAt: "2026-08-25T10:00:00Z" })]);
    expect(detail).not.toHaveProperty("payload");
    expect(detail).not.toHaveProperty("error_summary");
    expect(listJobs).toHaveBeenCalledWith(expect.objectContaining({ job_kind: "synthetic.noop", status: "pending", limit: 50 }), expect.objectContaining({ cache: "no-store", credentials: "same-origin" }));
    expect(getJob).toHaveBeenCalledWith(generatedSummary.job_id, expect.objectContaining({ cache: "no-store", credentials: "same-origin" }));
  });

  it("converts non-success responses to a bounded status-only error", async () => {
    vi.mocked(listJobs).mockResolvedValue({ data: { code: "temporarily_unavailable", message: "postgres://SECRET", request_id: "request" }, status: 503, headers: new Headers() });
    await expect(generatedJobApi.jobs({})).rejects.toEqual(expect.objectContaining<JobApiError>({ status: 503, name: "JobApiError", message: "job request failed" }));
  });
});

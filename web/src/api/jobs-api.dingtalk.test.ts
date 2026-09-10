import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { generatedJobApi } from "./job-api";

const jobId = "00000000-0000-4000-8000-000000000501";
const operationId = "00000000-0000-4000-8000-000000000601";

const failedSummary = {
  job_id: jobId,
  operation_id: operationId,
  job_kind: "dingtalk_alert_delivery",
  status: "failed",
  attempt_count: 5,
  max_attempts: 5,
  available_at: "2026-09-10T01:02:00Z",
  started_at: "2026-09-10T01:02:04Z",
  completed_at: "2026-09-10T01:02:05Z",
  cancel_requested: false,
  error_code: "dingtalk_business_rejected",
  outbox_status: "failed",
  created_at: "2026-09-10T01:02:03Z",
  updated_at: "2026-09-10T01:02:06Z",
};

const failedDetail = {
  ...failedSummary,
  events: [{
    sequence: 6,
    event_type: "failed",
    from_status: "running",
    to_status: "failed",
    attempt_count: 5,
    actor_type: "worker",
    reason_code: "job_failed",
    error_code: "dingtalk_business_rejected",
    occurred_at: "2026-09-10T01:02:05Z",
  }],
};

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => vi.stubGlobal("fetch", vi.fn()));
afterEach(() => vi.unstubAllGlobals());

describe("generated Jobs transport for DingTalk delivery", () => {
  it("maps the existing failed-job diagnostics and drops payload and uncontracted fields", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === `/api/jobs/${jobId}`) {
        return jsonResponse({
          ...failedDetail,
          payload: { email: "operator@example.invalid", message: "safe display snapshot" },
          webhook: "https://dingtalk.invalid/robot?access_token=SECRET",
          query: "?sign=SECRET",
          signing: "SECRET-SIGNATURE",
          rawresponse: "raw provider response",
          raw_response: "raw provider response",
          malicious_extra: { html: "<img src=x onerror=alert(1)>" },
        });
      }
      return jsonResponse({
        items: [{
          ...failedSummary,
          payload: "PAYLOAD-CANARY",
          webhook: "WEBHOOK-CANARY",
          query: "QUERY-CANARY",
          signing: "SIGNING-CANARY",
          rawresponse: "RAWRESPONSE-CANARY",
          malicious_extra: "<script>evil()</script>",
        }],
        next_cursor: null,
      });
    });

    const page = await generatedJobApi.jobs({ jobKind: "dingtalk_alert_delivery", status: "failed", limit: 50 });
    const detail = await generatedJobApi.job(jobId);

    expect(page).toEqual({
      items: [{
        jobId,
        operationId,
        jobKind: "dingtalk_alert_delivery",
        status: "failed",
        attemptCount: 5,
        maxAttempts: 5,
        availableAt: "2026-09-10T01:02:00Z",
        startedAt: "2026-09-10T01:02:04Z",
        completedAt: "2026-09-10T01:02:05Z",
        cancelRequested: false,
        errorCode: "dingtalk_business_rejected",
        outboxStatus: "failed",
        createdAt: "2026-09-10T01:02:03Z",
        updatedAt: "2026-09-10T01:02:06Z",
      }],
      nextCursor: null,
    });
    expect(detail).toEqual({
      jobId,
      operationId,
      jobKind: "dingtalk_alert_delivery",
      status: "failed",
      attemptCount: 5,
      maxAttempts: 5,
      availableAt: "2026-09-10T01:02:00Z",
      startedAt: "2026-09-10T01:02:04Z",
      completedAt: "2026-09-10T01:02:05Z",
      cancelRequested: false,
      errorCode: "dingtalk_business_rejected",
      outboxStatus: "failed",
      createdAt: "2026-09-10T01:02:03Z",
      updatedAt: "2026-09-10T01:02:06Z",
      events: [{
        sequence: 6,
        eventType: "failed",
        fromStatus: "running",
        toStatus: "failed",
        attemptCount: 5,
        actorType: "worker",
        reasonCode: "job_failed",
        errorCode: "dingtalk_business_rejected",
        occurredAt: "2026-09-10T01:02:05Z",
      }],
    });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/jobs?job_kind=dingtalk_alert_delivery&status=failed&limit=50", expect.objectContaining({
      method: "GET",
      cache: "no-store",
      credentials: "same-origin",
    }));
    expect(fetchMock).toHaveBeenNthCalledWith(2, `/api/jobs/${jobId}`, expect.objectContaining({
      method: "GET",
      cache: "no-store",
      credentials: "same-origin",
    }));

    for (const field of ["payload", "webhook", "query", "signing", "rawresponse", "raw_response", "malicious_extra"]) {
      expect(page.items[0]).not.toHaveProperty(field);
      expect(detail).not.toHaveProperty(field);
    }
  });

  it("turns an error body containing URL/query material into a status-only error", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValue(jsonResponse({
      code: "temporarily_unavailable",
      message: "POST https://dingtalk.invalid/robot?sign=SECRET&query=raw-response",
      request_id: "request-fixed",
    }, 503));

    await expect(generatedJobApi.jobs({ jobKind: "dingtalk_alert_delivery", status: "failed", limit: 50 })).rejects.toEqual(expect.objectContaining({
      name: "JobApiError",
      status: 503,
      message: "job request failed",
    }));
    await expect(generatedJobApi.jobs({ jobKind: "dingtalk_alert_delivery", status: "failed", limit: 50 })).rejects.not.toThrow(/dingtalk\.invalid|sign=SECRET|raw-response/);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthApi } from "../api/auth-api";
import { AuthProvider } from "../auth/AuthContext";
import { formatDateTime } from "../time";
import JobsPage from "./JobsPage";

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

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const authApi = {
  bootstrapStatus: vi.fn().mockResolvedValue("completed"),
  session: vi.fn().mockResolvedValue(null),
} as unknown as AuthApi;

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthProvider api={authApi}>{children}</AuthProvider>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn().mockImplementation(async (input) => {
    if (String(input) === `/api/jobs/${jobId}`) {
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
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("JobsPage DingTalk failed delivery diagnostics", () => {
  it("shows existing safe diagnostics in the Jobs view without a notification surface or payload fields", async () => {
    render(<JobsPage />, { wrapper: Wrapper });

    expect(await screen.findByText("dingtalk_alert_delivery")).toBeInTheDocument();
    const row = screen.getByText("dingtalk_alert_delivery").closest("tr")!;
    expect(within(row).getAllByText("failed").length).toBeGreaterThan(0);
    expect(screen.getByText("5 / 5")).toBeInTheDocument();
    expect(screen.getByText(formatDateTime(failedSummary.created_at))).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /通知|Notification|Webhook|webhook/ })).not.toBeInTheDocument();
    expect(screen.queryByText(/webhook|query|signing|rawresponse|raw_response|通知|notification/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
    const drawer = await screen.findByRole("dialog");
    expect(await within(drawer).findByText("dingtalk_alert_delivery")).toBeInTheDocument();
    expect(within(drawer).getAllByText("failed").length).toBeGreaterThan(0);
    expect(within(drawer).getByText("5 / 5")).toBeInTheDocument();
    expect(within(drawer).getAllByText("dingtalk_business_rejected").length).toBeGreaterThan(0);
    expect(within(drawer).getByText(formatDateTime(failedSummary.started_at))).toBeInTheDocument();
    expect(within(drawer).getByText(formatDateTime(failedSummary.completed_at))).toBeInTheDocument();
    expect(within(drawer).getByText(formatDateTime(failedSummary.created_at))).toBeInTheDocument();
    expect(within(drawer).getByText(formatDateTime(failedSummary.updated_at))).toBeInTheDocument();
    expect(within(drawer).getByText("#6 failed")).toBeInTheDocument();

    const body = document.body.textContent ?? "";
    for (const canary of [
      "PAYLOAD-CANARY",
      "WEBHOOK-CANARY",
      "QUERY-CANARY",
      "SIGNING-CANARY",
      "RAWRESPONSE-CANARY",
      "<script>evil()</script>",
      "https://dingtalk.invalid/robot?access_token=SECRET",
    ]) {
      expect(body).not.toContain(canary);
    }
  });
});

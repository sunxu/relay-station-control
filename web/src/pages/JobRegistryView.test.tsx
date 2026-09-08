import { formatDateTime } from "../time";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { JobApiError } from "../api/job-types";
import type { JobApi, JobDetail, JobSummary } from "../api/job-types";
import { JobRegistryView } from "./JobRegistryView";

const summary: JobSummary = {
  jobId: "00000000-0000-4000-8000-000000000101",
  operationId: "00000000-0000-4000-8000-000000000201",
  jobKind: "synthetic.noop",
  status: "running",
  attemptCount: 1,
  maxAttempts: 3,
  availableAt: "2026-08-25T10:00:00Z",
  startedAt: "2026-08-25T10:01:00Z",
  completedAt: null,
  cancelRequested: false,
  errorCode: null,
  outboxStatus: "suppressed",
  createdAt: "2026-08-25T10:00:00Z",
  updatedAt: "2026-08-25T10:01:00Z",
};

const detail: JobDetail = {
  ...summary,
  events: [{
    sequence: 1,
    eventType: "enqueued",
    fromStatus: null,
    toStatus: "pending",
    attemptCount: 0,
    actorType: "service",
    reasonCode: "job_enqueued",
    errorCode: null,
    occurredAt: "2026-08-25T10:00:00Z",
  }],
};

function makeApi(): JobApi {
  return {
    jobs: vi.fn().mockResolvedValue({ items: [summary], nextCursor: null }),
    job: vi.fn().mockResolvedValue(detail),
  };
}

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

describe("durable job read-only view", () => {
  it("renders an empty state and bounded page request without task mutations", async () => {
    const api = makeApi();
    vi.mocked(api.jobs).mockResolvedValue({ items: [], nextCursor: null });
    render(<JobRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });
    expect(await screen.findByText("当前过滤条件下没有持久任务")).toBeInTheDocument();
    expect(api.jobs).toHaveBeenCalledWith(expect.objectContaining({ limit: 50, cursor: undefined }));
    expect(screen.queryByRole("button", { name: /创建任务|重试任务|取消任务|删除任务/ })).not.toBeInTheDocument();
  });

  it("filters, pages, renders local values and opens a redacted event timeline", async () => {
    const api = makeApi();
    vi.mocked(api.jobs).mockImplementation(async (filters) => ({
      items: [{ ...summary, jobId: filters.cursor ? "00000000-0000-4000-8000-000000000102" : summary.jobId }],
      nextCursor: filters.cursor ? null : "cursor-page-two",
    }));
    const canaryDetail = {
      ...detail,
      payload: "SECRET-PAYLOAD-CANARY",
      payload_hash: "HASH-CANARY",
      idempotency_key: "KEY-CANARY",
      lease_fencing_token: "LEASE-CANARY",
      error_summary: "ERROR-SUMMARY-CANARY",
      outbox_envelope: "OUTBOX-CANARY",
    } as JobDetail;
    vi.mocked(api.job).mockResolvedValue(canaryDetail);
    render(<JobRegistryView api={api} onUnauthorized={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByText(summary.jobKind)).toBeInTheDocument();
    expect(document.querySelector(`tr[data-row-key="${summary.jobId}"]`)).not.toBeNull();
    expect(screen.getAllByText(formatDateTime("2026-08-25T10:00:00Z")).length).toBeGreaterThan(0);
    fireEvent.change(screen.getByLabelText("任务类型"), { target: { value: "synthetic.noop" } });
    fireEvent.mouseDown(screen.getByLabelText("任务状态"));
    fireEvent.click(await screen.findByText("running", { selector: ".ant-select-item-option-content" }));
    fireEvent.change(screen.getByLabelText("创建时间起点"), { target: { value: "2026-08-25T09:00" } });
    fireEvent.change(screen.getByLabelText("创建时间终点"), { target: { value: "2026-08-25T11:00" } });
    fireEvent.mouseDown(screen.getByLabelText("每页任务数"));
    fireEvent.click(await screen.findByText("200 / 页", { selector: ".ant-select-item-option-content" }));
    await waitFor(() => expect(api.jobs).toHaveBeenLastCalledWith(expect.objectContaining({
      jobKind: "synthetic.noop", status: "running", createdFrom: expect.stringContaining("2026-08-25"),
      createdTo: expect.stringContaining("2026-08-25"), cursor: undefined, limit: 200,
    })));

    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.jobs).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: "cursor-page-two" })));
    fireEvent.click(screen.getByRole("button", { name: "上一页" }));
    await waitFor(() => expect(api.jobs).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: undefined })));

    fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
    const drawer = await screen.findByRole("dialog");
    expect(await within(drawer).findByText("#1 enqueued")).toBeInTheDocument();
    expect(within(drawer).getByText(summary.operationId)).toBeInTheDocument();
    for (const canary of ["SECRET-PAYLOAD-CANARY", "HASH-CANARY", "KEY-CANARY", "LEASE-CANARY", "ERROR-SUMMARY-CANARY", "OUTBOX-CANARY"]) {
      expect(document.body.textContent).not.toContain(canary);
    }
    expect(within(drawer).queryByRole("button", { name: /创建任务|重试任务|取消任务|删除任务/ })).not.toBeInTheDocument();
  });

  it("removes stale list data on 503 and recovers only after explicit retry", async () => {
    const api = makeApi();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><JobRegistryView api={api} onUnauthorized={vi.fn()} /></QueryClientProvider>);
    expect(await screen.findByText(summary.jobKind)).toBeInTheDocument();

    vi.mocked(api.jobs).mockRejectedValue(new JobApiError(503));
    await client.refetchQueries({ queryKey: ["durable-jobs", "list"] });
    const card = screen.getByTestId("jobs-card");
    expect(await within(card).findByText("读取失败")).toBeInTheDocument();
    expect(within(card).queryByText(summary.jobKind)).not.toBeInTheDocument();
    expect(api.jobs).toHaveBeenCalledTimes(2);

    vi.mocked(api.jobs).mockResolvedValue({ items: [{ ...summary, status: "succeeded" }], nextCursor: null });
    fireEvent.click(within(card).getByRole("button", { name: "重试读取" }));
    expect(await within(card).findByText("succeeded")).toBeInTheDocument();
    expect(api.jobs).toHaveBeenCalledTimes(3);
  });

  it("shows a bounded detail 404 and hands a list 401 to authentication", async () => {
    const api = makeApi();
    const onUnauthorized = vi.fn();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.mocked(api.job).mockRejectedValue(new JobApiError(404));
    render(<QueryClientProvider client={client}><JobRegistryView api={api} onUnauthorized={onUnauthorized} /></QueryClientProvider>);
    await screen.findByText(summary.jobKind);
    fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
    expect(await screen.findByText("任务不存在")).toBeInTheDocument();

    vi.mocked(api.jobs).mockRejectedValue(new JobApiError(401));
    await client.refetchQueries({ queryKey: ["durable-jobs", "list"] });
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
  });
});

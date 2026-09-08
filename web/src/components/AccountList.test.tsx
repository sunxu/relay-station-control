import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccountList, type AccountListRow } from "./AccountList";

const row = (overrides: Partial<AccountListRow> = {}): AccountListRow => ({ account_key: "openai:a@example.invalid", email: "a@example.invalid", provider: "openai", basic_status: "reported_active", lifecycle: "present", quality: "unknown", request_count: 0, success_count: 0, failure_count: 0, success_rate: null, p95_latency_ms: null, last_success_at: null, last_failure_at: null, last_failure_class: null, recent_requests: [], ...overrides });
const requests = (values: boolean[]) => values.map((success, i) => ({ occurred_at: `2026-09-08T00:0${i}:00Z`, model: "gpt", success, failure_class: success ? null : "auth", duration_ms: i, request_id: `r-${i}` }));

describe("AccountList", () => {
  it.each(["good", "degraded", "bad", "unknown"] as const)("renders quality %s", (quality) => { render(<AccountList rows={[row({ quality })]} />); expect(screen.getByText(quality.charAt(0).toUpperCase() + quality.slice(1))).toBeInTheDocument(); });
  it("renders success and failed outcomes", () => { render(<AccountList rows={[row({ recent_requests: requests([true, false]) })]} />); expect(screen.getByLabelText("Success")).toBeInTheDocument(); expect(screen.getByLabelText("Failed auth")).toBeInTheDocument(); });
  it("caps outcomes at ten", () => { render(<AccountList rows={[row({ recent_requests: requests(Array(12).fill(true)) })]} />); expect(screen.getAllByLabelText("Success")).toHaveLength(10); });
  it("orders newest API results oldest to newest", () => { render(<AccountList rows={[row({ recent_requests: requests([true, false]) })]} />); const values = screen.getAllByTestId("recent-request-strip")[0]!.querySelectorAll(".ant-tag"); expect([...values].map((v) => v.getAttribute("aria-label"))).toEqual(["Failed auth", "Success"]); });
  it("shows no request for empty array", () => { render(<AccountList rows={[row()]} />); expect(screen.getByText("无请求")).toBeInTheDocument(); });
  it("shows empty state", () => { render(<AccountList rows={[]} />); expect(screen.getByText("当前过滤条件下没有账号")).toBeInTheDocument(); });
  it("shows loading state", () => { render(<AccountList rows={[]} loading />); expect(screen.getByRole("status")).toBeInTheDocument(); });
  it("shows unavailable state and retry", () => { const retry = vi.fn(); render(<AccountList rows={[]} unavailable onRetry={retry} />); expect(screen.getByText("读取不可用（unavailable）")).toBeInTheDocument(); fireEvent.click(screen.getByRole("button", { name: /重\s*试/ })); expect(retry).toHaveBeenCalled(); });
  it("uses account key as row identity", () => { render(<AccountList rows={[row({ account_key: "openai:a@example.invalid" })]} />); expect(screen.getByText("openai:a@example.invalid")).toBeInTheDocument(); });
  it("exposes detail callback", () => { const select = vi.fn(); render(<AccountList rows={[row()]} onSelectAccount={select} />); screen.getByText("查看详情").click(); expect(select).toHaveBeenCalledWith(expect.objectContaining({ account_key: row().account_key })); });
});

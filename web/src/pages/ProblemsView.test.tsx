import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ProblemAccountItem, ProblemAccountResponse, ProblemAccountsApi } from "../api/problem-accounts-types";
import { ProblemAccountsApiError } from "../api/problem-accounts-types";
import { ProblemsView } from "./ProblemsView";

const issue = (type: ProblemAccountItem["issues"][number]["type"], reason: ProblemAccountItem["issues"][number]["reason"], severity: ProblemAccountItem["issues"][number]["severity"], id: string) => ({
  occurrence_id: id, type, reason, severity, since: "2026-09-10T01:02:03Z",
});

function row(instance_id: string, account_key: string, email: string, issues: ProblemAccountItem["issues"]): ProblemAccountItem {
  return {
    instance_id, node_name: instance_id.endsWith("1") ? "Node One" : "Node Two", account_key, email, provider: "antigravity", issues,
    availability: null, token_state: "UNKNOWN", last_refresh_at: null, expected_valid_until: null, next_retry_at: null,
    last_success_at: null, last_failure_at: null, highest_severity: "Critical", oldest_active_since: "2026-09-10T01:02:03Z",
  };
}

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>{children}</QueryClientProvider>;
}

function makeApi(response: ProblemAccountResponse): ProblemAccountsApi {
  return { query: vi.fn().mockResolvedValue(response) };
}

describe("ProblemsView", () => {
  it("auto-queries the first page and keeps one row per node/account pair", async () => {
    const api = makeApi({ items: [
      row("00000000-0000-4000-8000-000000000001", "same", "same@example.invalid", [issue("TOKEN_INVALID", "token_invalid", "Critical", "occ-1")]),
      row("00000000-0000-4000-8000-000000000002", "same", "same@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ-2")]),
    ], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText("Node One")).toBeInTheDocument();
    expect(screen.getByText("Node Two")).toBeInTheDocument();
    expect(api.query).toHaveBeenCalledWith({ limit: 25 }, "csrf", expect.any(AbortSignal));
  });

  it("renders all issues in a single row and retains rows with missing diagnostics", async () => {
    const item = { ...row("00000000-0000-4000-8000-000000000001", "account", "account@example.invalid", [
      issue("TOKEN_INVALID", "token_invalid", "Critical", "occ-1"), issue("ACCOUNT_BLOCKED", "account_blocked", "Critical", "occ-2"),
      issue("FORBIDDEN", "forbidden", "Warning", "occ-3"), issue("CROSS_NODE_DUPLICATE_OWNERSHIP", "cross_node_duplicate_ownership", "Critical", "occ-4"),
    ]), node_name: undefined, token_state: undefined, expected_valid_until: undefined, last_refresh_at: undefined, next_retry_at: undefined, last_success_at: undefined, last_failure_at: undefined } as unknown as ProblemAccountItem;
    const api = makeApi({ items: [item], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText("CROSS_NODE_DUPLICATE_OWNERSHIP")).toBeInTheDocument();
    expect(screen.getAllByText("TOKEN_INVALID").length).toBeGreaterThan(0);
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText("Expected Valid Until")).toBeInTheDocument();
  });

  it("uses opaque cursor history for next and previous", async () => {
    const api: ProblemAccountsApi = { query: vi.fn()
      .mockResolvedValueOnce({ items: [row("00000000-0000-4000-8000-000000000001", "a", "a@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")])], next_cursor: "opaque-2" })
      .mockResolvedValue({ items: [], next_cursor: null }) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("a@example.invalid");
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 25, cursor: "opaque-2" }, "csrf", expect.any(AbortSignal)));
    fireEvent.click(screen.getByRole("button", { name: "上一页" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 25 }, "csrf", expect.any(AbortSignal)));
  });

  it("does not expose mutation controls", async () => {
    const api = makeApi({ items: [], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("当前没有 confirmed problems");
    expect(screen.queryByText(/Ack|Resolve|Repair|Move|Reassign|Refresh Token|Upload Credential|Actions|Operations|Manage/)).not.toBeInTheDocument();
  });

  it("normalizes email and trims node before querying", async () => {
    const api = makeApi({ items: [], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("当前没有 confirmed problems");
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "  User@Example.INVALID " } });
    fireEvent.change(screen.getByLabelText("Node UUID"), { target: { value: "  node-id  " } });
    await waitFor(() => expect(api.query).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ email: "user@example.invalid", node: "node-id", limit: 25 }, "csrf", expect.any(AbortSignal)));
  });

  it.each([["VALID"], ["INVALID"], ["UNKNOWN"]] as const)("renders token state %s without reclassification", async (token_state) => {
    const item = { ...row("00000000-0000-4000-8000-000000000001", "account", "account@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")]), token_state };
    const api = makeApi({ items: [item], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText(token_state)).toBeInTheDocument();
  });

  it("shows the prescribed status messages and retries the same cursor", async () => {
    const api: ProblemAccountsApi = { query: vi.fn()
      .mockRejectedValueOnce(new ProblemAccountsApiError(503, { code: "temporarily_unavailable", message: "busy", request_id: "request-1" }))
      .mockResolvedValueOnce({ items: [], next_cursor: "opaque-next" })
      .mockRejectedValueOnce(new ProblemAccountsApiError(403, { code: "forbidden", message: "no", request_id: "request-2" })) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText("读取不可用（unavailable）")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 25 }, "csrf", expect.any(AbortSignal)));
    await waitFor(() => expect(api.query).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    expect(await screen.findByText("当前会话无权读取 Problems。")).toBeInTheDocument();
  });

  it("clears stale rows and session on 401", async () => {
    const onUnauthorized = vi.fn();
    let failedSignal: AbortSignal | undefined;
    const api: ProblemAccountsApi = { query: vi.fn()
      .mockResolvedValueOnce({ items: [row("00000000-0000-4000-8000-000000000001", "account", "old@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")])], next_cursor: null })
      .mockImplementationOnce(async (_request, _csrf, signal) => { failedSignal = signal; throw new ProblemAccountsApiError(401, { code: "unauthorized", message: "expired", request_id: "request-1" }); }) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={onUnauthorized} />, { wrapper });
    expect(await screen.findByText("old@example.invalid")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "old@example.invalid" } });
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
    expect(failedSignal?.aborted).toBe(true);
    expect(screen.queryByText("old@example.invalid")).not.toBeInTheDocument();
    expect(screen.queryByText("occ")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toHaveValue("");
  });

  it("uses the real select controls for provider, severity, every reason, and clearable values", async () => {
    const api = makeApi({ items: [], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("当前没有 confirmed problems");
    const choose = async (label: string, option: string) => {
      fireEvent.mouseDown(screen.getByLabelText(label));
      fireEvent.click(await screen.findByText(option, { selector: ".ant-select-item-option-content" }));
    };
    await choose("Provider", "antigravity");
    await choose("Severity", "Critical");
    for (const reason of ["TOKEN_INVALID", "ACCOUNT_BLOCKED", "FORBIDDEN", "CROSS_NODE_DUPLICATE_OWNERSHIP"]) await choose("Reason", reason);
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ provider: "antigravity", severity: "Critical", reason: "cross_node_duplicate_ownership", limit: 25 }, "csrf", expect.any(AbortSignal)));
    const provider = screen.getByLabelText("Provider").closest(".ant-select");
    const clear = provider?.querySelector(".ant-select-clear");
    expect(clear).toBeTruthy();
    fireEvent.click(clear!);
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ severity: "Critical", reason: "cross_node_duplicate_ownership", limit: 25 }, "csrf", expect.any(AbortSignal)));
  });

  it("resets cursor and clears the result when a filter changes", async () => {
    const api: ProblemAccountsApi = { query: vi.fn()
      .mockResolvedValueOnce({ items: [row("00000000-0000-4000-8000-000000000001", "a", "a@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")])], next_cursor: "cursor-2" })
      .mockResolvedValue({ items: [], next_cursor: null }) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("a@example.invalid");
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 25, cursor: "cursor-2" }, "csrf", expect.any(AbortSignal)));
    expect(screen.getByRole("button", { name: "上一页" })).toBeEnabled();
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "new@example.invalid" } });
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.queryByText("a@example.invalid")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ email: "new@example.invalid", limit: 25 }, "csrf", expect.any(AbortSignal)));
  });

  it("queries page size changes from the first cursor", async () => {
    const item = row("00000000-0000-4000-8000-000000000001", "a", "a@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")]);
    const api: ProblemAccountsApi = { query: vi.fn()
      .mockResolvedValueOnce({ items: [item], next_cursor: "cursor-2" })
      .mockResolvedValueOnce({ items: [item], next_cursor: "cursor-3" })
      .mockResolvedValue({ items: [], next_cursor: null }) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("a@example.invalid");
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 25, cursor: "cursor-2" }, "csrf", expect.any(AbortSignal)));
    fireEvent.mouseDown(screen.getByLabelText("Page size"));
    fireEvent.click(await screen.findByText("50 / 页", { selector: ".ant-select-item-option-content" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 50 }, "csrf", expect.any(AbortSignal)));
    fireEvent.mouseDown(screen.getByLabelText("Page size"));
    fireEvent.click(await screen.findByText("100 / 页", { selector: ".ant-select-item-option-content" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ limit: 100 }, "csrf", expect.any(AbortSignal)));
  });

  it("preserves backend item and issue ordering and exact pair row keys", async () => {
    const first = row("00000000-0000-4000-8000-000000000002", "same", "z@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "z-occ"), issue("TOKEN_INVALID", "token_invalid", "Critical", "a-occ")]);
    const second = row("00000000-0000-4000-8000-000000000001", "same", "a@example.invalid", [issue("ACCOUNT_BLOCKED", "account_blocked", "Critical", "b-occ")]);
    const api = makeApi({ items: [first, second], next_cursor: null });
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("z@example.invalid");
    const rows = screen.getAllByRole("row");
    expect(rows[1]).toHaveAttribute("data-row-key", "00000000-0000-4000-8000-000000000002:same");
    expect(rows[2]).toHaveAttribute("data-row-key", "00000000-0000-4000-8000-000000000001:same");
    const text = document.body.textContent ?? "";
    expect(text.indexOf("z-occ")).toBeLessThan(text.indexOf("a-occ"));
  });

  it("disables all controls and announces pending reads", async () => {
    let resolve!: (response: ProblemAccountResponse) => void;
    const api: ProblemAccountsApi = { query: vi.fn().mockReturnValue(new Promise<ProblemAccountResponse>((done) => { resolve = done; })) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByRole("status", { name: "正在读取 Problems" })).toBeInTheDocument();
    expect(screen.getByLabelText("Provider")).toBeDisabled();
    expect(screen.getByLabelText("Node UUID")).toBeDisabled();
    expect(screen.getByLabelText("Severity")).toBeDisabled();
    expect(screen.getByLabelText("Reason")).toBeDisabled();
    expect(screen.getByLabelText("Email")).toBeDisabled();
    expect(screen.getByLabelText("Page size").closest(".ant-select")).toHaveClass("ant-select-disabled");
    expect(screen.getByRole("button", { name: /Query/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    resolve({ items: [], next_cursor: null });
  });

  it("renders the dedicated 400 validation message", async () => {
    const api: ProblemAccountsApi = { query: vi.fn().mockRejectedValue(new ProblemAccountsApiError(400, { code: "validation_failed", message: "invalid", request_id: "request-400" })) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText("筛选条件或分页凭据无效，请清除筛选并重新查询。")).toBeInTheDocument();
    expect(screen.queryByText("当前没有 confirmed problems")).not.toBeInTheDocument();
  });

  it("retries a 503 with the current email and opaque cursor", async () => {
    const api: ProblemAccountsApi = { query: vi.fn()
      .mockResolvedValueOnce({ items: [row("00000000-0000-4000-8000-000000000001", "a", "a@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")])], next_cursor: "cursor-2" })
      .mockResolvedValueOnce({ items: [row("00000000-0000-4000-8000-000000000001", "a", "a@example.invalid", [issue("FORBIDDEN", "forbidden", "Warning", "occ")])], next_cursor: "cursor-2" })
      .mockRejectedValueOnce(new ProblemAccountsApiError(503, { code: "temporarily_unavailable", message: "busy", request_id: "request-503" }))
      .mockResolvedValueOnce({ items: [], next_cursor: null }) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    await screen.findByText("a@example.invalid");
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: " a@example.invalid " } });
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ email: "a@example.invalid", limit: 25 }, "csrf", expect.any(AbortSignal)));
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(screen.getByText("读取不可用（unavailable）")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(api.query).toHaveBeenLastCalledWith({ email: "a@example.invalid", limit: 25, cursor: "cursor-2" }, "csrf", expect.any(AbortSignal)));
  });

  it("shows unavailable for network errors and both empty-state variants", async () => {
    const api: ProblemAccountsApi = { query: vi.fn().mockRejectedValue(new Error("network")) };
    render(<ProblemsView api={api} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText("读取不可用（unavailable）")).toBeInTheDocument();
    const emptyApi = makeApi({ items: [], next_cursor: null });
    cleanup();
    render(<ProblemsView api={emptyApi} csrfToken="csrf" onUnauthorized={vi.fn()} />, { wrapper });
    expect(await screen.findByText("当前没有 confirmed problems")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "filter@example.invalid" } });
    fireEvent.click(screen.getByRole("button", { name: /Query/ }));
    expect(await screen.findByText("当前过滤条件下没有 confirmed problems")).toBeInTheDocument();
  });
});

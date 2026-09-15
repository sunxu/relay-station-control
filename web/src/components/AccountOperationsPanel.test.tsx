import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AccountOperationsApi } from "../api/account-operations-api";
import { AccountOperationApiError, accountOperationErrorMessage } from "../api/account-operations-api";
import type { AccountOperationProjection } from "../api/generated/control";
import { AccountOperationResult, AccountOperationsPanel } from "./AccountOperationsPanel";

function operation(state: AccountOperationProjection["execution_state"], errorCode: string | null = null): AccountOperationProjection {
  return {
    command_id: "11111111-1111-4111-8111-111111111111",
    node_instance_id: "22222222-2222-4222-8222-222222222222",
    account_key: "antigravity:user@example.invalid",
    operation_kind: "disable",
    execution_state: state,
    result: state === "remote_applied" ? "applied" : state === "remote_noop" ? "noop" : state === "failed" ? "failed" : null,
    error_code: errorCode,
    lifecycle_overridden: false,
    same_account_overridden: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:01Z",
  };
}

function mockApi(overrides: Partial<AccountOperationsApi> = {}): AccountOperationsApi {
  return {
    mutate: vi.fn().mockResolvedValue(operation("remote_applied")),
    operation: vi.fn().mockResolvedValue(operation("outcome_unknown", "remote_outcome_unknown")),
    override: vi.fn().mockResolvedValue({ ...operation("outcome_unknown"), same_account_overridden: true }),
    ...overrides,
  };
}

describe("AccountOperationsPanel", () => {
  it.each([
    ["unsupported_provider", "该 Provider 不支持账户操作"],
    ["node_retired", "Node 已退役"],
    ["node_monitoring_ineligible", "Node 当前不符合监控条件"],
    ["account_target_not_found", "未找到目标账户"],
    ["account_target_ambiguous", "目标账户不唯一"],
    ["account_operation_in_progress", "该账户已有未决操作"],
    ["unsupported_node_version", "Node 运行版本不受支持"],
    ["node_management_unavailable", "Node 管理接口不可用"],
    ["service_unavailable", "账户操作服务不可用"],
  ])("maps structured error %s", (code, expected) => {
    expect(accountOperationErrorMessage(new AccountOperationApiError(409, { code: code as never, message: "ignored", request_id: "request-id" }))).toBe(expected);
  });

  it.each([
    ["remote_applied", "已应用"],
    ["remote_noop", "无需变更"],
    ["failed", "执行失败"],
  ] as const)("presents %s distinctly", (state, label) => {
    render(<AccountOperationResult operation={operation(state)} />);
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("presents outcome_unknown without success, failure, retry, or verification claims", () => {
    render(<AccountOperationResult operation={operation("outcome_unknown", "remote_outcome_unknown")} />);
    expect(screen.getByText("远端结果不确定")).toBeInTheDocument();
    expect(screen.getByText(/系统不会自动重试/)).toBeInTheDocument();
    expect(screen.queryByText("已应用")).not.toBeInTheDocument();
    expect(screen.queryByText("执行失败")).not.toBeInTheDocument();
    expect(screen.queryByText(/正在重试|正在验证/)).not.toBeInTheDocument();
  });

  it("submits one mutation while a request is active", async () => {
    let resolve!: (value: AccountOperationProjection) => void;
    const mutate = vi.fn(() => new Promise<AccountOperationProjection>((done) => { resolve = done; }));
    render(<AccountOperationsPanel api={mockApi({ mutate })} csrf="csrf" nodeInstanceId="node" accountKey="antigravity:user@example.invalid" />);
    const disable = screen.getByRole("button", { name: "Disable" });
    fireEvent.click(disable);
    fireEvent.click(disable);
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(disable).toBeDisabled();
    resolve(operation("remote_applied"));
    await waitFor(() => expect(disable).not.toBeDisabled());
  });

  it("submits enable and requires destructive confirmation for remove", async () => {
    const api = mockApi();
    render(<AccountOperationsPanel api={api} csrf="csrf" nodeInstanceId="node" accountKey="antigravity:user@example.invalid" basicStatus="disabled" />);
    fireEvent.click(screen.getByRole("button", { name: "Enable" }));
    await waitFor(() => expect(api.mutate).toHaveBeenCalledWith("enable", expect.anything(), "csrf"));
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect((await screen.findAllByText("确认移除此账户？")).length).toBeGreaterThan(0);
    expect(api.mutate).not.toHaveBeenCalledWith("remove", expect.anything(), "csrf");
  });

  it("uploads a file without rendering its credential content", async () => {
    const api = mockApi();
    render(<AccountOperationsPanel api={api} csrf="csrf" nodeInstanceId="node" accountKey="antigravity:user@example.invalid" />);
    const secret = '{"type":"antigravity","refresh_token":"never-render-this"}';
    const file = new File([secret], "credential.json", { type: "application/json" });
    fireEvent.change(document.querySelector("input[type=file]")!, { target: { files: [file] } });
    fireEvent.click(await screen.findByRole("button", { name: "Upload New" }));
    await waitFor(() => expect(api.mutate).toHaveBeenCalledTimes(1));
    expect(api.mutate).toHaveBeenCalledWith("upload_new", expect.objectContaining({ credential: file }), "csrf");
    expect(screen.queryByText(/never-render-this/)).not.toBeInTheDocument();
  });

  it("loads an operation and exposes only closed override reasons", async () => {
    const api = mockApi();
    render(<AccountOperationsPanel api={api} csrf="csrf" nodeInstanceId="node" accountKey="antigravity:user@example.invalid" />);
    fireEvent.change(screen.getByLabelText("Operation command ID"), { target: { value: "11111111-1111-4111-8111-111111111111" } });
    fireEvent.click(screen.getByRole("button", { name: "读取操作" }));
    await screen.findByText("远端结果不确定");
    fireEvent.mouseDown(screen.getByLabelText("Override reason"));
    expect((await screen.findAllByText("Control 进程已重启")).length).toBeGreaterThan(0);
    expect(screen.getByText("Node 已停止")).toBeInTheDocument();
    expect(screen.getByText("已人工接受风险")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Override reason" })).not.toBeInTheDocument();
  });
});

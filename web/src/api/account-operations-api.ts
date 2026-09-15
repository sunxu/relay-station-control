import {
  disableAccountOperation,
  enableAccountOperation,
  getAccountOperation,
  lifecycleOverrideAccountOperation,
  removeAccountOperation,
  replaceExistingAccountOperation,
  sameAccountOverrideAccountOperation,
  uploadNewAccountOperation,
} from "./generated/control";
import type {
  AccountOperationProjection,
  ErrorResponse,
  OverrideReason,
} from "./generated/control";

export type AccountMutationKind = "disable" | "enable" | "remove" | "upload_new" | "replace_existing";

export class AccountOperationApiError extends Error {
  constructor(readonly status: number, readonly detail: ErrorResponse) {
    super(detail.message);
    this.name = "AccountOperationApiError";
  }
}

export interface AccountOperationsApi {
  mutate(kind: AccountMutationKind, input: { commandId: string; nodeInstanceId: string; accountKey: string; credential?: File }, csrf: string): Promise<AccountOperationProjection>;
  operation(commandId: string): Promise<AccountOperationProjection>;
  override(kind: "lifecycle" | "same_account", input: { commandId: string; targetCommandId: string; reason: OverrideReason; detail?: string }, csrf: string): Promise<AccountOperationProjection>;
}

type GeneratedResponse = { status: number; data: unknown };

function errorResponse(value: unknown): ErrorResponse | undefined {
  if (!value || typeof value !== "object") return undefined;
  const record = value as Record<string, unknown>;
  const candidate = record.error && typeof record.error === "object" ? record.error as Record<string, unknown> : record;
  return typeof candidate.code === "string" && typeof candidate.message === "string" && typeof candidate.request_id === "string"
    ? candidate as unknown as ErrorResponse
    : undefined;
}

function unwrap(response: GeneratedResponse): AccountOperationProjection {
  const data = response.data as { operation?: AccountOperationProjection } | undefined;
  if (data?.operation) return data.operation;
  const detail = errorResponse(response.data) ?? { code: "internal_error", message: "请求未完成", request_id: "client-unknown" };
  throw new AccountOperationApiError(response.status, detail);
}

const options = (csrf?: string): RequestInit => ({
  cache: "no-store",
  credentials: "same-origin",
  headers: csrf ? { "X-CSRF-Token": csrf } : undefined,
});

export const generatedAccountOperationsApi: AccountOperationsApi = {
  async mutate(kind, input, csrf) {
    const request = { command_id: input.commandId, node_instance_id: input.nodeInstanceId, account_key: input.accountKey };
    if (kind === "disable") return unwrap(await disableAccountOperation(request, options(csrf)));
    if (kind === "enable") return unwrap(await enableAccountOperation(request, options(csrf)));
    if (kind === "remove") return unwrap(await removeAccountOperation({ ...request, confirmation: "REMOVE" }, options(csrf)));
    if (!input.credential) throw new AccountOperationApiError(400, { code: "validation_failed", message: "请选择凭据文件", request_id: "client-validation" });
    const upload = { request: new Blob([JSON.stringify(request)], { type: "application/json" }), credential: input.credential };
    return unwrap(kind === "upload_new"
      ? await uploadNewAccountOperation(upload, options(csrf))
      : await replaceExistingAccountOperation(upload, options(csrf)));
  },
  async operation(commandId) {
    return unwrap(await getAccountOperation(commandId, options()));
  },
  async override(kind, input, csrf) {
    const common = { command_id: input.commandId, reason: input.reason, detail: input.detail || undefined };
    return unwrap(kind === "lifecycle"
      ? await lifecycleOverrideAccountOperation(input.targetCommandId, { ...common, confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK" }, options(csrf))
      : await sameAccountOverrideAccountOperation(input.targetCommandId, { ...common, confirmation: "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK" }, options(csrf)));
  },
};

const errorLabels: Record<string, string> = {
  unsupported_provider: "该 Provider 不支持账户操作",
  node_retired: "Node 已退役",
  node_monitoring_ineligible: "Node 当前不符合监控条件",
  account_target_not_found: "未找到目标账户",
  account_target_ambiguous: "目标账户不唯一",
  account_operation_in_progress: "该账户已有未决操作",
  unsupported_node_version: "Node 运行版本不受支持",
  node_management_unavailable: "Node 管理接口不可用",
  service_unavailable: "账户操作服务不可用",
};

export function accountOperationErrorMessage(error: unknown): string {
  if (!(error instanceof AccountOperationApiError)) return "请求未完成，请检查当前状态后再决定是否发起新命令。";
  return errorLabels[error.detail.code] ?? `请求未完成（${error.detail.code}，请求 ID：${error.detail.request_id}）`;
}

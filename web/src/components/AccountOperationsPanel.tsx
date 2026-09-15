import { Alert, Button, Descriptions, Divider, Flex, Input, Modal, Select, Space, Tag, Typography, Upload } from "antd";
import type { UploadFile } from "antd";
import { useRef, useState } from "react";
import type { AccountOperationProjection, OverrideReason } from "../api/generated/control";
import type { AccountMutationKind, AccountOperationsApi } from "../api/account-operations-api";
import { AccountOperationApiError, accountOperationErrorMessage } from "../api/account-operations-api";
import { formatDateTime } from "../time";

const maxCredentialBytes = 1024 * 1024;
const reasons: Array<{ value: OverrideReason; label: string }> = [
  { value: "process_restarted", label: "Control 进程已重启" },
  { value: "node_stopped", label: "Node 已停止" },
  { value: "risk_accepted", label: "已人工接受风险" },
];

function statePresentation(operation: AccountOperationProjection) {
  if (operation.execution_state === "remote_applied") return { color: "green", label: "已应用" };
  if (operation.execution_state === "remote_noop") return { color: "blue", label: "无需变更" };
  if (operation.execution_state === "outcome_unknown") return { color: "orange", label: "远端结果不确定" };
  if (operation.execution_state === "failed") return { color: "red", label: "执行失败" };
  return { color: "default", label: operation.execution_state };
}

export function AccountOperationResult({ operation }: { operation: AccountOperationProjection }) {
  const state = statePresentation(operation);
  return <Flex vertical gap={10} data-testid="account-operation-result">
    {operation.execution_state === "outcome_unknown" && <Alert type="warning" showIcon title="执行结果未知" description="请求可能已被远端应用，也可能未应用。系统不会自动重试，以避免重复操作。请结合当前账户状态人工判断后再决定是否发起新的命令。" />}
    <Descriptions size="small" bordered column={1}>
      <Descriptions.Item label="Command ID"><Typography.Text copyable>{operation.command_id}</Typography.Text></Descriptions.Item>
      <Descriptions.Item label="操作">{operation.operation_kind}</Descriptions.Item>
      <Descriptions.Item label="执行状态"><Tag color={state.color}>{state.label}</Tag></Descriptions.Item>
      <Descriptions.Item label="结果/错误">{operation.error_code ?? operation.result ?? "—"}</Descriptions.Item>
      <Descriptions.Item label="创建时间">{formatDateTime(operation.created_at)}</Descriptions.Item>
      <Descriptions.Item label="更新时间">{formatDateTime(operation.updated_at)}</Descriptions.Item>
    </Descriptions>
  </Flex>;
}

export function AccountOperationsPanel({ api, csrf, nodeInstanceId, accountKey, basicStatus, onUnauthorized }: { api: AccountOperationsApi; csrf: string; nodeInstanceId: string; accountKey: string; basicStatus?: string; onUnauthorized?: () => void }) {
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [credential, setCredential] = useState<File>();
  const [credentialInputKey, setCredentialInputKey] = useState(0);
  const [operation, setOperation] = useState<AccountOperationProjection>();
  const [lookupID, setLookupID] = useState("");
  const [error, setError] = useState("");
  const [overrideKind, setOverrideKind] = useState<"lifecycle" | "same_account">("lifecycle");
  const [overrideReason, setOverrideReason] = useState<OverrideReason>("process_restarted");
  const [overrideDetail, setOverrideDetail] = useState("");

  const once = async (work: () => Promise<AccountOperationProjection>) => {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      const next = await work();
      setOperation(next);
      setLookupID(next.command_id);
    } catch (cause) {
      if (cause instanceof AccountOperationApiError && cause.status === 401) onUnauthorized?.();
      setError(accountOperationErrorMessage(cause));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  const clearCredential = () => {
    setCredential(undefined);
    setCredentialInputKey((value) => value + 1);
  };
  const mutate = async (kind: AccountMutationKind) => {
    try {
      await once(() => api.mutate(kind, { commandId: crypto.randomUUID(), nodeInstanceId, accountKey, credential }, csrf));
    } finally {
      if (kind === "upload_new" || kind === "replace_existing") clearCredential();
    }
  };
  const confirmRemove = () => Modal.confirm({ title: "确认移除此账户？", content: "该操作会删除 Node 上对应的单个凭据文件，无法由 Control 自动恢复。", okText: "确认移除", okButtonProps: { danger: true }, onOk: () => mutate("remove") });
  const applyOverride = () => {
    if (!operation) return Promise.resolve();
    return once(() => api.override(overrideKind, { commandId: crypto.randomUUID(), targetCommandId: operation.command_id, reason: overrideReason, detail: overrideDetail.trim() || undefined }, csrf));
  };

  return <Flex vertical gap={12}>
    <Typography.Text type="secondary">操作只提交逻辑账号身份；物理目标由服务端基于最新 Node 快照选择。</Typography.Text>
    {error && <Alert type="error" showIcon title={error} />}
    <Space wrap>
      {basicStatus !== "disabled" && <Button disabled={busy} loading={busy} onClick={() => void mutate("disable")}>Disable</Button>}
      {basicStatus === "disabled" && <Button disabled={busy} loading={busy} onClick={() => void mutate("enable")}>Enable</Button>}
      <Button danger disabled={busy} onClick={confirmRemove}>Remove</Button>
    </Space>
    <Upload key={credentialInputKey} beforeUpload={(file) => { if (file.size > maxCredentialBytes) { setError("凭据文件不能超过 1 MiB"); return Upload.LIST_IGNORE; } setCredential(file); setError(""); return false; }} fileList={credential ? [{ uid: "credential", name: credential.name, status: "done" } as UploadFile] : []} onRemove={() => { clearCredential(); return true; }} maxCount={1} accept="application/json,.json">
      <Button disabled={busy}>选择 credential JSON（最大 1 MiB）</Button>
    </Upload>
    <Space wrap>
      <Button type="primary" disabled={busy || !credential} loading={busy} onClick={() => void mutate("upload_new")}>Upload New</Button>
      <Button danger disabled={busy || !credential} loading={busy} onClick={() => Modal.confirm({ title: "确认替换现有凭据？", content: "Replace 使用远端原生的尽力而为、后写入覆盖语义。", okText: "确认替换", onOk: () => mutate("replace_existing") })}>Replace Existing</Button>
    </Space>
    <Divider />
    <Flex gap={8} wrap>
      <Input aria-label="Operation command ID" value={lookupID} onChange={(event) => setLookupID(event.target.value)} placeholder="输入 Command ID 查看操作结果" style={{ maxWidth: 390 }} />
      <Button disabled={busy || !lookupID.trim()} loading={busy} onClick={() => void once(() => api.operation(lookupID.trim()))}>读取操作</Button>
    </Flex>
    {operation && <AccountOperationResult operation={operation} />}
    {operation?.execution_state === "outcome_unknown" && <>
      <Divider />
      <Typography.Text strong>未知结果 Override</Typography.Text>
      <Select aria-label="Override 类型" value={overrideKind} onChange={setOverrideKind} options={[{ value: "lifecycle", label: "Lifecycle Override" }, { value: "same_account", label: "Same-account Override" }]} />
      <Select aria-label="Override reason" value={overrideReason} onChange={setOverrideReason} options={reasons} />
      <Input.TextArea aria-label="Override detail" value={overrideDetail} maxLength={512} onChange={(event) => setOverrideDetail(event.target.value)} placeholder="可选说明" />
      <Button danger disabled={busy} loading={busy} onClick={() => Modal.confirm({ title: `确认执行 ${overrideKind === "lifecycle" ? "Lifecycle" : "Same-account"} Override？`, content: `目标操作：${operation.command_id}`, okText: "确认 Override", onOk: applyOverride })}>执行 Override</Button>
    </>}
  </Flex>;
}

export function UploadNewAccountAction({ api, csrf, nodeInstanceId, onUnauthorized }: { api: AccountOperationsApi; csrf: string; nodeInstanceId: string; onUnauthorized?: () => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [email, setEmail] = useState("");
  const [credential, setCredential] = useState<File>();
  const [credentialInputKey, setCredentialInputKey] = useState(0);
  const [operation, setOperation] = useState<AccountOperationProjection>();
  const [error, setError] = useState("");

  const clearCredential = () => {
    setCredential(undefined);
    setCredentialInputKey((value) => value + 1);
  };
  const close = () => {
    if (busyRef.current) return;
    clearCredential();
    setEmail("");
    setError("");
    setOpen(false);
  };
  const submit = async () => {
    if (busyRef.current || !credential || !email.trim()) return;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      const next = await api.mutate("upload_new", {
        commandId: crypto.randomUUID(),
        nodeInstanceId,
        accountKey: `antigravity:${email.trim().toLowerCase()}`,
        credential,
      }, csrf);
      setOperation(next);
      setEmail("");
      setOpen(false);
    } catch (cause) {
      if (cause instanceof AccountOperationApiError && cause.status === 401) onUnauthorized?.();
      setError(accountOperationErrorMessage(cause));
    } finally {
      clearCredential();
      busyRef.current = false;
      setBusy(false);
    }
  };

  return <Flex vertical gap={12} align="flex-start">
    <Button data-testid="account-upload-new" type="primary" onClick={() => { setError(""); setOpen(true); }}>Upload New Account</Button>
    {operation && <AccountOperationResult operation={operation} />}
    <Modal title="Upload New Account" open={open} onCancel={close} onOk={() => void submit()} okText="Upload New" confirmLoading={busy} okButtonProps={{ "data-testid": "account-upload-new-submit", disabled: busy || !credential || !email.trim() }} cancelButtonProps={{ disabled: busy }} destroyOnHidden>
      <Flex vertical gap={12}>
        <Typography.Text type="secondary">无需已有 Inventory 账号。服务端会根据逻辑身份生成并验证远端目标。</Typography.Text>
        {error && <Alert type="error" showIcon title={error} />}
        <Input aria-label="Upload New Provider" value="antigravity" disabled />
        <Input data-testid="account-upload-new-email" aria-label="Upload New Email" type="email" autoComplete="off" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="账户邮箱" />
        <Upload data-testid="account-upload-new-file" key={credentialInputKey} beforeUpload={(file) => { if (file.size > maxCredentialBytes) { setError("凭据文件不能超过 1 MiB"); return Upload.LIST_IGNORE; } setCredential(file); setError(""); return false; }} fileList={credential ? [{ uid: "new-account-credential", name: credential.name, status: "done" } as UploadFile] : []} onRemove={() => { clearCredential(); return true; }} maxCount={1} accept="application/json,.json">
          <Button data-testid="account-upload-new-file-button" disabled={busy}>选择 credential JSON（最大 1 MiB）</Button>
        </Upload>
      </Flex>
    </Modal>
  </Flex>;
}

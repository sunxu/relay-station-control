import { Alert, Button, Descriptions, Divider, Flex, Input, Modal, Select, Space, Tag, Typography, Upload } from "antd";
import type { UploadFile } from "antd";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { AccountOperationProjection, OverrideReason } from "../api/generated/control";
import type { AccountMutationKind, AccountOperationsApi } from "../api/account-operations-api";
import { AccountOperationApiError, accountOperationErrorMessage } from "../api/account-operations-api";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";

const maxCredentialBytes = 1024 * 1024;
function statePresentation(operation: AccountOperationProjection, t: (key: "operations.applied" | "operations.noop" | "operations.unknown" | "operations.failed") => string) {
  if (operation.execution_state === "remote_applied") return { color: "green", label: t("operations.applied") };
  if (operation.execution_state === "remote_noop") return { color: "blue", label: t("operations.noop") };
  if (operation.execution_state === "outcome_unknown") return { color: "orange", label: t("operations.unknown") };
  if (operation.execution_state === "failed") return { color: "red", label: t("operations.failed") };
  return { color: "default", label: operation.execution_state };
}

function fallbackOperationCopy(locale: "zh-CN" | "en", key: string, options?: Record<string, unknown>) {
  const leaf = key.slice("operations.".length) as keyof typeof resources["zh-CN"]["translation"]["operations"];
  const value = resources[locale].translation.operations[leaf];
  if (typeof value !== "string") return key;
  return value.replace(/{{(\w+)}}/g, (_, name: string) => String(options?.[name] ?? `{{${name}}}`));
}

export function AccountOperationResult({ operation }: { operation: AccountOperationProjection }) {
  const { t: translate } = useTranslation();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const t = (key: string, options?: Record<string, unknown>) => { const fallback = fallbackOperationCopy(locale, key, options); const value = translate(key, { ...options, defaultValue: fallback }); return value === key ? fallback : value; };
  const state = statePresentation(operation, t);
  return <Flex vertical gap={10} data-testid="account-operation-result">
    {operation.execution_state === "outcome_unknown" && <Alert type="warning" showIcon title={t("operations.unknownTitle")} description={t("operations.unknownDescription")} />}
    <Descriptions size="small" bordered column={1}>
      <Descriptions.Item label={t("operations.commandId")}><Typography.Text copyable>{operation.command_id}</Typography.Text></Descriptions.Item>
      <Descriptions.Item label={t("operations.operation")}>{operation.operation_kind}</Descriptions.Item>
      <Descriptions.Item label={t("operations.executionState")}><Tag color={state.color}>{state.label}</Tag></Descriptions.Item>
      <Descriptions.Item label={t("operations.resultError")}>{operation.error_code ?? operation.result ?? "—"}</Descriptions.Item>
      <Descriptions.Item label={t("operations.created")}>{formatDateTime(operation.created_at, locale)}</Descriptions.Item>
      <Descriptions.Item label={t("operations.updated")}>{formatDateTime(operation.updated_at, locale)}</Descriptions.Item>
    </Descriptions>
  </Flex>;
}

export function AccountOperationsPanel({ api, csrf, nodeInstanceId, accountKey, basicStatus, onUnauthorized }: { api: AccountOperationsApi; csrf: string; nodeInstanceId: string; accountKey: string; basicStatus?: string; onUnauthorized?: () => void }) {
  const { t: translate } = useTranslation();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const t = (key: string, options?: Record<string, unknown>) => { const fallback = fallbackOperationCopy(locale, key, options); const value = translate(key, { ...options, defaultValue: fallback }); return value === key ? fallback : value; };
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
  const reasons: Array<{ value: OverrideReason; label: string }> = [
    { value: "process_restarted", label: t("operations.reasonRestart") },
    { value: "node_stopped", label: t("operations.reasonNodeStopped") },
    { value: "risk_accepted", label: t("operations.reasonAccepted") },
  ];

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
  const confirmRemove = () => Modal.confirm({ title: t("operations.removeTitle"), content: t("operations.removeDescription"), okText: t("operations.confirmRemove"), okButtonProps: { danger: true, "data-testid": "account-remove-confirm" }, cancelButtonProps: { "data-testid": "account-remove-cancel" }, onOk: () => mutate("remove") });
  const applyOverride = () => {
    if (!operation) return Promise.resolve();
    return once(() => api.override(overrideKind, { commandId: crypto.randomUUID(), targetCommandId: operation.command_id, reason: overrideReason, detail: overrideDetail.trim() || undefined }, csrf));
  };

  return <Flex vertical gap={12} data-testid="account-operations-panel">
    <Typography.Text type="secondary">{t("operations.guidance")}</Typography.Text>
    {error && <Alert type="error" showIcon title={error} />}
    <Space wrap>
      {basicStatus !== "disabled" && <Button data-testid="account-disable" disabled={busy} loading={busy} onClick={() => void mutate("disable")}>{t("operations.disable")}</Button>}
      {basicStatus === "disabled" && <Button data-testid="account-enable" disabled={busy} loading={busy} onClick={() => void mutate("enable")}>{t("operations.enable")}</Button>}
      <Button data-testid="account-remove" danger disabled={busy} onClick={confirmRemove}>{t("operations.remove")}</Button>
    </Space>
    <Upload data-testid="account-existing-credential-file" key={credentialInputKey} beforeUpload={(file) => { if (file.size > maxCredentialBytes) { setError(t("operations.credentialTooLarge")); return Upload.LIST_IGNORE; } setCredential(file); setError(""); return false; }} fileList={credential ? [{ uid: "credential", name: credential.name, status: "done" } as UploadFile] : []} onRemove={() => { clearCredential(); return true; }} maxCount={1} accept="application/json,.json">
      <Button disabled={busy}>{t("operations.chooseCredential")}</Button>
    </Upload>
    <Space wrap>
      <Button data-testid="account-upload-existing" type="primary" disabled={busy || !credential} loading={busy} onClick={() => void mutate("upload_new")}>{t("operations.uploadNew")}</Button>
      <Button data-testid="account-replace-existing" danger disabled={busy || !credential} loading={busy} onClick={() => Modal.confirm({ title: t("operations.replaceTitle"), content: t("operations.replaceDescription"), okText: t("operations.confirmReplace"), okButtonProps: { "data-testid": "account-replace-confirm" }, cancelButtonProps: { "data-testid": "account-replace-cancel" }, onOk: () => mutate("replace_existing") })}>{t("operations.replaceExisting")}</Button>
    </Space>
    <Divider />
    <Flex gap={8} wrap>
      <Input data-testid="account-operation-command-id" aria-label={t("operations.commandId")} value={lookupID} onChange={(event) => setLookupID(event.target.value)} placeholder={t("operations.lookupPlaceholder")} style={{ maxWidth: 390 }} />
      <Button data-testid="account-operation-read" disabled={busy || !lookupID.trim()} loading={busy} onClick={() => void once(() => api.operation(lookupID.trim()))}>{t("operations.read")}</Button>
    </Flex>
    {operation && <AccountOperationResult operation={operation} />}
    {operation?.execution_state === "outcome_unknown" && <>
      <Divider />
      <Typography.Text strong>{t("operations.unknownOverride")}</Typography.Text>
      <Select data-testid="account-override-kind" aria-label={t("operations.overrideType")} value={overrideKind} onChange={setOverrideKind} options={[{ value: "lifecycle", label: t("operations.lifecycleOverride") }, { value: "same_account", label: t("operations.sameAccountOverride") }]} />
      <Select data-testid="account-override-reason" aria-label={t("operations.overrideReason")} value={overrideReason} onChange={setOverrideReason} options={reasons} />
      <Input.TextArea data-testid="account-override-detail" aria-label={t("operations.overrideDetail")} value={overrideDetail} maxLength={512} onChange={(event) => setOverrideDetail(event.target.value)} placeholder={t("operations.optionalDetail")} />
      <Button data-testid="account-override-submit" danger disabled={busy} loading={busy} onClick={() => Modal.confirm({ title: t("operations.overrideConfirmTitle", { kind: overrideKind === "lifecycle" ? "Lifecycle" : "Same-account" }), content: t("operations.overrideTarget", { commandId: operation.command_id }), okText: t("operations.confirmOverride"), okButtonProps: { "data-testid": "account-override-confirm" }, cancelButtonProps: { "data-testid": "account-override-cancel" }, onOk: applyOverride })}>{t("operations.submitOverride")}</Button>
    </>}
  </Flex>;
}

export function UploadNewAccountAction({ api, csrf, nodeInstanceId, onUnauthorized, surface = "topology" }: { api: AccountOperationsApi; csrf: string; nodeInstanceId: string; onUnauthorized?: () => void; surface?: "accounts" | "topology" }) {
  const { t: translate } = useTranslation();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const operationCopy = resources[locale].translation.operations;
  const accountOperationCopy = surface === "accounts" ? { ...operationCopy, uploadAccount: operationCopy.accountsUploadAccount, uploadDescription: operationCopy.accountsUploadDescription, uploadProvider: operationCopy.accountsUploadProvider, uploadEmail: operationCopy.accountsUploadEmail } : operationCopy;
  const t = (key: string, options?: Record<string, unknown>) => { const leaf = key.slice("operations.".length) as keyof typeof accountOperationCopy; const fallback = typeof accountOperationCopy[leaf] === "string" ? accountOperationCopy[leaf].replace(/{{(\w+)}}/g, (_, name: string) => String(options?.[name] ?? `{{${name}}}`)) : fallbackOperationCopy(locale, key, options); const value = translate(key, { ...options, defaultValue: fallback }); return value === key ? fallback : value; };
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
    <Button data-testid="account-upload-new" type="primary" onClick={() => { setError(""); setOpen(true); }}>{t("operations.uploadAccount")}</Button>
    {operation && <AccountOperationResult operation={operation} />}
    <Modal title={t("operations.uploadAccount")} open={open} onCancel={close} onOk={() => void submit()} okText={t("operations.uploadNew")} confirmLoading={busy} okButtonProps={{ "data-testid": "account-upload-new-submit", disabled: busy || !credential || !email.trim() }} cancelButtonProps={{ disabled: busy }} destroyOnHidden>
      <Flex vertical gap={12}>
        <Typography.Text type="secondary">{t("operations.uploadDescription")}</Typography.Text>
        {error && <Alert type="error" showIcon title={error} />}
        <Input aria-label={t("operations.uploadProvider")} value="antigravity" disabled />
        <Input data-testid="account-upload-new-email" aria-label={t("operations.uploadEmail")} type="email" autoComplete="off" value={email} onChange={(event) => setEmail(event.target.value)} placeholder={t("operations.accountEmail")} />
        <Upload data-testid="account-upload-new-file" key={credentialInputKey} beforeUpload={(file) => { if (file.size > maxCredentialBytes) { setError(t("operations.credentialTooLarge")); return Upload.LIST_IGNORE; } setCredential(file); setError(""); return false; }} fileList={credential ? [{ uid: "new-account-credential", name: credential.name, status: "done" } as UploadFile] : []} onRemove={() => { clearCredential(); return true; }} maxCount={1} accept="application/json,.json">
          <Button data-testid="account-upload-new-file-button" disabled={busy}>{t("operations.chooseCredential")}</Button>
        </Upload>
      </Flex>
    </Modal>
  </Flex>;
}

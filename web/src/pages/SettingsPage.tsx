import { formatDateTime } from "../foundation/format";
import { PageShell } from "../foundation/PageShell";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { LocaleSwitcher } from "../foundation/LocaleSwitcher";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Flex,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
} from "antd";
import type {
  Administrator,
  ChangePasswordRequest,
  CreateAdministratorRequest,
  MfaMethod,
  ReauthenticateRequest,
} from "../api/generated/control";
import { AuthApiError, userFacingError } from "../api/auth-api";
import { handleSessionError, useAuth } from "../auth/AuthContext";
import { useTranslation } from "react-i18next";

const { Title, Paragraph, Text } = Typography;

function isFreshReauthentication(until?: string | null): boolean {
  return Boolean(until && Date.parse(until) > Date.now());
}

export default function SettingsPage() {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const { t } = useTranslation();
  const session = auth.session;
  const [administrators, setAdministrators] = useState<Administrator[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [reason, setReason] = useState("");
  const [selected, setSelected] = useState<string>();
  const [reauthForm] = Form.useForm<ReauthenticateRequest>();
  const [passwordForm] = Form.useForm<ChangePasswordRequest>();
  const [createForm] = Form.useForm<CreateAdministratorRequest>();
  const expireSession = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
    auth.clearSession();
  }, [auth, queryClient]);
  const mfaOptions = useMemo(() => [
    { label: t("settings.mfa.totp"), value: "totp" satisfies MfaMethod },
    { label: t("settings.mfa.recoveryCode"), value: "recovery_code" satisfies MfaMethod },
  ], [t]);

  const run = useCallback(async <T,>(operation: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      return await operation();
    } catch (cause) {
      handleSessionError(cause, expireSession);
      if (cause instanceof AuthApiError && cause.detail.code === "csrf_invalid") {
        await auth.refreshSession().catch(() => expireSession());
        setError(t("settings.csrfInvalid"));
      } else {
        setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
      }
      return undefined;
    } finally {
      setBusy(false);
    }
  }, [auth, expireSession, t]);

  const loadAdministrators = useCallback(async () => {
    const response = await run(() => auth.api.administrators());
    if (response) setAdministrators(response.items);
  }, [auth.api, run]);

  useEffect(() => {
    void loadAdministrators();
  }, [loadAdministrators]);

  const csrf = session?.csrf_token ?? "";
  const requireFresh = () => {
    if (!isFreshReauthentication(auth.session?.reauthenticated_until)) {
      setError(t("settings.reauthenticationRequired"));
      return false;
    }
    return true;
  };

  const submitReauthentication = async (input: ReauthenticateRequest) => {
    const response = await run(() => auth.api.reauthenticate(csrf, input));
    if (response) {
      auth.acceptSession(response);
      reauthForm.resetFields();
      setNotice(t("settings.reauthenticationSuccess"));
    }
  };

  const submitPassword = async (input: ChangePasswordRequest) => {
    const response = await run(() => auth.api.changePassword(csrf, input));
    if (response) {
      auth.acceptSession(response);
      passwordForm.resetFields();
      setNotice(t("settings.passwordChanged"));
    }
  };

  const createAdmin = async (input: CreateAdministratorRequest) => {
    if (!requireFresh()) return;
    const response = await run(() => auth.api.createAdministrator(csrf, { ...input, display_name: input.display_name.trim() }));
    if (response) {
      createForm.resetFields();
      await loadAdministrators();
      auth.showActivationToken(response);
    }
  };

  const operate = async (kind: "disable" | "token" | "mfa") => {
    if (!selected || reason.trim().length < 10 || !requireFresh()) return;
    const input = { reason: reason.trim() };
    const response = await run<Administrator | Awaited<ReturnType<typeof auth.api.activationToken>>>(async () => {
      if (kind === "disable") return await auth.api.disableAdministrator(csrf, selected, input);
      if (kind === "token") return await auth.api.activationToken(csrf, selected, input);
      return await auth.api.resetMfa(csrf, selected, input);
    });
    if (!response) return;
    setReason("");
    await loadAdministrators();
    if (kind !== "disable" && "activation_token" in response) auth.showActivationToken(response);
    else setNotice(t("settings.administratorDisabled"));
  };

  const regenerateCodes = async () => {
    if (reason.trim().length < 10 || !requireFresh()) return;
    const response = await run(() => auth.api.recoveryCodes(csrf, { reason: reason.trim() }));
    if (response) {
      setReason("");
      auth.showRecoveryCodes(response.recovery_codes);
    }
  };

  const columns = useMemo(() => [
    { title: t("settings.admins.loginName"), dataIndex: "login_name", key: "login_name" },
    { title: t("settings.admins.displayName"), dataIndex: "display_name", key: "display_name" },
    { title: t("settings.admins.role"), dataIndex: "role", key: "role", render: () => <Tag>super_admin</Tag> },
    { title: t("settings.admins.status"), dataIndex: "status", key: "status", render: (status: Administrator["status"]) => <Tag color={status === "enabled" ? "green" : status === "pending" ? "gold" : "default"}>{status}</Tag> },
    { title: t("settings.admins.lastLogin"), dataIndex: "last_login_at", key: "last_login_at", render: (value: string | null) => formatDateTime(value, locale) },
  ], [locale, t]);

  if (!session) return null;

  const highRisk = (
    <Alert
      type={isFreshReauthentication(session.reauthenticated_until) ? "success" : "warning"}
      showIcon
      message={isFreshReauthentication(session.reauthenticated_until) ? t("settings.highRiskValidUntil", { date: formatDateTime(session.reauthenticated_until, locale) }) : t("settings.highRiskLocked")}
    />
  );

  return (
    <PageShell
      className="management-page"
      testId="settings-page"
      title={t("settings.title")}
      description={t("settings.description")}
    >
      {error && <Alert type="error" showIcon message={error} className="form-alert" />}
      {notice && <Alert type="success" showIcon message={notice} className="form-alert" />}
      <Card>
        <Tabs
          destroyOnHidden
          items={[
            {
              key: "session",
              label: <span data-testid="settings-tab-session">{t("settings.tabs.session")}</span>,
              children: (
                <Flex vertical gap={16} data-testid="settings-session-summary">
                  <Title level={4}>{t("settings.session.current")}</Title>
                  <Text>{t("settings.session.idleUntil", { date: formatDateTime(session.idle_expires_at, locale) })}</Text>
                  <Text>{t("settings.session.absoluteUntil", { date: formatDateTime(session.absolute_expires_at, locale) })}</Text>
                  <Text>{t("settings.session.mfa", { value: session.mfa.completed ? session.mfa.method ?? t("settings.session.completed") : t("settings.session.incomplete") })}</Text>
                  <Text>{t("settings.session.recoveryRemaining", { count: session.recovery_codes_remaining })}</Text>
                  <Button data-testid="settings-session-refresh" onClick={() => void auth.refreshSession()}>{t("settings.session.refresh")}</Button>
                </Flex>
              ),
            },
            {
              key: "reauth",
              label: <span data-testid="settings-tab-reauth">{t("settings.tabs.reauth")}</span>,
              children: (
                <Flex vertical gap={16} className="form-column">
                  <div data-testid="settings-high-risk-state">{highRisk}</div>
                  <Form form={reauthForm} layout="vertical" preserve={false} onFinish={submitReauthentication}>
                    <Form.Item label={t("settings.reauth.currentPassword")} name="password" rules={[{ required: true }]}><Input.Password data-testid="settings-reauth-password" autoComplete="current-password" /></Form.Item>
                    <Form.Item label={t("settings.reauth.mfaMethod")} name="mfa_method" initialValue="totp"><Select data-testid="settings-reauth-method" options={mfaOptions} /></Form.Item>
                    <Form.Item label={t("settings.reauth.mfaCode")} name="mfa_code" rules={[{ required: true }]}><Input.Password data-testid="settings-reauth-code" visibilityToggle={false} autoComplete="one-time-code" /></Form.Item>
                    <Button data-testid="settings-reauth-submit" type="primary" htmlType="submit" loading={busy}>{t("settings.reauth.submit")}</Button>
                  </Form>
                </Flex>
              ),
            },
            {
              key: "password",
              label: <span data-testid="settings-tab-password">{t("settings.tabs.password")}</span>,
              children: (
                <Form form={passwordForm} className="form-column" layout="vertical" preserve={false} onFinish={submitPassword}>
                  <Form.Item label={t("settings.password.current")} name="current_password" rules={[{ required: true }]}><Input.Password data-testid="settings-password-current" autoComplete="current-password" /></Form.Item>
                  <Form.Item label={t("settings.password.next")} name="new_password" rules={[{ required: true }, { min: 14 }, { max: 128 }]}><Input.Password data-testid="settings-password-new" autoComplete="new-password" /></Form.Item>
                  <Form.Item label={t("settings.password.mfaMethod")} name="mfa_method" initialValue="totp"><Select data-testid="settings-password-method" options={mfaOptions} /></Form.Item>
                  <Form.Item label={t("settings.password.mfaCode")} name="mfa_code"><Input.Password data-testid="settings-password-mfa-code" visibilityToggle={false} autoComplete="one-time-code" /></Form.Item>
                  <Button data-testid="settings-password-submit" type="primary" htmlType="submit" loading={busy}>{t("settings.password.submit")}</Button>
                </Form>
              ),
            },
            {
              key: "admins",
              label: <span data-testid="settings-tab-administrators">{t("settings.tabs.admins")}</span>,
              children: (
                <Flex vertical gap={20}>
                  <div data-testid="settings-high-risk-state">{highRisk}</div>
                  <Table rowKey="id" size="small" scroll={{ x: 720 }} pagination={false} columns={columns} dataSource={administrators} rowSelection={{ type: "radio", selectedRowKeys: selected ? [selected] : [], onChange: (keys) => setSelected(String(keys[0])), renderCell: (_value, record, _index, originNode) => <span data-testid={`settings-admin-select-${record.id}`}>{originNode}</span>, getCheckboxProps: (record) => ({ "aria-label": `选择管理员 ${record.login_name}` }) }} />
                  <Card size="small" title={t("settings.admins.createTitle")}>
                    <Form form={createForm} layout="vertical" preserve={false} onFinish={createAdmin}>
                      <Form.Item label={t("settings.admins.loginName")} name="login_name" rules={[{ required: true }, { pattern: /^[a-z0-9._-]{3,64}$/ }]}><Input data-testid="settings-admin-create-login" /></Form.Item>
                      <Form.Item label={t("settings.admins.createDisplayName")} name="display_name" rules={[{ required: true, whitespace: true }, { max: 100 }]}><Input data-testid="settings-admin-create-display-name" /></Form.Item>
                      <Form.Item label={t("settings.admins.reason")} name="reason" rules={[{ required: true }, { min: 10 }, { max: 500 }]}><Input.TextArea data-testid="settings-admin-create-reason" /></Form.Item>
                      <Button data-testid="settings-admin-create-submit" type="primary" htmlType="submit" loading={busy}>{t("settings.admins.create")}</Button>
                    </Form>
                  </Card>
                  <Card size="small" title={t("settings.admins.actionTitle")}>
                    <Flex vertical gap={12}>
                      <Input.TextArea data-testid="settings-admin-action-reason" value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t("settings.admins.reasonPlaceholder")} maxLength={500} />
                      <Space wrap>
                        <Button data-testid="settings-admin-disable" danger disabled={!selected || reason.trim().length < 10} onClick={() => Modal.confirm({ title: t("settings.admins.disableConfirmTitle"), content: t("settings.admins.disableConfirmContent"), okButtonProps: { "data-testid": "settings-admin-disable-confirm" }, cancelButtonProps: { "data-testid": "settings-admin-disable-cancel" }, onOk: () => operate("disable") })}>{t("settings.admins.disable")}</Button>
                        <Button data-testid="settings-admin-token" disabled={!selected || reason.trim().length < 10} onClick={() => void operate("token")}>{t("settings.admins.token")}</Button>
                        <Button data-testid="settings-admin-reset-mfa" danger disabled={!selected || reason.trim().length < 10} onClick={() => Modal.confirm({ title: t("settings.admins.resetConfirmTitle"), content: t("settings.admins.resetConfirmContent"), okButtonProps: { "data-testid": "settings-admin-reset-mfa-confirm" }, cancelButtonProps: { "data-testid": "settings-admin-reset-mfa-cancel" }, onOk: () => operate("mfa") })}>{t("settings.admins.resetMfa")}</Button>
                      </Space>
                    </Flex>
                  </Card>
                </Flex>
              ),
            },
            {
              key: "recovery",
              label: <span data-testid="settings-tab-recovery">{t("settings.tabs.recovery")}</span>,
              children: (
                <Flex vertical gap={16} className="form-column">
                  {highRisk}
                  <Paragraph>{t("settings.recovery.description")}</Paragraph>
                  <Input.TextArea data-testid="settings-recovery-reason" value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t("settings.admins.reasonPlaceholder")} maxLength={500} />
                  <Button data-testid="settings-recovery-regenerate" danger disabled={reason.trim().length < 10} onClick={() => Modal.confirm({ title: t("settings.recovery.confirmTitle"), content: t("settings.recovery.confirmContent"), okButtonProps: { "data-testid": "settings-recovery-confirm" }, cancelButtonProps: { "data-testid": "settings-recovery-cancel" }, onOk: regenerateCodes })}>{t("settings.recovery.regenerate")}</Button>
                </Flex>
              ),
            },
            {
              key: "interface",
              label: <span data-testid="settings-tab-interface">{t("settings.tabs.interface")}</span>,
              children: (
                <Flex vertical gap={12} className="form-column" data-testid="settings-interface">
                  <Title level={4}>{t("settings.interface.title")}</Title>
                  <Paragraph>{t("settings.interface.description")}</Paragraph>
                  <LocaleSwitcher />
                </Flex>
              ),
            },
          ]}
        />
      </Card>
    </PageShell>
  );
}

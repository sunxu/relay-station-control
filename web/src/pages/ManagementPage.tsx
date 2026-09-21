import { formatDateTime } from "../foundation/format";
import { PageShell } from "../foundation/PageShell";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
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

export default function ManagementPage() {
  const auth = useAuth();
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
  const mfaOptions = useMemo(() => [
    { label: t("management.mfa.totp"), value: "totp" satisfies MfaMethod },
    { label: t("management.mfa.recoveryCode"), value: "recovery_code" satisfies MfaMethod },
  ], [t]);

  const run = useCallback(async <T,>(operation: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      return await operation();
    } catch (cause) {
      handleSessionError(cause, auth.clearSession);
      if (cause instanceof AuthApiError && cause.detail.code === "csrf_invalid") {
        await auth.refreshSession().catch(() => auth.clearSession());
        setError(t("management.csrfInvalid"));
      } else {
        setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
      }
      return undefined;
    } finally {
      setBusy(false);
    }
  }, [auth, t]);

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
      setError(t("management.reauthenticationRequired"));
      return false;
    }
    return true;
  };

  const submitReauthentication = async (input: ReauthenticateRequest) => {
    const response = await run(() => auth.api.reauthenticate(csrf, input));
    if (response) {
      auth.acceptSession(response);
      reauthForm.resetFields();
      setNotice(t("management.reauthenticationSuccess"));
    }
  };

  const submitPassword = async (input: ChangePasswordRequest) => {
    const response = await run(() => auth.api.changePassword(csrf, input));
    if (response) {
      auth.acceptSession(response);
      passwordForm.resetFields();
      setNotice(t("management.passwordChanged"));
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
    else setNotice(t("management.administratorDisabled"));
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
    { title: t("management.admins.loginName"), dataIndex: "login_name", key: "login_name" },
    { title: t("management.admins.displayName"), dataIndex: "display_name", key: "display_name" },
    { title: t("management.admins.role"), dataIndex: "role", key: "role", render: () => <Tag>super_admin</Tag> },
    { title: t("management.admins.status"), dataIndex: "status", key: "status", render: (status: Administrator["status"]) => <Tag color={status === "enabled" ? "green" : status === "pending" ? "gold" : "default"}>{status}</Tag> },
    { title: t("management.admins.lastLogin"), dataIndex: "last_login_at", key: "last_login_at", render: (value: string | null) => formatDateTime(value, locale) },
  ], [locale, t]);

  if (!session) return null;

  const highRisk = (
    <Alert
      type={isFreshReauthentication(session.reauthenticated_until) ? "success" : "warning"}
      showIcon
      message={isFreshReauthentication(session.reauthenticated_until) ? t("management.highRiskValidUntil", { date: formatDateTime(session.reauthenticated_until, locale) }) : t("management.highRiskLocked")}
    />
  );

  return (
    <PageShell
      className="management-page"
      testId="management-page"
      title={t("management.title")}
      description={t("management.sessionDescription", { displayName: session.administrator.display_name, loginName: session.administrator.login_name })}
      actions={
        <Space wrap>
          <Button data-testid="management-nav-jobs" onClick={() => auth.navigate("jobs")}>{t("management.navJobs")}</Button>
          <Button data-testid="management-nav-assets" onClick={() => auth.navigate("assets")}>{t("management.navAssets")}</Button>
          <Button data-testid="management-nav-topology" onClick={() => auth.navigate("topology")}>{t("management.navTopology")}</Button>
          <Button data-testid="management-nav-problems" onClick={() => auth.navigate("problems")}>{t("management.navProblems")}</Button>
        </Space>
      }
    >
      {error && <Alert type="error" showIcon message={error} className="form-alert" />}
      {notice && <Alert type="success" showIcon message={notice} className="form-alert" />}
      <Card>
        <Tabs
          destroyOnHidden
          items={[
            {
              key: "session",
              label: t("management.tabs.session"),
              children: (
                <Flex vertical gap={16}>
                  <Title level={4}>{t("management.session.current")}</Title>
                  <Text>{t("management.session.idleUntil", { date: formatDateTime(session.idle_expires_at, locale) })}</Text>
                  <Text>{t("management.session.absoluteUntil", { date: formatDateTime(session.absolute_expires_at, locale) })}</Text>
                  <Text>{t("management.session.mfa", { value: session.mfa.completed ? session.mfa.method ?? t("management.session.completed") : t("management.session.incomplete") })}</Text>
                  <Text>{t("management.session.recoveryRemaining", { count: session.recovery_codes_remaining })}</Text>
                  <Button onClick={() => void auth.refreshSession()}>{t("management.session.refresh")}</Button>
                </Flex>
              ),
            },
            {
              key: "reauth",
              label: t("management.tabs.reauth"),
              children: (
                <Flex vertical gap={16} className="form-column">
                  {highRisk}
                  <Form form={reauthForm} layout="vertical" preserve={false} onFinish={submitReauthentication}>
                    <Form.Item label={t("management.reauth.currentPassword")} name="password" rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item>
                    <Form.Item label={t("management.reauth.mfaMethod")} name="mfa_method" initialValue="totp"><Select options={mfaOptions} /></Form.Item>
                    <Form.Item label={t("management.reauth.mfaCode")} name="mfa_code" rules={[{ required: true }]}><Input.Password visibilityToggle={false} autoComplete="one-time-code" /></Form.Item>
                    <Button type="primary" htmlType="submit" loading={busy}>{t("management.reauth.submit")}</Button>
                  </Form>
                </Flex>
              ),
            },
            {
              key: "password",
              label: t("management.tabs.password"),
              children: (
                <Form form={passwordForm} className="form-column" layout="vertical" preserve={false} onFinish={submitPassword}>
                  <Form.Item label={t("management.password.current")} name="current_password" rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item>
                  <Form.Item label={t("management.password.next")} name="new_password" rules={[{ required: true }, { min: 14 }, { max: 128 }]}><Input.Password autoComplete="new-password" /></Form.Item>
                  <Form.Item label={t("management.password.mfaMethod")} name="mfa_method" initialValue="totp"><Select options={mfaOptions} /></Form.Item>
                  <Form.Item label={t("management.password.mfaCode")} name="mfa_code"><Input.Password visibilityToggle={false} autoComplete="one-time-code" /></Form.Item>
                  <Button type="primary" htmlType="submit" loading={busy}>{t("management.password.submit")}</Button>
                </Form>
              ),
            },
            {
              key: "admins",
              label: t("management.tabs.admins"),
              children: (
                <Flex vertical gap={20}>
                  {highRisk}
                  <Table rowKey="id" size="small" scroll={{ x: 720 }} pagination={false} columns={columns} dataSource={administrators} rowSelection={{ type: "radio", selectedRowKeys: selected ? [selected] : [], onChange: (keys) => setSelected(String(keys[0])), getCheckboxProps: (record) => ({ "aria-label": `选择管理员 ${record.login_name}` }) }} />
                  <Card size="small" title={t("management.admins.createTitle")}>
                    <Form form={createForm} layout="vertical" preserve={false} onFinish={createAdmin}>
                      <Form.Item label={t("management.admins.loginName")} name="login_name" rules={[{ required: true }, { pattern: /^[a-z0-9._-]{3,64}$/ }]}><Input /></Form.Item>
                      <Form.Item label={t("management.admins.createDisplayName")} name="display_name" rules={[{ required: true, whitespace: true }, { max: 100 }]}><Input /></Form.Item>
                      <Form.Item label={t("management.admins.reason")} name="reason" rules={[{ required: true }, { min: 10 }, { max: 500 }]}><Input.TextArea /></Form.Item>
                      <Button type="primary" htmlType="submit" loading={busy}>{t("management.admins.create")}</Button>
                    </Form>
                  </Card>
                  <Card size="small" title={t("management.admins.actionTitle")}>
                    <Flex vertical gap={12}>
                      <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t("management.admins.reasonPlaceholder")} maxLength={500} />
                      <Space wrap>
                        <Button data-testid="disable-administrator" danger disabled={!selected || reason.trim().length < 10} onClick={() => Modal.confirm({ title: t("management.admins.disableConfirmTitle"), content: t("management.admins.disableConfirmContent"), onOk: () => operate("disable") })}>{t("management.admins.disable")}</Button>
                        <Button disabled={!selected || reason.trim().length < 10} onClick={() => void operate("token")}>{t("management.admins.token")}</Button>
                        <Button danger disabled={!selected || reason.trim().length < 10} onClick={() => Modal.confirm({ title: t("management.admins.resetConfirmTitle"), content: t("management.admins.resetConfirmContent"), onOk: () => operate("mfa") })}>{t("management.admins.resetMfa")}</Button>
                      </Space>
                    </Flex>
                  </Card>
                </Flex>
              ),
            },
            {
              key: "recovery",
              label: t("management.tabs.recovery"),
              children: (
                <Flex vertical gap={16} className="form-column">
                  {highRisk}
                  <Paragraph>{t("management.recovery.description")}</Paragraph>
                  <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t("management.admins.reasonPlaceholder")} maxLength={500} />
                  <Button danger disabled={reason.trim().length < 10} onClick={() => Modal.confirm({ title: t("management.recovery.confirmTitle"), content: t("management.recovery.confirmContent"), onOk: regenerateCodes })}>{t("management.recovery.regenerate")}</Button>
                </Flex>
              ),
            },
          ]}
        />
      </Card>
    </PageShell>
  );
}

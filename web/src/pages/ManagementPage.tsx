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

const { Title, Paragraph, Text } = Typography;

function isFreshReauthentication(until?: string | null): boolean {
  return Boolean(until && Date.parse(until) > Date.now());
}

export default function ManagementPage() {
  const auth = useAuth();
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
        setError("安全凭据已刷新。为避免重复执行，本次操作没有自动重放，请确认状态后重试。");
      } else {
        setError(userFacingError(cause));
      }
      return undefined;
    } finally {
      setBusy(false);
    }
  }, [auth]);

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
      setError("此操作需要先在“重新认证”页签验证密码与 MFA，证明有效期为 5 分钟。");
      return false;
    }
    return true;
  };

  const submitReauthentication = async (input: ReauthenticateRequest) => {
    const response = await run(() => auth.api.reauthenticate(csrf, input));
    if (response) {
      auth.acceptSession(response);
      reauthForm.resetFields();
      setNotice("重新认证成功，高风险操作窗口已开启 5 分钟。");
    }
  };

  const submitPassword = async (input: ChangePasswordRequest) => {
    const response = await run(() => auth.api.changePassword(csrf, input));
    if (response) {
      auth.acceptSession(response);
      passwordForm.resetFields();
      setNotice("密码已修改，其他会话已撤销。");
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
    else setNotice("管理员已禁用，其会话与未使用令牌均已撤销。");
  };

  const regenerateCodes = async () => {
    if (reason.trim().length < 10 || !requireFresh()) return;
    const response = await run(() => auth.api.recoveryCodes(csrf, { reason: reason.trim() }));
    if (response) {
      setReason("");
      auth.showRecoveryCodes(response.recovery_codes);
    }
  };

  const logout = async () => {
    const response = await run(async () => {
      await auth.api.logout(csrf);
      return true;
    });
    if (response) auth.clearSession();
  };

  const columns = useMemo(() => [
    { title: "登录名", dataIndex: "login_name", key: "login_name" },
    { title: "实名", dataIndex: "display_name", key: "display_name" },
    { title: "角色", dataIndex: "role", key: "role", render: () => <Tag>super_admin</Tag> },
    { title: "状态", dataIndex: "status", key: "status", render: (status: Administrator["status"]) => <Tag color={status === "enabled" ? "green" : status === "pending" ? "gold" : "default"}>{status}</Tag> },
    { title: "最近登录", dataIndex: "last_login_at", key: "last_login_at", render: (value: string | null | undefined) => value ? new Date(value).toLocaleString() : "—" },
  ], []);

  if (!session) return null;

  const highRisk = (
    <Alert
      type={isFreshReauthentication(session.reauthenticated_until) ? "success" : "warning"}
      showIcon
      message={isFreshReauthentication(session.reauthenticated_until) ? `重新认证有效至 ${new Date(session.reauthenticated_until!).toLocaleTimeString()}` : "高风险操作当前锁定"}
    />
  );

  return (
    <main className="management-page" data-testid="management-page">
      <Flex justify="space-between" align="center" wrap gap={16} className="management-header">
        <div>
          <Title level={2}>Relay Station Control</Title>
          <Text type="secondary">{session.administrator.display_name} · {session.administrator.login_name}</Text>
        </div>
        <Space wrap>
          <Space wrap>
            <Button onClick={() => auth.navigate("jobs")}>持久任务</Button>
            <Button onClick={() => auth.navigate("assets")}>资产注册表</Button>
            <Button onClick={() => auth.navigate("topology")}>Node Topology</Button>
          </Space>
          <Button danger loading={busy} onClick={() => void logout()}>注销</Button>
        </Space>
      </Flex>
      {error && <Alert type="error" showIcon message={error} className="form-alert" />}
      {notice && <Alert type="success" showIcon message={notice} className="form-alert" />}
      <Card>
        <Tabs
          destroyOnHidden
          items={[
            {
              key: "session",
              label: "会话",
              children: (
                <Flex vertical gap={16}>
                  <Title level={4}>当前会话</Title>
                  <Text>空闲到期：{new Date(session.idle_expires_at).toLocaleString()}</Text>
                  <Text>绝对到期：{new Date(session.absolute_expires_at).toLocaleString()}</Text>
                  <Text>MFA：{session.mfa.completed ? session.mfa.method ?? "已完成" : "未完成"}</Text>
                  <Text>剩余恢复码：{session.recovery_codes_remaining}</Text>
                  <Button onClick={() => void auth.refreshSession()}>刷新会话与 CSRF</Button>
                </Flex>
              ),
            },
            {
              key: "reauth",
              label: "重新认证",
              children: (
                <Flex vertical gap={16} className="form-column">
                  {highRisk}
                  <Form form={reauthForm} layout="vertical" preserve={false} onFinish={submitReauthentication}>
                    <Form.Item label="当前密码" name="password" rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item>
                    <Form.Item label="MFA 方式" name="mfa_method" initialValue="totp"><Select options={[{ label: "TOTP", value: "totp" satisfies MfaMethod }, { label: "恢复码", value: "recovery_code" satisfies MfaMethod }]} /></Form.Item>
                    <Form.Item label="MFA 验证码" name="mfa_code" rules={[{ required: true }]}><Input.Password visibilityToggle={false} autoComplete="one-time-code" /></Form.Item>
                    <Button type="primary" htmlType="submit" loading={busy}>重新认证</Button>
                  </Form>
                </Flex>
              ),
            },
            {
              key: "password",
              label: "修改密码",
              children: (
                <Form form={passwordForm} className="form-column" layout="vertical" preserve={false} onFinish={submitPassword}>
                  <Form.Item label="当前密码" name="current_password" rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item>
                  <Form.Item label="新密码" name="new_password" rules={[{ required: true }, { min: 14 }, { max: 128 }]}><Input.Password autoComplete="new-password" /></Form.Item>
                  <Form.Item label="MFA 方式" name="mfa_method" initialValue="totp"><Select options={[{ label: "TOTP", value: "totp" }, { label: "恢复码", value: "recovery_code" }]} /></Form.Item>
                  <Form.Item label="MFA 验证码" name="mfa_code"><Input.Password visibilityToggle={false} autoComplete="one-time-code" /></Form.Item>
                  <Button type="primary" htmlType="submit" loading={busy}>修改密码并轮换会话</Button>
                </Form>
              ),
            },
            {
              key: "admins",
              label: "管理员",
              children: (
                <Flex vertical gap={20}>
                  {highRisk}
                  <Table rowKey="id" size="small" scroll={{ x: 720 }} pagination={false} columns={columns} dataSource={administrators} rowSelection={{ type: "radio", selectedRowKeys: selected ? [selected] : [], onChange: (keys) => setSelected(String(keys[0])), getCheckboxProps: (record) => ({ "aria-label": `选择管理员 ${record.login_name}` }) }} />
                  <Card size="small" title="创建待激活管理员">
                    <Form form={createForm} layout="vertical" preserve={false} onFinish={createAdmin}>
                      <Form.Item label="登录名" name="login_name" rules={[{ required: true }, { pattern: /^[a-z0-9._-]{3,64}$/ }]}><Input /></Form.Item>
                      <Form.Item label="实名显示名" name="display_name" rules={[{ required: true, whitespace: true }, { max: 100 }]}><Input /></Form.Item>
                      <Form.Item label="操作原因" name="reason" rules={[{ required: true }, { min: 10 }, { max: 500 }]}><Input.TextArea /></Form.Item>
                      <Button type="primary" htmlType="submit" loading={busy}>创建并显示一次性令牌</Button>
                    </Form>
                  </Card>
                  <Card size="small" title="对选中管理员执行高风险操作">
                    <Flex vertical gap={12}>
                      <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="操作原因（10–500 字符）" maxLength={500} />
                      <Space wrap>
                        <Button data-testid="disable-administrator" danger disabled={!selected || reason.trim().length < 10} onClick={() => Modal.confirm({ title: "确认禁用管理员？", content: "目标的全部会话与未使用令牌将被撤销。", onOk: () => operate("disable") })}>禁用</Button>
                        <Button disabled={!selected || reason.trim().length < 10} onClick={() => void operate("token")}>重新生成激活令牌</Button>
                        <Button danger disabled={!selected || reason.trim().length < 10} onClick={() => Modal.confirm({ title: "确认重置 MFA？", content: "目标将回到待激活状态并失去现有会话。", onOk: () => operate("mfa") })}>重置 MFA</Button>
                      </Space>
                    </Flex>
                  </Card>
                </Flex>
              ),
            },
            {
              key: "recovery",
              label: "恢复码",
              children: (
                <Flex vertical gap={16} className="form-column">
                  {highRisk}
                  <Paragraph>重新生成会立即撤销全部旧恢复码，新码只显示一次。</Paragraph>
                  <Input.TextArea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="操作原因（10–500 字符）" maxLength={500} />
                  <Button danger disabled={reason.trim().length < 10} onClick={() => Modal.confirm({ title: "重新生成恢复码？", content: "所有旧恢复码将立即失效。", onOk: regenerateCodes })}>重新生成恢复码</Button>
                </Flex>
              ),
            },
          ]}
        />
      </Card>
    </main>
  );
}

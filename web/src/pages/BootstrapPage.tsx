import { useEffect, useState } from "react";
import { Alert, Button, Card, Flex, Form, Input, Space, Typography } from "antd";
import type { BootstrapStartRequest, TotpEnrollment } from "../api/generated/control";
import { userFacingError } from "../api/auth-api";
import { useAuth } from "../auth/AuthContext";

const { Title, Paragraph, Text } = Typography;

interface BootstrapForm extends BootstrapStartRequest {
  secret: string;
}

export default function BootstrapPage() {
  const auth = useAuth();
  const [form] = Form.useForm<BootstrapForm>();
  const [secret, setSecret] = useState("");
  const [enrollment, setEnrollment] = useState<TotpEnrollment | null>(null);
  const [totpCode, setTotpCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => () => {
    form.resetFields();
    setSecret("");
    setEnrollment(null);
    setTotpCode("");
  }, [form]);

  const start = async (values: BootstrapForm) => {
    setBusy(true);
    setError("");
    try {
      const response = await auth.api.bootstrapStart(values.secret, {
        login_name: values.login_name,
        display_name: values.display_name.trim(),
        password: values.password,
      });
      setSecret(values.secret);
      form.resetFields();
      setEnrollment(response.totp_enrollment);
    } catch (cause) {
      setError(userFacingError(cause));
    } finally {
      setBusy(false);
    }
  };

  const complete = async () => {
    setBusy(true);
    setError("");
    try {
      const response = await auth.api.bootstrapComplete(secret, totpCode);
      setSecret("");
      setEnrollment(null);
      setTotpCode("");
      auth.acceptSession(response.session);
      auth.showRecoveryCodes(response.recovery_codes);
    } catch (cause) {
      setError(userFacingError(cause));
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    setBusy(true);
    setError("");
    try {
      await auth.api.bootstrapReset(secret || form.getFieldValue("secret") || "");
      setSecret("");
      setEnrollment(null);
      setTotpCode("");
      form.resetFields();
    } catch (cause) {
      setError(userFacingError(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="centered-page" data-testid="bootstrap-page">
      <Card className="auth-card">
        <Title level={2}>初始化 Control</Title>
        <Paragraph type="secondary">
          {auth.bootstrapState === "in_progress" ? "存在未完成的初始化，请重新输入相同资料继续，或使用运行时 Secret 重置。" : "创建首个实名超级管理员。"}
        </Paragraph>
        {error && <Alert type="error" showIcon message={error} className="form-alert" />}
        {!enrollment ? (
          <Form form={form} layout="vertical" preserve={false} onFinish={start} autoComplete="off">
            <Form.Item label="运行时 Bootstrap Secret" name="secret" rules={[{ required: true }]}>
              <Input.Password autoComplete="off" data-testid="bootstrap-secret" />
            </Form.Item>
            <Form.Item label="登录名" name="login_name" rules={[{ required: true }, { pattern: /^[a-z0-9._-]{3,64}$/ }]}>
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item label="实名显示名" name="display_name" rules={[{ required: true, whitespace: true }, { max: 100 }]}>
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item label="密码" name="password" rules={[{ required: true }, { min: 14 }, { max: 128 }]}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Space wrap>
              <Button htmlType="submit" type="primary" loading={busy}>开始或继续初始化</Button>
              {auth.bootstrapState === "in_progress" && <Button danger disabled={busy} onClick={() => void reset()}>重置未完成流程</Button>}
            </Space>
          </Form>
        ) : (
          <Flex vertical gap={16}>
            <Alert type="warning" showIcon message="请立即添加到认证器" description="此 TOTP 配置仅在当前页面内存中保留。" />
            <div className="secret-panel" data-testid="totp-enrollment">
              <Text copyable={{ text: enrollment.otpauth_uri }}>复制 TOTP 配置 URI</Text>
              <Text type="secondary">{enrollment.algorithm} · {enrollment.digits} 位 · {enrollment.period_seconds} 秒</Text>
            </div>
            <Input
              aria-label="TOTP 验证码"
              inputMode="numeric"
              maxLength={6}
              value={totpCode}
              onChange={(event) => setTotpCode(event.target.value)}
              autoComplete="one-time-code"
            />
            <Space>
              <Button type="primary" loading={busy} disabled={!/^\d{6}$/.test(totpCode)} onClick={() => void complete()}>确认并永久完成</Button>
              <Button danger disabled={busy} onClick={() => void reset()}>重置</Button>
            </Space>
          </Flex>
        )}
      </Card>
    </main>
  );
}

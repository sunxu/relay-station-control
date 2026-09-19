import { useEffect, useState } from "react";
import { Alert, Button, Card, Flex, Form, Input, Space, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { BootstrapStartRequest, TotpEnrollment } from "../api/generated/control";
import { userFacingError } from "../api/auth-api";
import { useAuth } from "../auth/AuthContext";

const { Title, Paragraph, Text } = Typography;

interface BootstrapForm extends BootstrapStartRequest {
  secret: string;
}

export default function BootstrapPage() {
  const auth = useAuth();
  const { t } = useTranslation();
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
      setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
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
      setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
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
      setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="centered-page" data-testid="bootstrap-page">
      <Card className="auth-card">
        <Title level={2}>{t("auth.bootstrap.title")}</Title>
        <Paragraph type="secondary">
          {auth.bootstrapState === "in_progress" ? t("auth.bootstrap.inProgress") : t("auth.bootstrap.newInstallation")}
        </Paragraph>
        {error && <Alert type="error" showIcon message={error} className="form-alert" />}
        {!enrollment ? (
          <Form form={form} layout="vertical" preserve={false} onFinish={start} autoComplete="off">
            <Form.Item label={t("auth.bootstrap.secret")} name="secret" rules={[{ required: true }]}>
              <Input.Password autoComplete="off" data-testid="bootstrap-secret" />
            </Form.Item>
            <Form.Item label={t("auth.bootstrap.loginName")} name="login_name" rules={[{ required: true }, { pattern: /^[a-z0-9._-]{3,64}$/ }]}>
              <Input autoComplete="off" data-testid="bootstrap-login" />
            </Form.Item>
            <Form.Item label={t("auth.bootstrap.displayName")} name="display_name" rules={[{ required: true, whitespace: true }, { max: 100 }]}>
              <Input autoComplete="off" data-testid="bootstrap-display-name" />
            </Form.Item>
            <Form.Item label={t("auth.bootstrap.password")} name="password" rules={[{ required: true }, { min: 14 }, { max: 128 }]}>
              <Input.Password autoComplete="new-password" data-testid="bootstrap-password" />
            </Form.Item>
            <Space wrap>
              <Button data-testid="bootstrap-start" htmlType="submit" type="primary" loading={busy}>{t("auth.bootstrap.start")}</Button>
              {auth.bootstrapState === "in_progress" && <Button data-testid="bootstrap-reset-pending" danger disabled={busy} onClick={() => void reset()}>{t("auth.bootstrap.resetPending")}</Button>}
            </Space>
          </Form>
        ) : (
          <Flex vertical gap={16}>
            <Alert type="warning" showIcon message={t("auth.bootstrap.addAuthenticator")} description={t("auth.bootstrap.totpMemoryOnly")} />
            <div className="secret-panel" data-testid="totp-enrollment">
              <Text copyable={{ text: enrollment.otpauth_uri }}>{t("auth.bootstrap.copyUri")}</Text>
              <Text type="secondary">{enrollment.algorithm} · {enrollment.digits} 位 · {enrollment.period_seconds} 秒</Text>
            </div>
            <Input
              aria-label={t("auth.bootstrap.totpCode")}
              data-testid="bootstrap-totp"
              inputMode="numeric"
              maxLength={6}
              value={totpCode}
              onChange={(event) => setTotpCode(event.target.value)}
              autoComplete="one-time-code"
            />
            <Space>
              <Button data-testid="bootstrap-complete" type="primary" loading={busy} disabled={!/^\d{6}$/.test(totpCode)} onClick={() => void complete()}>{t("auth.bootstrap.confirm")}</Button>
              <Button data-testid="bootstrap-reset" danger disabled={busy} onClick={() => void reset()}>{t("auth.bootstrap.reset")}</Button>
            </Space>
          </Flex>
        )}
      </Card>
    </main>
  );
}

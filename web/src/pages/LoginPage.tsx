import { formatDateTime } from "../foundation/format";
import { useEffect, useState } from "react";
import { Alert, Button, Card, Flex, Form, Input, Segmented, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { LoginRequest, MfaMethod } from "../api/generated/control";
import { AuthApiError, userFacingError } from "../api/auth-api";
import { useAuth } from "../auth/AuthContext";
import { useAppLocale } from "../foundation/FrontendFoundationProvider";

const { Title, Paragraph, Text } = Typography;

export default function LoginPage() {
  const auth = useAuth();
  const { locale } = useAppLocale();
  const { t } = useTranslation();
  const [form] = Form.useForm<LoginRequest>();
  const [method, setMethod] = useState<MfaMethod>("totp");
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!auth.challenge) return;
    const expiresAt = Date.parse(auth.challenge.expires_at);
    let timer: number | undefined;
    const expire = () => {
      auth.setChallenge(null);
      setCode("");
      setError(t("auth.login.expired"));
    };
    const schedule = () => {
      const remaining = expiresAt - Date.now();
      if (!Number.isFinite(expiresAt) || remaining <= 0) {
        expire();
        return;
      }
      timer = window.setTimeout(schedule, Math.min(remaining, 60_000));
    };
    schedule();
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [auth.challenge, auth.setChallenge]);

  const submitLogin = async (input: LoginRequest) => {
    setBusy(true);
    setError("");
    try {
      const response = await auth.api.login(input);
      form.resetFields();
      auth.acceptLogin(response);
    } catch (cause) {
      if (cause instanceof AuthApiError && cause.detail.code === "challenge_expired") {
        auth.setChallenge(null);
        setCode("");
      }
      setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
    } finally {
      setBusy(false);
    }
  };

  const submitMfa = async () => {
    setBusy(true);
    setError("");
    try {
      const response = await auth.api.completeMfa({ method, code });
      setCode("");
      auth.acceptSession(response);
    } catch (cause) {
      if (cause instanceof AuthApiError && cause.detail.code === "challenge_expired") {
        auth.setChallenge(null);
        setCode("");
      }
      setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="centered-page" data-testid="login-page">
      <Card className="auth-card">
        <Title level={2}>{t("auth.login.title")}</Title>
        <Paragraph type="secondary">{t("auth.login.securityHint")}</Paragraph>
        {error && <Alert type="error" showIcon message={error} className="form-alert" />}
        {!auth.challenge ? (
          <Form form={form} layout="vertical" preserve={false} onFinish={submitLogin}>
            <Form.Item label={t("auth.login.loginName")} name="login_name" rules={[{ required: true }]}>
              <Input autoComplete="username" data-testid="login-login-name" />
            </Form.Item>
            <Form.Item label={t("auth.login.password")} name="password" rules={[{ required: true }]}>
              <Input.Password autoComplete="current-password" data-testid="login-password" />
            </Form.Item>
            <Flex vertical gap={12}>
              <Button data-testid="login-submit" type="primary" htmlType="submit" loading={busy}>{t("auth.login.continue")}</Button>
              <Button data-testid="login-activation-link" type="link" onClick={() => auth.navigate("activation")}>{t("auth.login.activationLink")}</Button>
            </Flex>
          </Form>
        ) : (
          <Flex vertical gap={16}>
            <Alert type="info" showIcon message={t("auth.login.mfaRequired")} description={t("auth.login.challengeValidUntil", { date: formatDateTime(auth.challenge.expires_at, locale) })} />
            {auth.challenge.methods.length > 1 && (
              <Segmented
                value={method}
                onChange={(value) => { setMethod(value as MfaMethod); setCode(""); }}
                data-testid="login-mfa-method"
                options={auth.challenge.methods.map((item) => ({ label: <span data-testid={`login-method-${item}`}>{item === "totp" ? t("auth.login.totpMethod") : t("auth.login.recoveryCode")}</span>, value: item }))}
              />
            )}
            <Input.Password
              aria-label={method === "totp" ? t("auth.login.totp") : t("auth.login.recoveryCode")}
              data-testid="login-mfa-code"
              value={code}
              onChange={(event) => setCode(event.target.value)}
              autoComplete="one-time-code"
              visibilityToggle={false}
            />
            <Button data-testid="login-mfa-submit" type="primary" loading={busy} disabled={code.length < 6} onClick={() => void submitMfa()}>{t("auth.login.verify")}</Button>
            <Button data-testid="login-mfa-back" type="link" onClick={() => { auth.setChallenge(null); setCode(""); setError(""); }}>{t("auth.login.backToPassword")}</Button>
            {method === "recovery_code" && <Text type="secondary">{t("auth.login.recoveryWarning")}</Text>}
          </Flex>
        )}
      </Card>
    </main>
  );
}

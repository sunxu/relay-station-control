import { useEffect, useState } from "react";
import { Alert, Button, Card, Flex, Form, Input, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { TotpEnrollment } from "../api/generated/control";
import { userFacingError } from "../api/auth-api";
import { useAuth } from "../auth/AuthContext";

const { Title, Paragraph, Text } = Typography;

export default function ActivationPage() {
  const auth = useAuth();
  const { t } = useTranslation();
  const [token, setToken] = useState("");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [enrollment, setEnrollment] = useState<TotpEnrollment | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (window.location.search || window.location.hash) window.history.replaceState(null, "", window.location.pathname);
    return () => {
      setToken("");
      setPassword("");
      setTotpCode("");
      setEnrollment(null);
    };
  }, []);

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const response = await auth.api.activate({ stage: "start", activation_token: token });
      if ("totp_enrollment" in response) setEnrollment(response.totp_enrollment);
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
      const response = await auth.api.activate({ stage: "complete", activation_token: token, password, totp_code: totpCode });
      if ("session" in response) {
        setToken("");
        setPassword("");
        setTotpCode("");
        setEnrollment(null);
        auth.acceptSession(response.session);
        auth.showRecoveryCodes(response.recovery_codes);
      }
    } catch (cause) {
      setError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="centered-page" data-testid="activation-page">
      <Card className="auth-card">
        <Title level={2}>{t("auth.activation.title")}</Title>
        <Paragraph type="secondary">{t("auth.activation.tokenHint")}</Paragraph>
        {error && <Alert type="error" showIcon message={error} className="form-alert" />}
        <Form layout="vertical" preserve={false} autoComplete="off">
          <Form.Item label={t("auth.activation.token")} required>
            <Input.Password value={token} onChange={(event) => setToken(event.target.value)} visibilityToggle={false} autoComplete="off" data-testid="activation-token" />
          </Form.Item>
          {!enrollment ? (
            <Button data-testid="activation-start" type="primary" loading={busy} disabled={token.length < 32} onClick={() => void start()}>{t("auth.activation.start")}</Button>
          ) : (
            <Flex vertical gap={16}>
              <div className="secret-panel">
                <Text copyable={{ text: enrollment.otpauth_uri }}>{t("auth.bootstrap.copyUri")}</Text>
                <Text type="secondary">{t("auth.activation.leaveHint")}</Text>
              </div>
              <Form.Item label={t("auth.activation.newPassword")} required>
                <Input.Password data-testid="activation-password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" />
              </Form.Item>
              <Form.Item label={t("auth.activation.totpCode")} required>
                <Input data-testid="activation-totp" value={totpCode} onChange={(event) => setTotpCode(event.target.value)} maxLength={6} inputMode="numeric" autoComplete="one-time-code" />
              </Form.Item>
              <Button data-testid="activation-complete" type="primary" loading={busy} disabled={password.length < 14 || !/^\d{6}$/.test(totpCode)} onClick={() => void complete()}>{t("auth.activation.complete")}</Button>
            </Flex>
          )}
          <Button data-testid="activation-back" type="link" onClick={() => auth.navigate("login")}>{t("auth.activation.backToLogin")}</Button>
        </Form>
      </Card>
    </main>
  );
}

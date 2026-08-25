import { useEffect, useState } from "react";
import { Alert, Button, Card, Flex, Form, Input, Typography } from "antd";
import type { TotpEnrollment } from "../api/generated/control";
import { userFacingError } from "../api/auth-api";
import { useAuth } from "../auth/AuthContext";

const { Title, Paragraph, Text } = Typography;

export default function ActivationPage() {
  const auth = useAuth();
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
      setError(userFacingError(cause));
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
      setError(userFacingError(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="centered-page" data-testid="activation-page">
      <Card className="auth-card">
        <Title level={2}>激活管理员账号</Title>
        <Paragraph type="secondary">请手工粘贴令牌。页面不会从 URL、历史记录或浏览器存储读取令牌。</Paragraph>
        {error && <Alert type="error" showIcon message={error} className="form-alert" />}
        <Form layout="vertical" preserve={false} autoComplete="off">
          <Form.Item label="一次性激活令牌" required>
            <Input.Password value={token} onChange={(event) => setToken(event.target.value)} visibilityToggle={false} autoComplete="off" data-testid="activation-token" />
          </Form.Item>
          {!enrollment ? (
            <Button type="primary" loading={busy} disabled={token.length < 32} onClick={() => void start()}>开始激活</Button>
          ) : (
            <Flex vertical gap={16}>
              <div className="secret-panel">
                <Text copyable={{ text: enrollment.otpauth_uri }}>复制 TOTP 配置 URI</Text>
                <Text type="secondary">离开此页后不会保留。</Text>
              </div>
              <Form.Item label="新密码" required>
                <Input.Password value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" />
              </Form.Item>
              <Form.Item label="TOTP 验证码" required>
                <Input value={totpCode} onChange={(event) => setTotpCode(event.target.value)} maxLength={6} inputMode="numeric" autoComplete="one-time-code" />
              </Form.Item>
              <Button type="primary" loading={busy} disabled={password.length < 14 || !/^\d{6}$/.test(totpCode)} onClick={() => void complete()}>完成激活</Button>
            </Flex>
          )}
          <Button type="link" onClick={() => auth.navigate("login")}>返回登录</Button>
        </Form>
      </Card>
    </main>
  );
}

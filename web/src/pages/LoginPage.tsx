import { useEffect, useState } from "react";
import { Alert, Button, Card, Flex, Form, Input, Segmented, Typography } from "antd";
import type { LoginRequest, MfaMethod } from "../api/generated/control";
import { AuthApiError, userFacingError } from "../api/auth-api";
import { useAuth } from "../auth/AuthContext";

const { Title, Paragraph, Text } = Typography;

export default function LoginPage() {
  const auth = useAuth();
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
      setError("验证已过期，请重新登录。");
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
      setError(userFacingError(cause));
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
      setError(userFacingError(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="centered-page" data-testid="login-page">
      <Card className="auth-card">
        <Title level={2}>管理员登录</Title>
        <Paragraph type="secondary">所有认证失败使用统一提示，不确认账号状态。</Paragraph>
        {error && <Alert type="error" showIcon message={error} className="form-alert" />}
        {!auth.challenge ? (
          <Form form={form} layout="vertical" preserve={false} onFinish={submitLogin}>
            <Form.Item label="登录名" name="login_name" rules={[{ required: true }]}>
              <Input autoComplete="username" />
            </Form.Item>
            <Form.Item label="密码" name="password" rules={[{ required: true }]}>
              <Input.Password autoComplete="current-password" />
            </Form.Item>
            <Flex vertical gap={12}>
              <Button type="primary" htmlType="submit" loading={busy}>继续</Button>
              <Button type="link" onClick={() => auth.navigate("activation")}>使用激活令牌设置新账号</Button>
            </Flex>
          </Form>
        ) : (
          <Flex vertical gap={16}>
            <Alert type="info" showIcon message="需要第二步验证" description={`挑战有效至 ${new Date(auth.challenge.expires_at).toLocaleTimeString()}`} />
            {auth.challenge.methods.length > 1 && (
              <Segmented
                value={method}
                onChange={(value) => { setMethod(value as MfaMethod); setCode(""); }}
                options={auth.challenge.methods.map((item) => ({ label: item === "totp" ? "TOTP" : "恢复码", value: item }))}
              />
            )}
            <Input.Password
              aria-label={method === "totp" ? "TOTP 验证码" : "恢复码"}
              value={code}
              onChange={(event) => setCode(event.target.value)}
              autoComplete="one-time-code"
              visibilityToggle={false}
            />
            <Button type="primary" loading={busy} disabled={code.length < 6} onClick={() => void submitMfa()}>验证并登录</Button>
            <Button type="link" onClick={() => { auth.setChallenge(null); setCode(""); setError(""); }}>返回密码登录</Button>
            {method === "recovery_code" && <Text type="secondary">恢复码成功使用后将立即失效；登录后请检查剩余数量。</Text>}
          </Flex>
        )}
      </Card>
    </main>
  );
}

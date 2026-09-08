import { formatDateTime } from "../time";
import { useEffect, useState } from "react";
import { Alert, Button, Card, Checkbox, Flex, List, Typography } from "antd";
import { useAuth } from "../auth/AuthContext";

const { Title, Paragraph, Text } = Typography;

export default function OneTimeMaterialPage() {
  const auth = useAuth();
  const [confirmed, setConfirmed] = useState(false);
  const material = auth.oneTime;

  useEffect(() => {
    const discard = () => auth.discardOneTime();
    const discardFromCache = (event: PageTransitionEvent) => {
      if (event.persisted) auth.discardOneTime();
    };
    window.addEventListener("pagehide", discard);
    window.addEventListener("beforeunload", discard);
    window.addEventListener("pageshow", discardFromCache);
    return () => {
      window.removeEventListener("pagehide", discard);
      window.removeEventListener("beforeunload", discard);
      window.removeEventListener("pageshow", discardFromCache);
      auth.discardOneTime();
    };
  }, [auth]);

  if (!material) return null;
  const recovery = material.kind === "recovery_codes";

  return (
    <main className="centered-page" data-testid="one-time-page">
      <Card className="auth-card one-time-card">
        <Title level={2}>{recovery ? "保存恢复码" : "复制激活令牌"}</Title>
        <Alert
          type="warning"
          showIcon
          message="只显示这一次"
          description={recovery ? "每个恢复码只能使用一次。请保存到密码管理器或离线安全位置。" : "令牌只应通过可信渠道交给对应管理员，禁止放入 URL。"}
        />
        {recovery ? (
          <List className="secret-list" bordered dataSource={material.values} renderItem={(value) => <List.Item><Text code>{value}</Text></List.Item>} />
        ) : (
          <div className="secret-panel">
            <Text code copyable={{ text: material.values[0] }}>{material.values[0]}</Text>
            {material.expiresAt && <Text type="secondary">有效至 {formatDateTime(material.expiresAt)}</Text>}
          </div>
        )}
        <Flex vertical gap={12}>
          <Checkbox checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)}>
            我已安全保存，理解离开后无法再次查看
          </Checkbox>
          <Button type="primary" disabled={!confirmed} onClick={auth.leaveOneTime}>确认并离开</Button>
          <Paragraph type="secondary">本页设置禁止缓存；离开时材料会从应用内存移除。</Paragraph>
        </Flex>
      </Card>
    </main>
  );
}

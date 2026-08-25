import { Alert, Card, Flex, Typography } from "antd";
import { useGetHealthz } from "./api/generated/control";

const { Title, Paragraph, Text } = Typography;

export default function App() {
  const health = useGetHealthz({
    query: {
      refetchInterval: 30_000,
      retry: 1,
    },
  });

  return (
    <main className="page">
      <Card className="status-card">
        <Flex vertical gap={16}>
          <div>
            <Title level={2}>Relay Station Control</Title>
            <Paragraph type="secondary">单环境管控中心技术验证</Paragraph>
          </div>
          {health.isPending && <Alert type="info" message="正在检查 Control 状态" showIcon />}
          {health.isError && <Alert type="error" message="Control API 不可用" showIcon />}
          {health.data && (
            <Alert
              type="success"
              message="Control API 正常"
              description={<Text>版本：{health.data.data.version}</Text>}
              showIcon
            />
          )}
        </Flex>
      </Card>
    </main>
  );
}

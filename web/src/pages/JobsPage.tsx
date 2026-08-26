import { Button, Flex, Typography } from "antd";
import { generatedJobApi } from "../api/job-api";
import { useAuth } from "../auth/AuthContext";
import { JobRegistryView } from "./JobRegistryView";

const { Title, Text } = Typography;

export default function JobsPage() {
  const auth = useAuth();
  return (
    <main className="management-page" data-testid="jobs-page">
      <Flex justify="space-between" align="center" wrap gap={16} className="management-header">
        <div>
          <Title level={2}>持久任务</Title>
          <Text type="secondary">PostgreSQL 真相源的只读运行视图</Text>
        </div>
        <Button onClick={() => auth.navigate("management")}>管理员控制台</Button>
      </Flex>
      <JobRegistryView api={generatedJobApi} onUnauthorized={auth.clearSession} />
    </main>
  );
}

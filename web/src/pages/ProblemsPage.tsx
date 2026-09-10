import { Button, Flex, Typography } from "antd";
import { generatedProblemAccountsApi } from "../api/problem-accounts-api";
import { useAuth } from "../auth/AuthContext";
import { ProblemsView } from "./ProblemsView";

export default function ProblemsPage() {
  const auth = useAuth();
  if (!auth.session) return null;
  return (
    <main className="management-page" data-testid="problems-page">
      <Flex justify="space-between" align="center" wrap gap={16} className="management-header">
        <div>
          <Typography.Title level={2}>Problems</Typography.Title>
          <Typography.Text type="secondary">已确认账号问题的只读运行观察</Typography.Text>
        </div>
        <Button onClick={() => auth.navigate("management")}>管理员控制台</Button>
      </Flex>
      <ProblemsView api={generatedProblemAccountsApi} csrfToken={auth.session.csrf_token} onUnauthorized={auth.clearSession} />
    </main>
  );
}

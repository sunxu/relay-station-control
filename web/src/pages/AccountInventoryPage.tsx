import { Button, Flex, Typography } from "antd";
import { generatedAccountInventoryApi } from "../api/account-inventory-api";
import { generatedAssetApi } from "../api/asset-api";
import { useAuth } from "../auth/AuthContext";
import { AccountInventoryView } from "./AccountInventoryView";

const { Title, Text } = Typography;

export default function AccountInventoryPage() {
  const auth = useAuth();
  if (!auth.session) return null;

  return (
    <main className="management-page account-inventory-page" data-testid="account-inventory-page">
      <Flex justify="space-between" align="center" wrap gap={16} className="management-header">
        <div>
          <Title level={2}>账号清单</Title>
          <Text type="secondary">PostgreSQL 当前生命周期只读视图</Text>
        </div>
        <Button onClick={() => auth.navigate("management")}>管理员控制台</Button>
      </Flex>
      <AccountInventoryView
        api={generatedAccountInventoryApi}
        assetApi={generatedAssetApi}
        csrfToken={auth.session.csrf_token}
        onUnauthorized={auth.clearSession}
      />
    </main>
  );
}

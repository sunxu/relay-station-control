import { Button, Flex, Typography } from "antd";
import { generatedAssetApi } from "../api/asset-api";
import { useAuth } from "../auth/AuthContext";
import { AssetRegistryView } from "./AssetRegistryView";

const { Title, Text } = Typography;

export default function AssetsPage() {
  const auth = useAuth();

  return (
    <main className="management-page" data-testid="assets-page">
      <Flex justify="space-between" align="center" wrap gap={16} className="management-header">
        <div>
          <Title level={2}>资产注册表</Title>
          <Text type="secondary">只读视图</Text>
        </div>
        <Button onClick={() => auth.navigate("management")}>管理员控制台</Button>
      </Flex>
      <AssetRegistryView api={generatedAssetApi} onUnauthorized={auth.clearSession} />
    </main>
  );
}

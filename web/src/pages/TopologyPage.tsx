import { Button, Flex, Typography } from "antd";
import { generatedAssetApi } from "../api/asset-api";
import { generatedTopologyApi } from "../api/topology-api";
import { useAuth } from "../auth/AuthContext";
import { TopologyView } from "./TopologyView";

export default function TopologyPage() {
  const auth = useAuth();
  if (!auth.session) return null;
  return <main className="management-page" data-testid="topology-page">
    <Flex justify="space-between" align="center" wrap gap={16} className="management-header">
      <div><Typography.Title level={2}>Node Topology</Typography.Title><Typography.Text type="secondary">Node 及其只读关联观察</Typography.Text></div>
      <Button onClick={() => auth.navigate("management")}>管理员控制台</Button>
    </Flex>
    <TopologyView api={generatedTopologyApi} assetApi={generatedAssetApi} initialInstanceId={new URLSearchParams(window.location.search).get("instance_id") ?? undefined} onUnauthorized={auth.clearSession} />
  </main>;
}

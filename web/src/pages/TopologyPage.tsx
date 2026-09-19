import { generatedAccountInventoryApi } from "../api/account-inventory-api";
import { generatedAssetApi } from "../api/asset-api";
import { generatedTopologyApi } from "../api/topology-api";
import { generatedAccountOperationsApi } from "../api/account-operations-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { TopologyView } from "./TopologyView";

export default function TopologyPage() {
  const auth = useAuth();
  const { t } = useTranslation();
  if (!auth.session) return null;
  return <PageShell className="management-page" testId="topology-page" title={t("topology.title")} description={t("topology.description")} actions={<Button onClick={() => auth.navigate("management")}>{t("topology.management")}</Button>}>
    <TopologyView api={generatedTopologyApi} assetApi={generatedAssetApi} inventoryApi={generatedAccountInventoryApi} accountOperationsApi={generatedAccountOperationsApi} initialInstanceId={new URLSearchParams(window.location.search).get("instance_id") ?? undefined} csrfToken={auth.session.csrf_token} onUnauthorized={auth.clearSession} />
  </PageShell>;
}
import { Button } from "antd";

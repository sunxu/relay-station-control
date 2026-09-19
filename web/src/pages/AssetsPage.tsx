import { Button } from "antd";
import { generatedAssetApi } from "../api/asset-api";
import { generatedGatewayAdminApi } from "../api/gateway-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { AssetRegistryView } from "./AssetRegistryView";

export default function AssetsPage() {
  const auth = useAuth();
  const { t } = useTranslation();

  return (
    <PageShell
      className="management-page"
      testId="assets-page"
      title={t("assets.title")}
      description={t("assets.description")}
      actions={<Button onClick={() => auth.navigate("management")}>{t("assets.management")}</Button>}
    >
      <AssetRegistryView api={generatedAssetApi} gatewayApi={generatedGatewayAdminApi} csrfToken={auth.session?.csrf_token ?? ""} onUnauthorized={auth.clearSession} />
    </PageShell>
  );
}

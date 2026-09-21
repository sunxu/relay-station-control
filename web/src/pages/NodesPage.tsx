import { generatedAssetApi } from "../api/asset-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { AssetRegistryView } from "./AssetRegistryView";

export default function NodesPage() {
  const auth = useAuth();
  const { t } = useTranslation();

  return (
    <PageShell
      className="management-page"
      testId="nodes-page"
      title={t("nodes.title")}
      description={t("nodes.description")}
    >
      <AssetRegistryView
        api={generatedAssetApi}
        csrfToken={auth.session?.csrf_token ?? ""}
        onUnauthorized={auth.clearSession}
        mode="nodes"
      />
    </PageShell>
  );
}

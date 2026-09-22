import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { generatedAccountInventoryApi } from "../api/account-inventory-api";
import { generatedAssetApi } from "../api/asset-api";
import { generatedTopologyApi } from "../api/topology-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { MonitoringView } from "./MonitoringView";

export default function MonitoringPage() {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const expireSession = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
    auth.clearSession();
  }, [auth.clearSession, queryClient]);
  if (!auth.session) return null;
  return <PageShell className="management-page" testId="monitoring-page" title={t("monitoring.title")} description={t("monitoring.description")}>
    <MonitoringView api={generatedTopologyApi} assetApi={generatedAssetApi} inventoryApi={generatedAccountInventoryApi} initialInstanceId={new URLSearchParams(window.location.search).get("instance_id") ?? undefined} csrfToken={auth.session.csrf_token} onUnauthorized={expireSession} onNavigate={(route) => auth.navigate(route)} />
  </PageShell>;
}

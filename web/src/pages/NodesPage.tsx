import { generatedAssetApi } from "../api/asset-api";
import { useAuth } from "../auth/AuthContext";
import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { NodeManagementView } from "./NodeManagementView";

export default function NodesPage() {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const expireSession = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
    auth.clearSession();
  }, [auth.clearSession, queryClient]);

  return (
    <PageShell
      className="management-page"
      testId="nodes-page"
      title={t("nodes.title")}
      description={t("nodes.description")}
    >
      <NodeManagementView
        api={generatedAssetApi}
        csrfToken={auth.session?.csrf_token ?? ""}
        onUnauthorized={expireSession}
      />
    </PageShell>
  );
}

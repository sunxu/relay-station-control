import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { generatedJobApi } from "../api/job-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { JobRegistryView } from "./JobRegistryView";

export default function OperationsPage() {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const expireSession = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
    auth.clearSession();
  }, [auth.clearSession, queryClient]);

  return (
    <PageShell className="management-page" testId="operations-page" title={t("operations.title")} description={t("operations.description")}>
      <JobRegistryView api={generatedJobApi} onUnauthorized={expireSession} />
    </PageShell>
  );
}

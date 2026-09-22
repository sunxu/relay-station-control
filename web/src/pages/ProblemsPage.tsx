import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { generatedProblemAccountsApi } from "../api/problem-accounts-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { ProblemsView } from "./ProblemsView";

export default function ProblemsPage() {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const expireSession = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
    auth.clearSession();
  }, [auth, queryClient]);
  if (!auth.session) return null;
  return (
    <PageShell className="management-page" testId="problems-page" title={t("problems.title")} description={t("problems.description")}>
      <ProblemsView api={generatedProblemAccountsApi} csrfToken={auth.session.csrf_token} onUnauthorized={expireSession} />
    </PageShell>
  );
}

import { Button } from "antd";
import { generatedProblemAccountsApi } from "../api/problem-accounts-api";
import { useAuth } from "../auth/AuthContext";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { ProblemsView } from "./ProblemsView";

export default function ProblemsPage() {
  const auth = useAuth();
  const { t } = useTranslation();
  if (!auth.session) return null;
  return (
    <PageShell className="management-page" testId="problems-page" title={t("problems.title")} description={t("problems.description")} actions={<Button onClick={() => auth.navigate("management")}>{t("problems.management")}</Button>}>
      <ProblemsView api={generatedProblemAccountsApi} csrfToken={auth.session.csrf_token} onUnauthorized={auth.clearSession} />
    </PageShell>
  );
}

import { generatedJobApi } from "../api/job-api";
import { useAuth } from "../auth/AuthContext";
import { Button } from "antd";
import { PageShell } from "../foundation/PageShell";
import { useTranslation } from "react-i18next";
import { JobRegistryView } from "./JobRegistryView";

export default function JobsPage() {
  const auth = useAuth();
  const { t } = useTranslation();
  return (
    <PageShell className="management-page" testId="jobs-page" title={t("jobs.title")} description={t("jobs.description")} actions={<Button onClick={() => auth.navigate("management")}>{t("jobs.management")}</Button>}>
      <JobRegistryView api={generatedJobApi} onUnauthorized={auth.clearSession} />
    </PageShell>
  );
}

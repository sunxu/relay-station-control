import { lazy, Suspense } from "react";
import { Alert, Card, Spin } from "antd";
import { useTranslation } from "react-i18next";
import type { AuthApi } from "./api/auth-api";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { LocaleSwitcher } from "./foundation/LocaleSwitcher";
import { AppShell } from "./foundation/AppShell";
import { FoundationPage, type FoundationPlaceholderRoute } from "./foundation/FoundationPage";
import { isAuthenticatedRoute, type AuthenticatedRoute } from "./foundation/navigation";

const BootstrapPage = lazy(() => import("./pages/BootstrapPage"));
const LoginPage = lazy(() => import("./pages/LoginPage"));
const ActivationPage = lazy(() => import("./pages/ActivationPage"));
const ManagementPage = lazy(() => import("./pages/ManagementPage"));
const AssetsPage = lazy(() => import("./pages/AssetsPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const TopologyPage = lazy(() => import("./pages/TopologyPage"));
const ProblemsPage = lazy(() => import("./pages/ProblemsPage"));
const OneTimeMaterialPage = lazy(() => import("./pages/OneTimeMaterialPage"));
const DashboardPage = lazy(() => import("./pages/DashboardPage"));

function AuthShell() {
  const auth = useAuth();
  const { t } = useTranslation();
  const localeControl = auth.route === "management" || auth.route === "settings" ? null : <LocaleSwitcher />;

  if (auth.route === "loading") {
    return <>{localeControl}<main className="centered-page" data-testid="auth-loading"><Spin size="large" tip={t("common.shell.authLoading")} /></main></>;
  }
  if (auth.route === "unavailable") {
    return (
      <>{localeControl}<main className="centered-page" data-testid="auth-unavailable">
        <Card className="auth-card"><Alert type="error" showIcon message={t("common.shell.authUnavailable")} description={t("common.shell.authUnavailableDescription")} /></Card>
      </main></>
    );
  }

  const authenticated = isAuthenticatedRoute(auth.route);
  const content = <Suspense fallback={<main className="centered-page" data-testid="route-loading"><Spin size="large" tip={t("common.shell.routeLoading")} /></main>}>
      {auth.route === "bootstrap" && <BootstrapPage />}
      {auth.route === "login" && <LoginPage />}
      {auth.route === "activation" && <ActivationPage />}
      {(auth.route === "management" || auth.route === "settings") && <ManagementPage />}
      {auth.route === "assets" && <AssetsPage />}
      {auth.route === "jobs" && <JobsPage />}
      {auth.route === "topology" && <TopologyPage />}
      {auth.route === "problems" && <ProblemsPage />}
      {auth.route === "dashboard" && <DashboardPage />}
      {authenticated && ["accounts", "nodes", "operations", "monitoring"].includes(auth.route) && <FoundationPage route={auth.route as FoundationPlaceholderRoute} />}
      {auth.route === "one-time" && <OneTimeMaterialPage />}
    </Suspense>;

  if (authenticated) return <AppShell route={auth.route as AuthenticatedRoute}>{content}</AppShell>;
  return <>{localeControl}{content}</>;
}

export default function App({ api }: { api?: AuthApi }) {
  return <AuthProvider api={api}><AuthShell /></AuthProvider>;
}

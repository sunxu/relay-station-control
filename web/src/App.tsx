import { lazy, Suspense } from "react";
import { Alert, Card, Spin } from "antd";
import type { AuthApi } from "./api/auth-api";
import { AuthProvider, useAuth } from "./auth/AuthContext";

const BootstrapPage = lazy(() => import("./pages/BootstrapPage"));
const LoginPage = lazy(() => import("./pages/LoginPage"));
const ActivationPage = lazy(() => import("./pages/ActivationPage"));
const ManagementPage = lazy(() => import("./pages/ManagementPage"));
const OneTimeMaterialPage = lazy(() => import("./pages/OneTimeMaterialPage"));

function AuthShell() {
  const auth = useAuth();

  if (auth.route === "loading") {
    return <main className="centered-page" data-testid="auth-loading"><Spin size="large" tip="正在读取安全状态" /></main>;
  }
  if (auth.route === "unavailable") {
    return (
      <main className="centered-page" data-testid="auth-unavailable">
        <Card className="auth-card"><Alert type="error" showIcon message="Control 认证服务暂时不可用" description="管理面已安全关闭；请检查 Control 与 PostgreSQL 状态。" /></Card>
      </main>
    );
  }

  return (
    <Suspense fallback={<main className="centered-page" data-testid="route-loading"><Spin size="large" /></main>}>
      {auth.route === "bootstrap" && <BootstrapPage />}
      {auth.route === "login" && <LoginPage />}
      {auth.route === "activation" && <ActivationPage />}
      {auth.route === "management" && <ManagementPage />}
      {auth.route === "one-time" && <OneTimeMaterialPage />}
    </Suspense>
  );
}

export default function App({ api }: { api?: AuthApi }) {
  return <AuthProvider api={api}><AuthShell /></AuthProvider>;
}

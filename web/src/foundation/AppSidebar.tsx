import { DashboardOutlined, DeploymentUnitOutlined, LineChartOutlined, SettingOutlined, ThunderboltOutlined, UserOutlined, WarningOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { AuthenticatedRoute } from "./navigation";
import { primaryNavigation } from "./navigation";
import { useAuth } from "../auth/AuthContext";

export function AppSidebar({ route }: { route: AuthenticatedRoute }) {
  const auth = useAuth();
  const { t } = useTranslation();
  const currentRoute = route === "management" ? "settings" : route === "jobs" ? "operations" : route === "topology" ? "monitoring" : route;
  const icons = {
    dashboard: DashboardOutlined,
    accounts: UserOutlined,
    nodes: DeploymentUnitOutlined,
    operations: ThunderboltOutlined,
    monitoring: LineChartOutlined,
    problems: WarningOutlined,
    settings: SettingOutlined,
  } as const;

  return (
    <aside className="app-sidebar" aria-label={t("common.shell.sidebarLabel")} data-testid="app-sidebar">
      <div className="app-sidebar-brand">
        <strong>Relay Station</strong>
        <span>{t("common.shell.controlConsole")}</span>
      </div>
      <nav className="app-sidebar-nav">
        {primaryNavigation.map((entry) => (
          <button
            key={entry.id}
            type="button"
            className={currentRoute === entry.route ? "app-sidebar-item active" : "app-sidebar-item"}
            aria-current={currentRoute === entry.route ? "page" : undefined}
            data-testid={entry.testId}
            onClick={() => auth.navigate(entry.route)}
          >
            <span data-testid={`sidebar-icon-${entry.id}`} aria-hidden="true">{(() => { const Icon = icons[entry.id as keyof typeof icons]; return <Icon />; })()}</span>
            {t(entry.labelKey)}
          </button>
        ))}
      </nav>
    </aside>
  );
}

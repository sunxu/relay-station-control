import { useTranslation } from "react-i18next";
import type { AuthenticatedRoute } from "./navigation";
import { primaryNavigation } from "./navigation";
import { useAuth } from "../auth/AuthContext";

export function AppSidebar({ route }: { route: AuthenticatedRoute }) {
  const auth = useAuth();
  const { t } = useTranslation();
  const currentRoute = route === "management" ? "settings" : route;

  return (
    <aside className="app-sidebar" aria-label={t("common.shell.sidebarLabel")} data-testid="app-sidebar">
      <div className="app-sidebar-brand">Relay Station</div>
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
            {t(entry.labelKey)}
          </button>
        ))}
      </nav>
    </aside>
  );
}

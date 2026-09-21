import { useTranslation } from "react-i18next";
import type { NavigationRoute } from "./navigation";
import { PageShell } from "./PageShell";

const pageKeys = {
  dashboard: ["dashboard.title", "dashboard.description"],
  accounts: ["accounts.title", "accounts.description"],
  nodes: ["nodes.title", "nodes.description"],
  operations: ["operations.title", "operations.description"],
  monitoring: ["monitoring.title", "monitoring.description"],
  problems: ["problems.title", "problems.description"],
  settings: ["settings.title", "settings.description"],
} as const;

export function FoundationPage({ route }: { route: NavigationRoute }) {
  const { t } = useTranslation();
  const [titleKey, descriptionKey] = pageKeys[route];
  return (
    <PageShell className="foundation-page" testId={`${route}-page`} title={t(titleKey)} description={t(descriptionKey)}>
      <div data-testid="foundation-placeholder" />
    </PageShell>
  );
}

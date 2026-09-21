import { useTranslation } from "react-i18next";
import type { TranslationKey } from "./resources";
import type { NavigationRoute } from "./navigation";
import { PageShell } from "./PageShell";

export type FoundationPlaceholderRoute = Exclude<NavigationRoute, "problems" | "settings">;

const pageKeys: Record<FoundationPlaceholderRoute, readonly [TranslationKey, TranslationKey]> = {
  dashboard: ["dashboard.title", "dashboard.description"],
  accounts: ["accounts.title", "accounts.description"],
  nodes: ["nodes.title", "nodes.description"],
  operations: ["operations.title", "operations.description"],
  monitoring: ["monitoring.title", "monitoring.description"],
} as const;

export function FoundationPage({ route }: { route: FoundationPlaceholderRoute }) {
  const { t } = useTranslation();
  const [titleKey, descriptionKey] = pageKeys[route];
  return (
    <PageShell className="foundation-page" testId={`${route}-page`} title={t(titleKey)} description={t(descriptionKey)}>
      <div data-testid="foundation-placeholder" />
    </PageShell>
  );
}

import type { PropsWithChildren } from "react";
import type { AuthenticatedRoute } from "./navigation";
import { AppSidebar } from "./AppSidebar";
import { GlobalHeader } from "./GlobalHeader";

export function AppShell({ route, children }: PropsWithChildren<{ route: AuthenticatedRoute }>) {
  return (
    <div className="app-shell" data-testid="app-shell">
      <AppSidebar route={route} />
      <div className="app-shell-main">
        <GlobalHeader />
        <div className="app-shell-content">{children}</div>
      </div>
    </div>
  );
}

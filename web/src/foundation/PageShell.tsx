import type { PropsWithChildren } from "react";
import { PageHeader, type PageHeaderProps } from "./PageHeader";

export type PageShellProps = PropsWithChildren<PageHeaderProps>;

export function PageShell({ title, description, actions, children }: PageShellProps) {
  return (
    <main className="page-shell">
      <PageHeader title={title} description={description} actions={actions} />
      <div className="page-shell-content">{children}</div>
    </main>
  );
}

import type { PropsWithChildren, ReactNode } from "react";
import { PageHeader, type PageHeaderProps } from "./PageHeader";

export type PageShellProps = PropsWithChildren<PageHeaderProps & { className?: string; testId?: string; breadcrumb?: ReactNode }>;

export function PageShell({ title, description, actions, breadcrumb, className, testId, children }: PageShellProps) {
  return (
    <main className={className ? `page-shell ${className}` : "page-shell"} data-testid={testId}>
      <PageHeader title={title} description={description} actions={actions} breadcrumb={breadcrumb} />
      <div className="page-shell-content">{children}</div>
    </main>
  );
}

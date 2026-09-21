import type { ReactNode } from "react";

export type PageHeaderProps = {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
};

export function PageHeader({ title, description, actions, breadcrumb }: PageHeaderProps) {
  return (
    <header className="page-header">
      {breadcrumb ? <div className="page-header-breadcrumb">{breadcrumb}</div> : null}
      <div className="page-header-content">
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-header-actions">{actions}</div> : null}
    </header>
  );
}

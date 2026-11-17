import type { ReactNode } from "react";
import { WorkspaceSectionNav } from "../shell/WorkspaceSectionNav";

// Shared operational page heading.
export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <>
      <header className="reborn-page-header">
        <div className="reborn-page-heading">
          <h1>{title}</h1>
          {subtitle ? <p>{subtitle}</p> : null}
        </div>
        {actions ? <div className="page-header-actions">{actions}</div> : null}
      </header>
      <WorkspaceSectionNav />
    </>
  );
}

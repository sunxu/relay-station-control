import type { ReactNode } from "react";

export type ReadStateStatus = "loading" | "error" | "empty" | "content";

export type ReadStateProps = {
  status: ReadStateStatus;
  message?: ReactNode;
  onRetry?: () => void;
  children?: ReactNode;
};

export function ReadState({ status, message, onRetry, children }: ReadStateProps) {
  if (status === "content") return <>{children}</>;

  if (status === "error") {
    return (
      <div className="read-state read-state-error" role="alert">
        <p>{message ?? "Unavailable"}</p>
        {onRetry ? <button type="button" onClick={onRetry}>Retry</button> : null}
      </div>
    );
  }

  return (
    <div className={`read-state read-state-${status}`} role="status" aria-live="polite">
      {message ?? (status === "loading" ? "Loading" : "No records")}
    </div>
  );
}

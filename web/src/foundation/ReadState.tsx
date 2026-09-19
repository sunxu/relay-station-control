import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export type ReadStateStatus = "loading" | "error" | "empty" | "content";

export type ReadStateProps = {
  status: ReadStateStatus;
  message?: ReactNode;
  onRetry?: () => void;
  children?: ReactNode;
};

export function ReadState({ status, message, onRetry, children }: ReadStateProps) {
  const { t } = useTranslation();
  if (status === "content") return <>{children}</>;

  if (status === "error") {
    return (
      <div className="read-state read-state-error" role="alert">
        <p>{message ?? t("common.readState.unavailable")}</p>
        {onRetry ? <button type="button" onClick={onRetry}>{t("common.readState.retry")}</button> : null}
      </div>
    );
  }

  return (
    <div className={`read-state read-state-${status}`} role="status" aria-live="polite">
      {message ?? t(status === "loading" ? "common.readState.loading" : "common.readState.empty")}
    </div>
  );
}

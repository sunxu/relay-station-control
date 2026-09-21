import { Input } from "antd";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthApiError, userFacingError } from "../api/auth-api";
import { handleSessionError, useAuth } from "../auth/AuthContext";
import { LocaleSwitcher } from "./LocaleSwitcher";
import { searchNavigation } from "./navigation";

export function GlobalHeader() {
  const auth = useAuth();
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const results = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return [];
    return searchNavigation.filter((entry) => {
      const label = t(entry.labelKey).toLocaleLowerCase();
      return label.includes(normalized) || entry.path.includes(normalized);
    });
  }, [query, t]);

  const logout = async () => {
    setLogoutError(null);
    try {
      await auth.api.logout(auth.session?.csrf_token ?? "");
      auth.clearSession();
    } catch (cause) {
      if (cause instanceof AuthApiError && cause.detail.code === "csrf_invalid") {
        await auth.refreshSession().catch(() => null);
      } else {
        handleSessionError(cause, auth.clearSession);
        if (cause instanceof AuthApiError && cause.status === 401) return;
      }
      setLogoutError(userFacingError(cause, (key, requestId) => t(key, { requestId: requestId ?? "" })));
    }
  };

  return (
    <header className="global-header" data-testid="global-header">
      <div className="global-header-search">
        <Input
          aria-label={t("common.shell.search")}
          data-testid="navigation-search-input"
          placeholder={t("common.shell.searchPlaceholder")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        {results.length > 0 ? (
          <div className="navigation-search-results" data-testid="navigation-search-results">
            {results.map((entry) => (
              <button
                key={entry.id}
                type="button"
                data-testid={entry.testId === "search-result-assets" ? entry.testId : `search-result-${entry.id}`}
                onClick={() => { auth.navigate(entry.route); setQuery(""); }}
              >
                {t(entry.labelKey)}
              </button>
            ))}
          </div>
        ) : null}
      </div>
      <div className="global-header-actions">
        {logoutError ? <div role="alert" data-testid="global-logout-error">{logoutError}</div> : null}
        <span data-testid="admin-identity">{auth.session?.administrator.display_name} · {auth.session?.administrator.login_name}</span>
        <LocaleSwitcher />
        <button type="button" data-testid="global-logout" onClick={() => void logout()}>{t("common.shell.logout")}</button>
      </div>
    </header>
  );
}

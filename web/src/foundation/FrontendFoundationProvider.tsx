import { ConfigProvider } from "antd";
import { I18nextProvider } from "react-i18next";
import { createContext, useCallback, useContext, useMemo, useState, type PropsWithChildren } from "react";
import { createAppI18n } from "./i18n";
import { AppLocale, persistExplicitLocale, resolveInitialLocale } from "./locale";
import { antdLocales, foundationTheme } from "./theme";

export type FrontendFoundationProviderProps = PropsWithChildren<{
  initialLocale?: AppLocale;
}>;

type AppLocaleContextValue = {
  locale: AppLocale;
  setLocale: (locale: AppLocale) => void;
};

const AppLocaleContext = createContext<AppLocaleContextValue | null>(null);

export function useAppLocale(): AppLocaleContextValue {
  const context = useContext(AppLocaleContext);
  if (!context) throw new Error("useAppLocale must be used inside FrontendFoundationProvider");
  return context;
}

export function useOptionalAppLocale(): AppLocaleContextValue | null {
  return useContext(AppLocaleContext);
}

export function FrontendFoundationProvider({
  children,
  initialLocale,
}: FrontendFoundationProviderProps) {
  const [locale, setLocaleState] = useState<AppLocale>(() => initialLocale ?? resolveInitialLocale());
  const setLocale = useCallback((nextLocale: AppLocale) => {
    persistExplicitLocale(nextLocale);
    setLocaleState(nextLocale);
  }, []);
  const localeContext = useMemo(() => ({ locale, setLocale }), [locale, setLocale]);
  const i18n = useMemo(() => createAppI18n(locale), [locale]);

  return (
    <AppLocaleContext.Provider value={localeContext}>
      <I18nextProvider i18n={i18n}>
        <ConfigProvider locale={antdLocales[locale]} theme={foundationTheme}>
          {children}
        </ConfigProvider>
      </I18nextProvider>
    </AppLocaleContext.Provider>
  );
}

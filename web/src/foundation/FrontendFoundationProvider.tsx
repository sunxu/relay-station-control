import { ConfigProvider } from "antd";
import { I18nextProvider } from "react-i18next";
import { useMemo, type PropsWithChildren } from "react";
import { createAppI18n } from "./i18n";
import { AppLocale, resolveInitialLocale } from "./locale";
import { antdLocales, foundationTheme } from "./theme";

export type FrontendFoundationProviderProps = PropsWithChildren<{
  initialLocale?: AppLocale;
}>;

export function FrontendFoundationProvider({
  children,
  initialLocale = resolveInitialLocale(),
}: FrontendFoundationProviderProps) {
  const i18n = useMemo(() => createAppI18n(initialLocale), [initialLocale]);

  return (
    <I18nextProvider i18n={i18n}>
      <ConfigProvider locale={antdLocales[initialLocale]} theme={foundationTheme}>
        {children}
      </ConfigProvider>
    </I18nextProvider>
  );
}

import { ConfigProvider } from "antd";
import { type ReactNode, useState } from "react";
import { I18nextProvider } from "react-i18next";
import { createAppI18n } from "./i18n";
import type { AppLocale } from "./locale";
import { antdLocaleFor, appTheme } from "./theme";

export interface FrontendFoundationProviderProps {
  children: ReactNode;
  initialLocale: AppLocale;
}

export function FrontendFoundationProvider({ children, initialLocale }: FrontendFoundationProviderProps) {
  const [locale] = useState<AppLocale>(initialLocale);
  const [i18n] = useState(() => createAppI18n(initialLocale));

  return (
    <I18nextProvider i18n={i18n}>
      <ConfigProvider locale={antdLocaleFor(locale)} theme={appTheme}>
        {children}
      </ConfigProvider>
    </I18nextProvider>
  );
}

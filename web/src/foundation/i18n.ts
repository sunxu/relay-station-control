import { createInstance, type i18n } from "i18next";
import { APP_LOCALES, DEFAULT_APP_LOCALE, type AppLocale } from "./locale";
import { appResources } from "./resources";

export function createAppI18n(locale: AppLocale): i18n {
  const instance = createInstance();
  void instance.init({
    resources: appResources,
    lng: locale,
    fallbackLng: DEFAULT_APP_LOCALE,
    supportedLngs: [...APP_LOCALES],
    initAsync: false,
    interpolation: {
      escapeValue: false,
    },
  });
  return instance;
}

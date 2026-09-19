import { createInstance, type i18n } from "i18next";
import { AppLocale } from "./locale";
import { resources } from "./resources";

export const supportedLocales: AppLocale[] = ["zh-CN", "en"];

export function createAppI18n(locale: AppLocale): i18n {
  const instance = createInstance();
  instance.init({
    lng: locale,
    fallbackLng: "zh-CN",
    supportedLngs: supportedLocales,
    resources,
    interpolation: {
      escapeValue: false,
    },
  });
  return instance;
}

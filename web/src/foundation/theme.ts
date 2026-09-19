import type { ThemeConfig } from "antd";
import enUS from "antd/es/locale/en_US";
import zhCN from "antd/es/locale/zh_CN";
import type { AppLocale } from "./locale";

export const appTheme: ThemeConfig = {};

export function antdLocaleFor(locale: AppLocale) {
  return locale === "en" ? enUS : zhCN;
}

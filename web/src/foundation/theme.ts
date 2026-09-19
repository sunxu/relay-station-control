import type { ThemeConfig } from "antd";
import enUS from "antd/es/locale/en_US";
import zhCN from "antd/es/locale/zh_CN";
import { AppLocale } from "./locale";

export const antdLocales = {
  "zh-CN": zhCN,
  en: enUS,
} satisfies Record<AppLocale, typeof zhCN>;

export const foundationTheme: ThemeConfig = {};

import type { ThemeConfig } from "antd";
import enUS from "antd/es/locale/en_US";
import zhCN from "antd/es/locale/zh_CN";
import { AppLocale } from "./locale";

export const antdLocales = {
  "zh-CN": zhCN,
  en: enUS,
} satisfies Record<AppLocale, typeof zhCN>;

export const foundationTheme: ThemeConfig = {
  token: {
    colorPrimary: "#2563eb",
    colorBgLayout: "#f4f7fb",
    colorBgContainer: "#ffffff",
    colorBorder: "#d9e2ef",
    borderRadius: 8,
    controlHeight: 40,
    fontSize: 14,
    colorSuccess: "#16835b",
    colorWarning: "#b7791f",
    colorError: "#c53030",
  },
  components: {
    Layout: { headerHeight: 60 },
    Table: { cellPaddingBlockSM: 10, cellPaddingInlineSM: 12 },
  },
};

export type AppLocale = "zh-CN" | "en";

const supportedBrowserLocale = (value: string | null | undefined): AppLocale | undefined => {
  if (!value) return undefined;

  const locale = value.trim().toLowerCase().replaceAll("_", "-");
  if (locale === "zh" || locale.startsWith("zh-")) return "zh-CN";
  if (locale === "en" || locale.startsWith("en-")) return "en";
  return undefined;
};

export function isAppLocale(value: unknown): value is AppLocale {
  return value === "zh-CN" || value === "en";
}

export function normalizeBrowserLocale(value: string | null | undefined): AppLocale {
  return supportedBrowserLocale(value) ?? "zh-CN";
}

export function resolveInitialLocale(
  locales: readonly (string | null | undefined)[] = typeof navigator === "undefined"
    ? []
    : [navigator.language, ...navigator.languages],
): AppLocale {
  for (const locale of locales) {
    const supported = supportedBrowserLocale(locale);
    if (supported) return supported;
  }
  return "zh-CN";
}

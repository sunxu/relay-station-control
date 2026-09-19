export const APP_LOCALES = ["zh-CN", "en"] as const;

export type AppLocale = (typeof APP_LOCALES)[number];

export const DEFAULT_APP_LOCALE: AppLocale = "zh-CN";

export function isAppLocale(value: string | null | undefined): value is AppLocale {
  return value === "zh-CN" || value === "en";
}

export function normalizeBrowserLocale(value: string | null | undefined): AppLocale | null {
  const normalized = value?.trim().toLowerCase();
  if (!normalized) return null;
  if (normalized === "zh" || normalized.startsWith("zh-")) return "zh-CN";
  if (normalized === "en" || normalized.startsWith("en-")) return "en";
  return null;
}

export function resolveBrowserLocale(candidates?: readonly string[]): AppLocale {
  const browserCandidates = candidates ?? (
    typeof navigator === "undefined"
      ? []
      : navigator.languages?.length
        ? navigator.languages
        : [navigator.language]
  );

  for (const candidate of browserCandidates) {
    const locale = normalizeBrowserLocale(candidate);
    if (locale) return locale;
  }

  return DEFAULT_APP_LOCALE;
}

export function resolveInitialLocale(): AppLocale {
  return resolveBrowserLocale();
}

export type AppLocale = "zh-CN" | "en";
export const LOCALE_STORAGE_KEY = "relay-control.locale";

type LocaleStorage = Pick<Storage, "getItem" | "setItem">;

function browserStorage(): LocaleStorage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

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

export function readPersistedLocale(storage: LocaleStorage | undefined = browserStorage()): AppLocale | undefined {
  try {
    const value = storage?.getItem(LOCALE_STORAGE_KEY);
    return isAppLocale(value) ? value : undefined;
  } catch {
    return undefined;
  }
}

export function persistExplicitLocale(locale: AppLocale, storage: LocaleStorage | undefined = browserStorage()): void {
  try {
    storage?.setItem(LOCALE_STORAGE_KEY, locale);
  } catch {
    // A storage failure must not prevent the current session from switching.
  }
}

export function resolveInitialLocale(
  locales: readonly (string | null | undefined)[] = typeof navigator === "undefined"
    ? []
    : [navigator.language, ...navigator.languages],
  storage: LocaleStorage | undefined = browserStorage(),
): AppLocale {
  const persisted = readPersistedLocale(storage);
  if (persisted) return persisted;

  for (const locale of locales) {
    const supported = supportedBrowserLocale(locale);
    if (supported) return supported;
  }
  return "zh-CN";
}

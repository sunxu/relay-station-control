import { describe, expect, it } from "vitest";
import {
  LOCALE_STORAGE_KEY,
  isAppLocale,
  normalizeBrowserLocale,
  persistExplicitLocale,
  readPersistedLocale,
  resolveInitialLocale,
} from "./locale";

function storage(values: Record<string, string> = {}, overrides: Partial<Storage> = {}): Storage {
  const state = new Map(Object.entries(values));
  return {
    get length() { return state.size; },
    clear: () => state.clear(),
    getItem: (key) => state.get(key) ?? null,
    key: (index) => [...state.keys()][index] ?? null,
    removeItem: (key) => { state.delete(key); },
    setItem: (key, value) => { state.set(key, String(value)); },
    ...overrides,
  };
}

describe("AppLocale", () => {
  it("accepts only the closed application locale set", () => {
    expect(isAppLocale("zh-CN")).toBe(true);
    expect(isAppLocale("en")).toBe(true);
    expect(isAppLocale("zh")).toBe(false);
    expect(isAppLocale("fr")).toBe(false);
    expect(isAppLocale(null)).toBe(false);
  });

  it.each([
    ["zh-CN", "zh-CN"],
    ["zh", "zh-CN"],
    ["zh-Hant-TW", "zh-CN"],
    ["en", "en"],
    ["en-GB", "en"],
    ["fr-FR", "zh-CN"],
    [undefined, "zh-CN"],
  ])("normalizes %s to %s", (input, expected) => {
    expect(normalizeBrowserLocale(input)).toBe(expected);
  });

  it("uses the first supported browser locale and falls back to zh-CN", () => {
    expect(resolveInitialLocale(["fr-FR", "en-US", "zh-CN"])).toBe("en");
    expect(resolveInitialLocale(["fr-FR", "de-DE"])).toBe("zh-CN");
    expect(resolveInitialLocale([])).toBe("zh-CN");
  });

  it("resolves valid persisted locales before browser locale", () => {
    expect(resolveInitialLocale(["en-US"], storage({ [LOCALE_STORAGE_KEY]: "zh-CN" }))).toBe("zh-CN");
    expect(resolveInitialLocale(["zh-CN"], storage({ [LOCALE_STORAGE_KEY]: "en" }))).toBe("en");
  });

  it("ignores invalid persisted locales and does not persist automatic resolution", () => {
    const target = storage({ [LOCALE_STORAGE_KEY]: "fr" });
    expect(readPersistedLocale(target)).toBeUndefined();
    expect(resolveInitialLocale(["en-US"], target)).toBe("en");
    expect(target.getItem(LOCALE_STORAGE_KEY)).toBe("fr");

    const empty = storage();
    expect(resolveInitialLocale(["en-US"], empty)).toBe("en");
    expect(empty.getItem(LOCALE_STORAGE_KEY)).toBeNull();
  });

  it("survives localStorage read and write failures", () => {
    const unreadable = storage({}, { getItem: () => { throw new Error("read failed"); } });
    const unwritable = storage({}, { setItem: () => { throw new Error("write failed"); } });

    expect(readPersistedLocale(unreadable)).toBeUndefined();
    expect(resolveInitialLocale(["en-US"], unreadable)).toBe("en");
    expect(() => persistExplicitLocale("en", unwritable)).not.toThrow();
  });
});

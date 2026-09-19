import { describe, expect, it } from "vitest";
import { isAppLocale, normalizeBrowserLocale, resolveInitialLocale } from "./locale";

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
});

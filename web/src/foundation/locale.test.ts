import { describe, expect, it } from "vitest";
import {
  DEFAULT_APP_LOCALE,
  isAppLocale,
  normalizeBrowserLocale,
  resolveBrowserLocale,
} from "./locale";

describe("AppLocale", () => {
  it("recognizes the closed locale set", () => {
    expect(isAppLocale("zh-CN")).toBe(true);
    expect(isAppLocale("en")).toBe(true);
    expect(isAppLocale("en-US")).toBe(false);
    expect(isAppLocale("ja")).toBe(false);
  });

  it("normalizes supported browser locale families", () => {
    expect(normalizeBrowserLocale("zh")).toBe("zh-CN");
    expect(normalizeBrowserLocale("zh-TW")).toBe("zh-CN");
    expect(normalizeBrowserLocale("en")).toBe("en");
    expect(normalizeBrowserLocale("en-US")).toBe("en");
  });

  it("ignores unsupported browser locales", () => {
    expect(normalizeBrowserLocale("ja-JP")).toBeNull();
    expect(normalizeBrowserLocale("")).toBeNull();
    expect(normalizeBrowserLocale(undefined)).toBeNull();
  });

  it("selects the first supported browser locale", () => {
    expect(resolveBrowserLocale(["ja-JP", "en-US", "zh-CN"])).toBe("en");
    expect(resolveBrowserLocale(["zh-Hant", "en-US"])).toBe("zh-CN");
  });

  it("falls back to zh-CN when no supported browser locale exists", () => {
    expect(resolveBrowserLocale(["ja-JP", "fr-FR"])).toBe(DEFAULT_APP_LOCALE);
    expect(resolveBrowserLocale([])).toBe("zh-CN");
  });
});

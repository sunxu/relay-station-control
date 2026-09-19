import { test as base } from "@playwright/test";

const localeStorageKey = "relay-control.locale";

export type Stage2LocaleFixtures = {
  configureLocaleStorage: (value?: string) => Promise<void>;
};

export const test = base.extend<Stage2LocaleFixtures>({
  configureLocaleStorage: async ({ page }, use) => {
    await use(async (value) => {
      await page.addInitScript(({ key, storedValue }) => {
        if (storedValue === null) {
          window.localStorage.removeItem(key);
        } else {
          window.localStorage.setItem(key, storedValue);
        }
      }, { key: localeStorageKey, storedValue: value ?? null });
    });
  },
});

export { expect } from "@playwright/test";

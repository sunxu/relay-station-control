import type { Page } from "@playwright/test";
import { expect, test } from "./stage2-locale.fixture";

async function openApp(page: Page) {
  await page.goto("/");
  await expect(page.getByTestId("locale-selector")).toBeVisible();
}

test.describe("Stage 2 locale foundation", () => {
  test.use({ locale: "zh-CN" });

  test("PRIMARY_ZH_CN resolves context zh-CN with stored zh-CN", async ({ page, configureLocaleStorage }) => {
    await configureLocaleStorage("zh-CN");
    await openApp(page);
    await expect(page.getByTestId("locale-selector")).toHaveValue("zh-CN");
  });

  test("stored en wins over context zh-CN", async ({ page, configureLocaleStorage }) => {
    await configureLocaleStorage("en");
    await openApp(page);
    await expect(page.getByTestId("locale-selector")).toHaveValue("en");
  });

  test("invalid stored locale falls back to context zh-CN", async ({ page, configureLocaleStorage }) => {
    await configureLocaleStorage("fr");
    await openApp(page);
    await expect(page.getByTestId("locale-selector")).toHaveValue("zh-CN");
  });
});

test.describe("Stage 2 representative English locale", () => {
  test.use({ locale: "en" });

  test("REPRESENTATIVE_EN resolves context en with stored en", async ({ page, configureLocaleStorage }) => {
    await configureLocaleStorage("en");
    await openApp(page);
    await expect(page.getByTestId("locale-selector")).toHaveValue("en");
  });

  test("context en resolves without stored locale", async ({ page, configureLocaleStorage }) => {
    await configureLocaleStorage();
    await openApp(page);
    await expect(page.getByTestId("locale-selector")).toHaveValue("en");
  });
});

test.describe("Stage 2 explicit selection", () => {
  test.use({ locale: "zh-CN" });

  test("explicit en selection survives reload", async ({ page, configureLocaleStorage }) => {
    await openApp(page);
    const selector = page.getByTestId("locale-selector");
    await expect(selector).toHaveValue("zh-CN");
    await selector.selectOption("en");
    await expect(selector).toHaveValue("en");
    await page.reload();
    await expect(selector).toHaveValue("en");
  });
});

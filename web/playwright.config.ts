import { defineConfig } from "@playwright/test";

const baseURL = process.env.CONTROL_E2E_BASE_URL ?? "http://localhost:18080";
const e2eLocale = process.env.CONTROL_E2E_LOCALE;
if (e2eLocale && e2eLocale !== "zh-CN" && e2eLocale !== "en") {
  throw new Error(`CONTROL_E2E_LOCALE must be zh-CN or en, got ${e2eLocale}`);
}

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 240_000,
  expect: { timeout: 15_000 },
  reporter: [["list", { printSteps: false }]],
  outputDir: process.env.CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR ?? "test-results/e2e",
  use: {
    baseURL,
    channel: process.env.CONTROL_E2E_BROWSER_CHANNEL ?? "chromium",
    locale: e2eLocale || undefined,
    storageState: e2eLocale ? {
      cookies: [],
      origins: [{
        origin: new URL(baseURL).origin,
        localStorage: [{ name: "relay-control.locale", value: e2eLocale }],
      }],
    } : undefined,
    viewport: { width: 1280, height: 720 },
    headless: true,
    ignoreHTTPSErrors: true,
    trace: "off",
    screenshot: "off",
    video: "off",
    serviceWorkers: "block",
  },
});

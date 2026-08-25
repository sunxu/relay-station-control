import { defineConfig } from "@playwright/test";

const baseURL = process.env.CONTROL_E2E_BASE_URL ?? "http://localhost:18080";

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
    channel: process.env.CONTROL_E2E_BROWSER_CHANNEL ?? "chrome",
    headless: true,
    ignoreHTTPSErrors: true,
    trace: "off",
    screenshot: "off",
    video: "off",
    serviceWorkers: "block",
  },
});

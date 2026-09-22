import { expect, test, type Page } from "@playwright/test";

const session = {
  state: "authenticated",
  administrator: { id: "settings-admin", login_name: "settings.operator", display_name: "设置验收" },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "settings-csrf",
  idle_expires_at: "2099-09-22T00:30:00Z",
  absolute_expires_at: "2099-09-22T12:00:00Z",
  reauthenticated_until: null,
  recovery_codes_remaining: 8,
};

async function installFixture(page: Page, locale: "zh-CN" | "en") {
  const requests: Array<{ method: string; url: string; origin: string; pathname: string }> = [];
  page.on("request", (request) => {
    if (request.resourceType() !== "fetch" && request.resourceType() !== "xhr") return;
    const url = new URL(request.url());
    requests.push({ method: request.method(), url: request.url(), origin: url.origin, pathname: url.pathname });
  });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const json = (body: unknown, status = 200) => route.fulfill({ status, headers: { "Cache-Control": "no-store", "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (url.pathname === "/api/bootstrap/status") return json({ status: "completed" });
    if (url.pathname === "/api/auth/session") return json(session);
    if (url.pathname === "/api/admins") return json({ items: [{ id: "settings-admin", login_name: "settings.operator", display_name: "设置验收", auth_source: "local", role: "super_admin", status: "enabled", created_at: "2026-09-22T00:00:00Z", updated_at: "2026-09-22T00:00:00Z", last_login_at: null }], next_cursor: null });
    throw new Error(`unexpected API request: ${route.request().method()} ${url.pathname}`);
  });
  await page.addInitScript((value) => localStorage.setItem("relay-control.locale", value), locale);
  return requests;
}

async function assertDesktop(page: Page, locale: "zh-CN" | "en", width: number, height: number) {
  await page.setViewportSize({ width, height });
  const requests = await installFixture(page, locale);
  await page.goto("/settings");
  await expect(page.getByTestId("settings-page")).toBeVisible();
  await expect(page.getByRole("heading", { name: locale === "zh-CN" ? "设置" : "Settings" })).toBeVisible();
  await expect(page.getByTestId("settings-tab-session")).toBeVisible();
  await expect(page.getByTestId("settings-tab-reauth")).toBeVisible();
  await expect(page.getByTestId("settings-tab-password")).toBeVisible();
  await expect(page.getByTestId("settings-tab-administrators")).toBeVisible();
  await expect(page.getByTestId("settings-tab-recovery")).toBeVisible();
  await expect(page.getByTestId("settings-tab-interface")).toBeVisible();
  await expect(page.getByTestId("settings-page")).toHaveAttribute("data-testid", "settings-page");
  expect(await page.locator("html").evaluate((element) => element.scrollWidth <= window.innerWidth)).toBe(true);
  expect(requests.every((request) => request.origin === new URL(page.url()).origin)).toBe(true);
  expect(requests.filter((request) => request.pathname.includes("preferences") || request.pathname.includes("settings")).length).toBe(0);
  expect(requests.filter((request) => /health|connection-test|monitoring-(enable|disable)/i.test(request.pathname)).length).toBe(0);
  expect(requests.filter((request) => request.method !== "GET").length).toBe(0);
  await page.getByTestId("settings-tab-interface").click();
  const selector = page.getByTestId("locale-selector");
  await expect(selector).toHaveCount(1);
  const nextLocale = locale === "zh-CN" ? "en" : "zh-CN";
  await selector.selectOption(nextLocale);
  await expect(selector).toHaveValue(nextLocale);
  await expect(page.getByRole("heading", { name: nextLocale === "en" ? "Settings" : "设置" })).toBeVisible();
  if (locale === "zh-CN") {
    await page.reload();
    await expect(page.getByTestId("settings-page")).toBeVisible();
    await expect(page.getByTestId("locale-selector")).toHaveValue("en");
    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
  }
  await expect(page.getByTestId("locale-selector")).toHaveCount(1);
  expect(requests.filter((request) => request.method !== "GET").length).toBe(0);
}

test("Settings desktop zh-CN 1280x720", async ({ page }) => assertDesktop(page, "zh-CN", 1280, 720));
test("Settings desktop en 1280x720", async ({ page }) => assertDesktop(page, "en", 1280, 720));
test("Settings desktop zh-CN 1440x900", async ({ page }) => assertDesktop(page, "zh-CN", 1440, 900));

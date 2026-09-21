import { expect, test, type Page } from "@playwright/test";

const session = {
  state: "authenticated",
  administrator: { display_name: "Foundation Operator", login_name: "foundation.operator" },
  csrf_token: "c".repeat(32),
};

async function installFoundationSession(page: Page) {
  await page.route("**/api/bootstrap/status", (route) => route.fulfill({ json: { status: "completed" } }));
  await page.route("**/api/auth/session", (route) => route.fulfill({ json: session }));
}

async function proveShell(page: Page, locale: "zh-CN" | "en", width: number, height: number) {
  await page.setViewportSize({ width, height });
  await page.addInitScript((value) => window.localStorage.setItem("relay-control.locale", value), locale);
  await installFoundationSession(page);
  const requests: string[] = [];
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (pathname.startsWith("/api/")) requests.push(`${request.method()} ${request.url()}`);
  });
  await page.goto("/");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByTestId("dashboard-page")).toBeVisible();
  await expect(page.getByTestId("locale-selector")).toHaveValue(locale);
  const layout = await page.evaluate(() => {
    const sidebar = document.querySelector<HTMLElement>("[data-testid='app-sidebar']");
    const header = document.querySelector<HTMLElement>("[data-testid='global-header']");
    return {
      sidebarWidth: sidebar?.getBoundingClientRect().width ?? 0,
      headerHeight: header?.getBoundingClientRect().height ?? 0,
      scrollWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth,
    };
  });
  expect(layout.sidebarWidth).toBeGreaterThanOrEqual(220);
  expect(layout.sidebarWidth).toBeLessThanOrEqual(260);
  expect(layout.headerHeight).toBeGreaterThanOrEqual(56);
  expect(layout.headerHeight).toBeLessThanOrEqual(64);
  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.viewportWidth);
  await expect(page.getByTestId("app-sidebar")).toBeVisible();
  await expect(page.getByTestId("global-header")).toBeVisible();
  await expect(page.getByTestId("foundation-placeholder")).toBeAttached();

  const labels = locale === "zh-CN"
    ? ["仪表盘", "账号", "节点", "操作", "监控", "问题", "设置"]
    : ["Dashboard", "Accounts", "Relay Nodes", "Operations", "Monitoring", "Problems", "Settings"];
  for (const [index, testId] of ["dashboard", "accounts", "nodes", "operations", "monitoring", "problems", "settings"].entries()) {
    await expect(page.getByTestId(`sidebar-${testId}`)).toHaveText(labels[index]);
  }

  await page.getByTestId("navigation-search-input").fill(locale === "zh-CN" ? "Gateway" : "Assets");
  await expect(page.getByTestId("search-result-assets")).toBeVisible();

  await page.getByTestId("sidebar-accounts").click();
  await expect(page).toHaveURL(/\/accounts$/);
  await expect(page.getByTestId("accounts-page")).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByTestId("dashboard-page")).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL(/\/accounts$/);
  await expect(page.getByTestId("accounts-page")).toBeVisible();
  await expect(page.locator("body")).not.toContainText("Issues");

  const businessRequests = requests.filter((request) =>
    !request.includes("/api/bootstrap/status") && !request.includes("/api/auth/session"),
  );
  expect(businessRequests).toEqual([]);
}

test("ZH_CN_PC_BROWSER at 1280x720", async ({ page }) => {
  await proveShell(page, "zh-CN", 1280, 720);
});

test("EN_PC_BROWSER at 1280x720", async ({ page }) => {
  await proveShell(page, "en", 1280, 720);
});

test("ZH_CN_PC_BROWSER at 1440x900", async ({ page }) => {
  await proveShell(page, "zh-CN", 1440, 900);
});

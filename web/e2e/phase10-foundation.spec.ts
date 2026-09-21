import { expect, test, type Page } from "@playwright/test";

const session = {
  state: "authenticated",
  administrator: { display_name: "Foundation Operator", login_name: "foundation.operator" },
  csrf_token: "c".repeat(32),
};

async function installFoundationSession(page: Page) {
  await page.route("**/api/bootstrap/status", (route) => route.fulfill({ json: { status: "completed" } }));
  await page.route("**/api/auth/session", (route) => route.fulfill({ json: session }));
  await page.route("**/api/healthz", (route) => route.fulfill({ json: { status: "ok", version: "0.9.3" } }));
  await page.route("**/api/assets/gateways**", (route) => route.fulfill({ json: { items: [{ instance_id: "gateway-1" }], next_cursor: null, gateway_counts: { active: 1, retired: 0, total: 1 } } }));
  await page.route("**/api/assets/nodes**", (route) => route.fulfill({ json: { items: [{ instance_id: "node-1" }], next_cursor: null, node_counts: { active: 1, retired: 0, total: 1 } } }));
  await page.route("**/api/account-inventory/poll-capacity", (route) => route.fulfill({ json: { status: "disabled", enabled: false, eligible_node_count: 0, effective_capacity: 0, concurrency: 0, request_timeout_ms: 0, finalize_timeout_ms: 0, lifecycle_timeout_ms: 0, claim_timeout_ms: 0, evaluated_slot: "slot-1", evaluated_at: "2026-09-21T00:00:00Z" } }));
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
  await expect(page.getByTestId("dashboard-control-summary")).toBeVisible();

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

  const allowed = ["/api/bootstrap/status", "/api/auth/session", "/api/healthz", "/api/assets/gateways", "/api/assets/nodes", "/api/account-inventory/poll-capacity"];
  expect(requests.every((request) => allowed.some((path) => request.includes(path)))).toBe(true);
  expect(requests.some((request) => /connection-test|jobs|problem-accounts|account-inventory\/query/.test(request))).toBe(false);
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

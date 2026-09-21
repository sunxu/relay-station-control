import { expect, test, type Page } from "@playwright/test";

const session = { state: "authenticated", administrator: { display_name: "Dashboard Operator", login_name: "dashboard.operator" }, csrf_token: "c".repeat(32) };
const allowed = new Set([
  "/api/bootstrap/status",
  "/api/auth/session",
  "/api/healthz",
  "/api/assets/gateways",
  "/api/assets/nodes",
  "/api/account-inventory/poll-capacity",
]);

async function installDashboardFixture(page: Page, failGateway = false) {
  let gatewayAttempts = 0;
  const requests: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/static/, "");
    if (path.startsWith("/api/")) requests.push(`${request.method()} ${path}`);
  });
  await page.route("**/api/bootstrap/status", (route) => route.fulfill({ json: { status: "completed" } }));
  await page.route("**/api/auth/session", (route) => route.fulfill({ json: session }));
  await page.route("**/api/healthz", (route) => route.fulfill({ json: { status: "ok", version: "0.9.3" } }));
  await page.route("**/api/assets/gateways**", (route) => {
    gatewayAttempts += 1;
    if (failGateway && gatewayAttempts < 3) return route.fulfill({ status: 503, json: { code: "service_unavailable", message: "unavailable", request_id: "dashboard-gateway" } });
    return route.fulfill({ json: { items: [{ instance_id: "gateway-page-item" }], next_cursor: null, gateway_counts: { active: 30, retired: 7, total: 37 } } });
  });
  await page.route("**/api/assets/nodes**", (route) => route.fulfill({ json: { items: [{ instance_id: "node-page-item" }], next_cursor: null, node_counts: { active: 15, retired: 4, total: 19 } } }));
  await page.route("**/api/account-inventory/poll-capacity**", (route) => route.fulfill({ json: { status: "ready", enabled: true, eligible_node_count: 4, effective_capacity: 12, concurrency: 2, request_timeout_ms: 1000, finalize_timeout_ms: 1000, lifecycle_timeout_ms: 1000, claim_timeout_ms: 1000, evaluated_slot: "slot-1", evaluated_at: "2026-09-21T00:00:00Z" } }));
  return { requests, allowed };
}

async function assertDesktop(page: Page, locale: "zh-CN" | "en", width: number, height: number) {
  await page.setViewportSize({ width, height });
  await page.addInitScript((value) => window.localStorage.setItem("relay-control.locale", value), locale);
  const { requests } = await installDashboardFixture(page);
  await page.goto("/");
  await expect(page.getByTestId("dashboard-page")).toBeVisible();
  await expect(page.getByTestId("dashboard-gateway-summary")).toContainText("37");
  await expect(page.getByTestId("dashboard-node-summary")).toContainText("19");
  if (locale === "zh-CN") {
    for (const leakedLabel of ["Inventory", "Dashboard", "Accounts", "Relay Nodes", "Operations", "Problems", "Settings"]) {
      await expect(page.getByTestId("dashboard-page")).not.toContainText(leakedLabel);
    }
  }
  const layout = await page.evaluate(() => ({
    sidebar: document.querySelector<HTMLElement>("[data-testid='app-sidebar']")?.getBoundingClientRect().width ?? 0,
    header: document.querySelector<HTMLElement>("[data-testid='global-header']")?.getBoundingClientRect().height ?? 0,
    scrollWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
  }));
  expect(layout.sidebar).toBeGreaterThanOrEqual(220);
  expect(layout.sidebar).toBeLessThanOrEqual(260);
  expect(layout.header).toBeGreaterThanOrEqual(56);
  expect(layout.header).toBeLessThanOrEqual(64);
  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.viewportWidth);
  await expect(page.getByTestId("dashboard-nav-accounts")).toBeVisible();
  const dashboardPaths = requests.map((request) => request.split(" ")[1]);
  expect(dashboardPaths.filter((path) => path === "/api/account-inventory/query" || path === "/api/jobs" || path === "/api/problem-accounts/query")).toEqual([]);
  expect(dashboardPaths.every((path) => allowed.has(path))).toBe(true);
  await page.getByTestId("dashboard-nav-accounts").click();
  await expect(page).toHaveURL(/\/accounts$/);
  expect(requests.some((request) => request.startsWith("POST "))).toBe(false);
}

test("Dashboard desktop zh-CN 1280x720", async ({ page }) => assertDesktop(page, "zh-CN", 1280, 720));
test("Dashboard desktop en 1280x720", async ({ page }) => assertDesktop(page, "en", 1280, 720));
test("Dashboard desktop zh-CN 1440x900", async ({ page }) => assertDesktop(page, "zh-CN", 1440, 900));

test("Dashboard isolates one unavailable source and retries only it", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const { requests } = await installDashboardFixture(page, true);
  await page.goto("/");
  await expect(page.getByTestId("dashboard-gateway-summary")).toContainText("Unavailable");
  await expect(page.getByTestId("dashboard-node-summary")).toContainText("19");
  await page.getByTestId("dashboard-retry-gateway").click();
  await expect(page.getByTestId("dashboard-gateway-summary")).toContainText("37");
  expect(requests.filter((request) => request.includes("/api/assets/gateways")).length).toBe(3);
});

test("Dashboard work entries navigate without domain reads", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const { requests } = await installDashboardFixture(page);
  await page.goto("/");
  await expect(page.getByTestId("dashboard-page")).toBeVisible();
  const dashboardPaths = requests.map((request) => request.split(" ")[1]);
  expect(dashboardPaths.filter((path) => path === "/api/account-inventory/query" || path === "/api/jobs" || path === "/api/problem-accounts/query")).toEqual([]);
  for (const [testId, path] of [["dashboard-nav-operations", "/operations"], ["dashboard-nav-problems", "/problems"]] as const) {
    await page.getByTestId(testId).click();
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await page.goto("/");
    await expect(page.getByTestId("dashboard-page")).toBeVisible();
  }
});

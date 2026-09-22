import { expect, test, type Page } from "@playwright/test";

const JOB = "00000000-0000-4000-8000-000000000301";
const OPERATION = "00000000-0000-4000-8000-000000000302";
const session = {
  state: "authenticated",
  administrator: { id: "00000000-0000-4000-8000-000000000399", login_name: "operations", display_name: "操作验收", auth_source: "local", role: "super_admin", status: "enabled" },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "operations-csrf",
};

function job(status = "running") {
  return {
    job_id: JOB,
    operation_id: OPERATION,
    job_kind: "dingtalk_alert_delivery",
    status,
    attempt_count: 1234,
    max_attempts: 5678,
    available_at: "2026-09-20T01:00:00Z",
    started_at: "2026-09-20T01:01:00Z",
    completed_at: null,
    cancel_requested: false,
    error_code: null,
    outbox_status: "publishing",
    created_at: "2026-09-20T01:00:00Z",
    updated_at: "2026-09-20T01:01:00Z",
  };
}

function detail() {
  return {
    ...job(),
    events: [{ sequence: 1, event_type: "enqueued", from_status: null, to_status: "pending", attempt_count: 0, actor_type: "service", reason_code: "job_enqueued", error_code: null, occurred_at: "2026-09-20T01:00:00Z" }],
  };
}

async function installFixture(page: Page, locale: "zh-CN" | "en") {
  const requests: Array<{ method: string; url: string; origin: string; pathname: string }> = [];
  page.on("request", (request) => {
    if (request.resourceType() === "fetch" || request.resourceType() === "xhr") {
      const url = new URL(request.url());
      requests.push({ method: request.method(), url: request.url(), origin: url.origin, pathname: url.pathname });
    }
  });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (!url.pathname.startsWith("/api/")) return route.continue();
    const json = (body: unknown, status = 200) => route.fulfill({ status, headers: { "Cache-Control": "no-store", "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (url.pathname === "/api/bootstrap/status") return json({ status: "completed" });
    if (url.pathname === "/api/auth/session") return json(session);
    if (url.pathname === "/api/jobs") return json({ items: [job()], next_cursor: null });
    if (url.pathname === `/api/jobs/${JOB}`) return json(detail());
    throw new Error(`unexpected API request: ${route.request().method()} ${url.pathname}`);
  });
  await page.addInitScript((value: string) => localStorage.setItem("relay-control.locale", value), locale);
  return requests;
}

async function assertDesktopSurface(page: Page, locale: "zh-CN" | "en") {
  await expect(page.getByTestId("operations-page")).toBeVisible();
  await expect(page.getByTestId("durable-jobs-view")).toBeVisible();
  await expect(page.getByTestId(`job-row-${JOB}`)).toBeVisible();
  await expect(page.getByTestId("job-kind-filter")).toHaveAttribute("aria-label", locale === "zh-CN" ? "任务类型" : "Job type");
  await expect(page.getByTestId("job-status-filter").locator("input")).toHaveAttribute("aria-label", locale === "zh-CN" ? "任务状态" : "Job status");
  await expect(page.getByTestId("job-created-from")).toHaveAttribute("aria-label", locale === "zh-CN" ? "创建时间起点" : "Created from");
  await expect(page.getByTestId("job-created-to")).toHaveAttribute("aria-label", locale === "zh-CN" ? "创建时间终点" : "Created to");
  await expect(page.getByTestId("job-page-size").locator("input")).toHaveAttribute("aria-label", locale === "zh-CN" ? "每页任务数" : "Jobs per page");
  expect(await page.locator("html").evaluate((element) => element.scrollWidth <= window.innerWidth)).toBe(true);
}

async function assertFilterQueryTransport(page: Page, requests: Array<{ method: string; url: string; origin: string; pathname: string }>) {
  await page.getByTestId("job-kind-filter").fill("dingtalk_alert_delivery");
  await page.getByTestId("job-created-from").fill("2026-09-20T00:00");
  await page.getByTestId("job-created-to").fill("2026-09-20T02:00");
  await page.getByTestId("job-status-filter").click();
  const runningOption = page.getByTestId("job-status-option-running");
  await runningOption.scrollIntoViewIfNeeded();
  await runningOption.click();
  await page.getByTestId("job-page-size").click();
  await page.getByTestId("job-page-size-option-200").click();
  await expect.poll(() => requests.filter((request) => request.pathname === "/api/jobs").length).toBeGreaterThan(1);
  const latest = new URL(requests.filter((request) => request.pathname === "/api/jobs").at(-1)!.url);
  const expectedFrom = await page.evaluate(() => new Date("2026-09-20T00:00").toISOString());
  const expectedTo = await page.evaluate(() => new Date("2026-09-20T02:00").toISOString());
  expect(latest.searchParams.get("job_kind")).toBe("dingtalk_alert_delivery");
  expect(latest.searchParams.get("status")).toBe("running");
  expect(latest.searchParams.get("created_from")).toBe(expectedFrom);
  expect(latest.searchParams.get("created_to")).toBe(expectedTo);
  expect(latest.searchParams.get("limit")).toBe("200");
}

function assertZhCnOrdinaryCopy(body: string) {
  for (const forbidden of ["Jobs", "Job", "Retry", "Previous", "Next", "View Details", "Lifecycle Events", "Cancel Requested", "Job ID", "Operation ID", "Outbox"]) {
    expect(body).not.toMatch(new RegExp(`\\b${forbidden}\\b`, "u"));
  }
}

test.describe("Phase 10 Operations", () => {
  test("renders zh-CN Durable Jobs at 1280x720 with filters and Control-only read transport", async ({ page }) => {
    test.setTimeout(30_000);
    const requests = await installFixture(page, "zh-CN");
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto("/operations");
    await expect(page.getByTestId("operations-page")).toContainText("操作");
    await assertDesktopSurface(page, "zh-CN");
    assertZhCnOrdinaryCopy(await page.getByTestId("operations-page").innerText());
    await assertFilterQueryTransport(page, requests);
    await page.getByTestId(`job-details-${JOB}`).click();
    await expect(page.getByTestId("job-detail")).toBeVisible();
    await expect(page.getByTestId("job-event-1")).toBeVisible();
    await expect(page.getByTestId("job-detail-drawer")).toBeVisible();
    await expect(page.getByTestId("job-detail-close")).toBeVisible();
    await page.getByTestId("job-detail-close").click();
    const controlOrigin = new URL(page.url()).origin;
    expect(requests.every((request) => request.origin === controlOrigin)).toBe(true);
    expect(requests.filter((request) => /health|connection-test|account-operations|account-inventory|problem-accounts|\/nodes|\/gateway/i.test(request.pathname)).length).toBe(0);
    expect(requests.filter((request) => request.method !== "GET").length).toBe(0);
    expect(requests.some((request) => request.pathname === "/api/jobs")).toBe(true);
    expect(requests.some((request) => request.pathname === `/api/jobs/${JOB}`)).toBe(true);
  });

  test("renders English Durable Jobs at 1280x720", async ({ page }) => {
    test.setTimeout(30_000);
    const requests = await installFixture(page, "en");
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto("/operations/");
    await assertDesktopSurface(page, "en");
    await expect(page.getByTestId("durable-jobs-view")).toContainText("Persistent jobs");
    await expect(page.getByTestId("app-sidebar").getByTestId("sidebar-operations")).toHaveAttribute("aria-current", "page");
    const controlOrigin = new URL(page.url()).origin;
    expect(requests.every((request) => request.origin === controlOrigin)).toBe(true);
  });

  test("renders zh-CN Durable Jobs at 1440x900 without page overflow", async ({ page }) => {
    test.setTimeout(30_000);
    const requests = await installFixture(page, "zh-CN");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/jobs/");
    await expect(page.getByTestId("operations-page")).toContainText("操作");
    await assertDesktopSurface(page, "zh-CN");
    assertZhCnOrdinaryCopy(await page.getByTestId("operations-page").innerText());
    const controlOrigin = new URL(page.url()).origin;
    expect(requests.every((request) => request.origin === controlOrigin)).toBe(true);
  });
});

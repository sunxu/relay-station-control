import { expect, test, type Page } from "@playwright/test";

const nodeA = "11111111-1111-4111-8111-111111111111";
const nodeB = "22222222-2222-4222-8222-222222222222";
const occurrence = (id: string, type: string, reason: string, severity: string) => ({ occurrence_id: id, type, reason, severity, since: "2026-09-07T00:00:00Z" });
const row = (instance_id: string, node_name: string, email: string, issues: unknown[]) => ({ instance_id, node_name, account_key: `antigravity:${email}`, email, provider: "antigravity", issues, availability: null, token_state: "UNKNOWN", last_refresh_at: null, expected_valid_until: null, next_retry_at: null, last_success_at: null, last_failure_at: null, highest_severity: "Critical", oldest_active_since: "2026-09-07T00:00:00Z" });
const session = { state: "authenticated", administrator: { id: "00000000-0000-4000-8000-000000000001", login_name: "e2e", display_name: "E2E", auth_source: "local", role: "super_admin", status: "enabled", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }, mfa: { required: true, completed: true, method: "totp" }, csrf_token: "c", created_at: "2026-01-01T00:00:00Z", last_activity_at: "2026-01-01T00:00:00Z", idle_expires_at: "2099-01-01T00:30:00Z", absolute_expires_at: "2099-01-01T12:00:00Z", reauthenticated_until: null, recovery_codes_remaining: 10 };

async function runProblemsCase(page: Page, locale: "zh-CN" | "en", viewport: { width: number; height: number }) {
  await page.setViewportSize(viewport);
  await page.addInitScript((value: string) => localStorage.setItem("relay-control.locale", value), locale);
  let controlOrigin = "";
  const requests: Array<{ method: string; url: string; origin: string; pathname: string }> = [];
  const problemRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];
  const pageOneRows = [row(nodeA, "Node A", "user@example.invalid", [occurrence("33333333-3333-4333-8333-333333333333", "TOKEN_INVALID", "token_invalid", "Critical"), occurrence("44444444-4444-4444-8444-444444444444", "FORBIDDEN", "forbidden", "Warning")]), row(nodeB, "Node B", "user@example.invalid", [occurrence("55555555-5555-4555-8555-555555555555", "ACCOUNT_BLOCKED", "account_blocked", "Critical")])];
  let authenticated = false;
  let expired = false;
  page.on("request", (request) => {
    if (request.resourceType() !== "fetch" && request.resourceType() !== "xhr") return;
    const url = new URL(request.url());
    requests.push({ method: request.method(), url: request.url(), origin: url.origin, pathname: url.pathname });
    if (url.pathname === "/api/problem-accounts/query") problemRequests.push({ body: request.postDataJSON() as Record<string, unknown>, headers: request.headers() });
  });
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (url.pathname === "/api/bootstrap/status") return route.fulfill({ json: { status: "completed" } });
    if (url.pathname === "/api/auth/session") return authenticated ? route.fulfill({ json: session }) : route.fulfill({ status: 401, json: { code: "unauthorized", message: "unauthorized", request_id: "fixture" } });
    if (url.pathname === "/api/auth/login") { expect(request.method()).toBe("POST"); authenticated = true; return route.fulfill({ json: session }); }
    if (url.pathname === "/api/admins") return route.fulfill({ json: { items: [session.administrator], next_cursor: null } });
    if (url.pathname === "/api/problem-accounts/query") {
      if (expired) return route.fulfill({ status: 401, json: { code: "unauthorized", message: "expired", request_id: "fixture" } });
      expect(request.method()).toBe("POST");
      const body = request.postDataJSON() as Record<string, unknown>;
      if (body.cursor === "problems-page-2") return route.fulfill({ json: { items: [], next_cursor: null } });
      return route.fulfill({ json: { items: pageOneRows, next_cursor: "problems-page-2" } });
    }
    throw new Error(`unhandled API fixture: ${request.method()} ${url.pathname}`);
  });

  await page.goto("/");
  controlOrigin = new URL(page.url()).origin;
  await expect(page.getByTestId("login-page")).toBeVisible();
  await page.getByTestId("login-login-name").fill("e2e");
  await page.getByTestId("login-password").fill("password");
  await page.getByTestId("login-submit").click();
  await page.goto("/problems");
  await expect(page.getByTestId("problems-page")).toBeVisible();
  await expect(page.getByTestId("problem-row").first()).toBeVisible();
  await expect(page.getByTestId("problems-card")).toContainText("token_invalid");
  await expect(page.getByTestId("problems-card")).toContainText("forbidden");
  expect(problemRequests[0].body).toEqual({ limit: 25 });
  expect(problemRequests[0].headers["x-csrf-token"]).toBe("c");
  expect(requests.every((request) => request.origin === controlOrigin)).toBe(true);
  expect(requests.filter((request) => /health|connection-test|monitoring-(enable|disable)/.test(request.pathname)).length).toBe(0);
  expect(requests.filter((request) => request.pathname.startsWith("/api/account-operations/")).length).toBe(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (locale === "zh-CN") {
    const text = await page.getByTestId("problems-page").innerText();
    expect(text).not.toMatch(/\b(?:Issues|Availability|Inventory timing|Request evidence|Since|Expected Valid Until|Retry|Page size|Query|confirmed problems)\b/u);
  } else {
    await expect(page.getByTestId("problems-page")).toContainText("Problems");
  }

  const choose = async (selectId: string, optionId: string) => {
    await page.getByTestId(selectId).click();
    await page.getByTestId(optionId).click();
  };
  await choose("problems-provider-filter", "problems-provider-antigravity");
  await choose("problems-severity-filter", "problems-severity-critical");
  await choose("problems-reason-filter", "problems-reason-cross-node-duplicate-ownership");
  await page.getByTestId("problems-node-filter").fill(`  ${nodeA}  `);
  await page.getByTestId("problems-email-filter").fill("  USER@EXAMPLE.INVALID ");
  await page.getByTestId("problems-query").click();
  await expect.poll(() => problemRequests.length).toBe(2);
  expect(problemRequests[1].body).toEqual({ provider: "antigravity", node: nodeA, severity: "Critical", reason: "cross_node_duplicate_ownership", email: "user@example.invalid", limit: 25 });
  await page.getByTestId("problems-next").click();
  await expect(page.getByTestId("problems-card")).toContainText(locale === "zh-CN" ? "当前筛选条件下没有已确认问题" : "No confirmed problems match the filters");
  expect(problemRequests[2].body).toEqual({ provider: "antigravity", node: nodeA, severity: "Critical", reason: "cross_node_duplicate_ownership", email: "user@example.invalid", limit: 25, cursor: "problems-page-2" });
  await page.getByTestId("problems-previous").click();
  await expect(page.getByTestId("problems-card").getByText("TOKEN_INVALID", { exact: true })).toBeVisible();
  expect(problemRequests[3].body).toEqual({ provider: "antigravity", node: nodeA, severity: "Critical", reason: "cross_node_duplicate_ownership", email: "user@example.invalid", limit: 25 });
  await choose("problems-page-size", "problems-page-size-50");
  await expect.poll(() => problemRequests.length).toBe(5);
  expect(problemRequests[4].body).toEqual({ provider: "antigravity", node: nodeA, severity: "Critical", reason: "cross_node_duplicate_ownership", email: "user@example.invalid", limit: 50 });
  await choose("problems-page-size", "problems-page-size-100");
  await expect.poll(() => problemRequests.length).toBe(6);
  expect(problemRequests[5].body).toEqual({ provider: "antigravity", node: nodeA, severity: "Critical", reason: "cross_node_duplicate_ownership", email: "user@example.invalid", limit: 100 });

  expired = true;
  await page.getByTestId("problems-query").click();
  await expect(page.getByTestId("login-page")).toBeVisible();
  await expect(page.getByTestId("problems-page")).toHaveCount(0);
  expect(await page.getByText("user@example.invalid", { exact: true }).count()).toBe(0);
}

test("Problems zh-CN desktop 1280", async ({ page }) => runProblemsCase(page, "zh-CN", { width: 1280, height: 720 }));
test("Problems en desktop 1280", async ({ page }) => runProblemsCase(page, "en", { width: 1280, height: 720 }));
test("Problems zh-CN desktop 1440", async ({ page }) => runProblemsCase(page, "zh-CN", { width: 1440, height: 900 }));

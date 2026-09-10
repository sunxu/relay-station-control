import { expect, test } from "@playwright/test";

const nodeA = "11111111-1111-4111-8111-111111111111";
const nodeB = "22222222-2222-4222-8222-222222222222";
const occurrence = (id: string, type: string, reason: string, severity: string) => ({
  occurrence_id: id,
  type,
  reason,
  severity,
  since: "2026-09-07T00:00:00Z",
});
const row = (instance_id: string, node_name: string, email: string, issues: unknown[]) => ({
  instance_id,
  node_name,
  account_key: `antigravity:${email}`,
  email,
  provider: "antigravity",
  issues,
  availability: null,
  token_state: "UNKNOWN",
  last_refresh_at: null,
  expected_valid_until: null,
  next_retry_at: null,
  last_success_at: null,
  last_failure_at: null,
  highest_severity: "Critical",
  oldest_active_since: "2026-09-07T00:00:00Z",
});
const session = {
  state: "authenticated",
  administrator: {
    id: "00000000-0000-4000-8000-000000000001",
    login_name: "e2e",
    display_name: "E2E",
    auth_source: "local",
    role: "super_admin",
    status: "enabled",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "c",
  created_at: "2026-01-01T00:00:00Z",
  last_activity_at: "2026-01-01T00:00:00Z",
  idle_expires_at: "2099-01-01T00:30:00Z",
  absolute_expires_at: "2099-01-01T12:00:00Z",
  reauthenticated_until: null,
  recovery_codes_remaining: 10,
};

test("Problems is a lazy read-only view with one query per page", async ({ page }, testInfo) => {
  const problemRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];
  let authenticated = false;
  let expired = false;
  const pageOneRows = [
    row(nodeA, "Node A", "user@example.invalid", [
      occurrence("33333333-3333-4333-8333-333333333333", "TOKEN_INVALID", "token_invalid", "Critical"),
      occurrence("44444444-4444-4444-8444-444444444444", "FORBIDDEN", "forbidden", "Warning"),
    ]),
    row(nodeB, "Node B", "user@example.invalid", [occurrence("55555555-5555-4555-8555-555555555555", "ACCOUNT_BLOCKED", "account_blocked", "Critical")]),
  ];
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (url.pathname === "/api/bootstrap/status") return route.fulfill({ json: { status: "completed" } });
    if (url.pathname === "/api/auth/session") {
      if (!authenticated) return route.fulfill({ status: 401, json: { code: "unauthorized", message: "unauthorized", request_id: "fixture" } });
      return route.fulfill({ json: session });
    }
    if (url.pathname === "/api/auth/login") {
      expect(request.method()).toBe("POST");
      expect(request.postDataJSON()).toEqual({ login_name: "e2e", password: "password" });
      authenticated = true;
      return route.fulfill({ json: session });
    }
    if (url.pathname === "/api/admins") return route.fulfill({ json: { items: [session.administrator], next_cursor: null } });
    if (url.pathname === "/api/problem-accounts/query") {
      if (expired) return route.fulfill({ status: 401, json: { code: "unauthorized", message: "expired", request_id: "fixture" } });
      expect(request.method()).toBe("POST");
      problemRequests.push({ body: request.postDataJSON() as Record<string, unknown>, headers: request.headers() });
      const body = request.postDataJSON() as Record<string, unknown>;
      if (body.cursor === "problems-page-2") return route.fulfill({ json: { items: [], next_cursor: null } });
      return route.fulfill({ json: { items: pageOneRows, next_cursor: "problems-page-2" } });
    }
    throw new Error(`unhandled API fixture: ${request.method()} ${url.pathname}`);
  });

  await page.goto("/");
  await expect(page.getByTestId("login-page")).toBeVisible();
  await page.getByLabel("登录名").fill("e2e");
  await page.getByLabel("密码").fill("password");
  await page.getByRole("button", { name: /继\s*续/ }).click();
  await expect(page.getByTestId("management-page")).toBeVisible();
  await page.getByRole("button", { name: "Problems" }).click();
  await expect(page.getByTestId("problems-page")).toBeVisible();
  await expect(page.getByText("user@example.invalid").first()).toBeVisible();
  await expect(page.getByText("TOKEN_INVALID", { exact: true })).toBeVisible();
  await expect(page.getByText("FORBIDDEN", { exact: true })).toBeVisible();
  await expect(page.getByText("ACCOUNT_BLOCKED", { exact: true })).toBeVisible();
  expect(await page.getByTestId("problems-card").getByText("—", { exact: true }).count()).toBeGreaterThan(0);
  expect(await page.getByText("user@example.invalid", { exact: true }).count()).toBe(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("problems-desktop.png"), fullPage: true });
  expect(problemRequests).toHaveLength(1);
  expect(problemRequests[0].body).toEqual({ limit: 25 });
  expect(problemRequests[0].headers["x-csrf-token"]).toBe("c");

  await page.getByLabel("Email").fill("  USER@EXAMPLE.INVALID ");
  await page.getByRole("button", { name: "Query" }).click();
  await expect.poll(() => problemRequests.length).toBe(2);
  expect(problemRequests[1].body).toEqual({ email: "user@example.invalid", limit: 25 });
  await page.getByRole("button", { name: "下一页" }).click();
  await expect(page.getByText("当前过滤条件下没有 confirmed problems")).toBeVisible();
  expect(problemRequests[2].body).toEqual({ email: "user@example.invalid", limit: 25, cursor: "problems-page-2" });
  await page.getByRole("button", { name: "上一页" }).click();
  await expect(page.getByText("TOKEN_INVALID", { exact: true })).toBeVisible();
  expect(problemRequests[3].body).toEqual({ email: "user@example.invalid", limit: 25 });
  expect(problemRequests.every(({ body }) => !("node" in body))).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByText("TOKEN_INVALID", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("problems-mobile.png"), fullPage: true });
  expired = true;
  await page.getByRole("button", { name: "Query" }).click();
  await expect(page.getByTestId("login-page")).toBeVisible();
  await expect(page.getByTestId("problems-page")).toHaveCount(0);
  await expect(page.getByText("user@example.invalid", { exact: true })).toHaveCount(0);
  await expect(page.getByText("TOKEN_INVALID", { exact: true })).toHaveCount(0);
});

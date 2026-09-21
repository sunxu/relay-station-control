import { expect, test, type Page } from "@playwright/test";

const NODE = "11111111-1111-4111-8111-111111111111";
const ACCOUNT = "openai:accounts@example.invalid";
const UNKNOWN_COMMAND = "22222222-2222-4222-8222-222222222222";
const APPLIED_COMMAND = "33333333-3333-4333-8333-333333333333";
const session = { state: "authenticated", administrator: { display_name: "Accounts Operator", login_name: "accounts.operator" }, csrf_token: "c".repeat(32) };
type CapturedRequest = { method: string; url: string; origin: string; pathname: string };

async function installAccountsFixture(page: Page) {
  const requests: CapturedRequest[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (request.resourceType() === "fetch" || request.resourceType() === "xhr") requests.push({ method: request.method(), url: request.url(), origin: url.origin, pathname: url.pathname });
  });
  await page.route("**/api/bootstrap/status", (route) => route.fulfill({ json: { status: "completed" } }));
  await page.route("**/api/auth/session", (route) => route.fulfill({ json: session }));
  await page.route("**/api/assets/nodes", (route) => route.fulfill({ json: { items: [{ instance_id: NODE, display_name: "Acceptance Relay", node_type: "cliproxyapi", driver_contract_version: "1", management_endpoint: "http://node:8317", secret_configured: true, capabilities: ["management_account_inventory_read"], lifecycle_status: "ACTIVE", revision: 1, retired_at: null, retired_by: null, retire_reason: null, monitoring: { monitoring_active: true, effective_from: null, effective_to: null } }], next_cursor: null } }));
  await page.route(`**/api/assets/nodes/${NODE}`, (route) => route.fulfill({ json: { asset: { instance_id: NODE, display_name: "Acceptance Relay", node_type: "cliproxyapi", driver_contract_version: "1", management_endpoint: "http://node:8317", secret_configured: true, capabilities: ["management_account_inventory_read"], lifecycle_status: "ACTIVE", revision: 1, retired_at: null, retired_by: null, retire_reason: null, monitoring: { monitoring_active: true, effective_from: null, effective_to: null } }, predecessor: null, successor: null } }));
  await page.route("**/api/account-inventory/poll-capacity", (route) => route.fulfill({ json: { status: "ready", enabled: true, eligible_node_count: 1, effective_capacity: 10, concurrency: 1, request_timeout_ms: 1000, finalize_timeout_ms: 1000, lifecycle_timeout_ms: 1000, claim_timeout_ms: 1000, evaluated_slot: "slot-1", evaluated_at: "2026-09-21T00:00:00Z" } }));
  await page.route(`**/api/topology/nodes/${NODE}/provider-states`, (route) => route.fulfill({ json: { providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: null, snapshot_freshness: "fresh", health_scheduled_at: null, health_degraded: false, health_reason: null }], observed_at: "2026-09-21T00:00:00Z" } }));
  await page.route(`**/api/topology/nodes/${NODE}/account-quality/query`, (route) => route.fulfill({ json: { instance_id: NODE, window: "15m", items: [{ account_key: ACCOUNT, email: "accounts@example.invalid", provider: "openai", quality: "good", request_count: 37, success_count: 36, failure_count: 1, success_rate: 36 / 37, p95_latency_ms: 123, last_success_at: "2026-09-21T00:00:00Z", last_failure_at: null, last_failure_class: null, lifecycle: "present", basic_status: "reported_active", availability: { state: "AVAILABLE", reason: "available", since: "2026-09-20T00:00:00Z" }, recent_requests: [], inventory: { lifecycle: "present", basic_status: "reported_active" } }], next_cursor: null } }));
  await page.route(`**/api/topology/nodes/${NODE}/incidents**`, (route) => route.fulfill({ json: { instance_id: NODE, items: [], next_cursor: null } }));
  await page.route(`**/api/topology/nodes/${NODE}/request-history**`, (route) => route.fulfill({ json: { instance_id: NODE, account_key: ACCOUNT, items: [], next_cursor: null } }));
  await page.route(`**/api/topology/nodes/${NODE}/account-availability-occurrences**`, (route) => route.fulfill({ json: { instance_id: NODE, account_key: ACCOUNT, items: [], next_cursor: null } }));
  await page.route(`**/api/account-operations/${UNKNOWN_COMMAND}`, (route) => route.fulfill({ json: { operation: { command_id: UNKNOWN_COMMAND, node_instance_id: NODE, account_key: ACCOUNT, operation_kind: "disable", execution_state: "outcome_unknown", result: null, error_code: null, lifecycle_overridden: false, same_account_overridden: false, created_at: "2026-09-21T00:00:00Z", updated_at: "2026-09-21T00:00:00Z" } } }));
  await page.route(`**/api/account-operations/${APPLIED_COMMAND}`, (route) => route.fulfill({ json: { operation: { command_id: APPLIED_COMMAND, node_instance_id: NODE, account_key: ACCOUNT, operation_kind: "disable", execution_state: "remote_applied", result: "applied", error_code: null, lifecycle_overridden: false, same_account_overridden: false, created_at: "2026-09-21T00:00:00Z", updated_at: "2026-09-21T00:00:00Z" } } }));
  return requests;
}

async function assertAccounts(page: Page, locale: "zh-CN" | "en", width: number, height: number) {
  await page.setViewportSize({ width, height });
  await page.addInitScript((value) => window.localStorage.setItem("relay-control.locale", value), locale);
  const requests = await installAccountsFixture(page);
  await page.goto(`/accounts?instance_id=${NODE}`);
  await expect(page.getByTestId("accounts-page")).toBeVisible();
  await expect(page.getByTestId("accounts-results")).toBeVisible();
  await expect(page.getByTestId(`account-details-${encodeURIComponent(ACCOUNT)}`)).toBeVisible();
  if (locale === "zh-CN") for (const word of ["Node", "Account", "Inventory", "Quality", "Operations", "Request History", "Availability"]) await expect(page.getByTestId("accounts-page")).not.toContainText(new RegExp(`\\b${word}\\b`, "u"));
  await page.getByTestId(`account-details-${encodeURIComponent(ACCOUNT)}`).click();
  await expect(page.getByTestId("account-request-history-tab")).toBeVisible();
  await page.getByTestId("account-availability-tab").click();
  await expect(page.getByTestId("account-availability-status")).toBeVisible();
  const layout = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, viewportWidth: window.innerWidth }));
  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.viewportWidth);
  const controlOrigin = new URL(page.url()).origin;
  expect(requests.every((request) => request.origin === controlOrigin)).toBe(true);
  expect(requests.some((request) => /health|connection-test/u.test(request.pathname))).toBe(false);
  expect(requests.some((request) => request.origin !== controlOrigin)).toBe(false);
}

test("Accounts zh-CN desktop 1280", async ({ page }) => assertAccounts(page, "zh-CN", 1280, 720));
test("Accounts en desktop 1280", async ({ page }) => assertAccounts(page, "en", 1280, 720));
test("Accounts zh-CN desktop 1440", async ({ page }) => assertAccounts(page, "zh-CN", 1440, 900));

test("Accounts canonical operation wiring preserves outcome-unknown safeguards", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.addInitScript((value) => window.localStorage.setItem("relay-control.locale", value), "zh-CN");
  await installAccountsFixture(page);
  await page.goto(`/accounts?instance_id=${NODE}`);
  await expect(page.getByTestId("accounts-results")).toBeVisible();
  await page.getByTestId(`account-details-${encodeURIComponent(ACCOUNT)}`).click();
  await page.getByTestId("account-operations-tab").click();

  await page.getByTestId("account-operation-command-id").fill(UNKNOWN_COMMAND);
  await page.getByTestId("account-operation-read").click();
  await expect(page.getByTestId("account-operation-result")).toBeVisible();
  await expect(page.getByTestId("account-operation-result")).toContainText("远端结果不确定");
  await expect(page.getByTestId("account-override-kind")).toBeVisible();

  await page.getByTestId("account-operation-command-id").fill(APPLIED_COMMAND);
  await page.getByTestId("account-operation-read").click();
  await expect(page.getByTestId("account-operation-result")).toContainText("已应用");
  await expect(page.getByTestId("account-override-kind")).not.toBeVisible();
});

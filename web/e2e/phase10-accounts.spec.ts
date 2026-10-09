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
  await page.route("**/api/assets/nodes**", (route) => route.fulfill({ json: { items: [{ instance_id: NODE, display_name: "Acceptance Relay", node_type: "cliproxyapi", driver_contract_version: "1", management_endpoint: "http://node:8317", secret_configured: true, capabilities: ["management_account_inventory_read"], lifecycle_status: "ACTIVE", revision: 1, retired_at: null, retired_by: null, retire_reason: null, monitoring: { monitoring_active: true, effective_from: null, effective_to: null } }], next_cursor: null } }));
  await page.route(`**/api/assets/nodes/${NODE}`, (route) => route.fulfill({ json: { asset: { instance_id: NODE, display_name: "Acceptance Relay", node_type: "cliproxyapi", driver_contract_version: "1", management_endpoint: "http://node:8317", secret_configured: true, capabilities: ["management_account_inventory_read"], lifecycle_status: "ACTIVE", revision: 1, retired_at: null, retired_by: null, retire_reason: null, monitoring: { monitoring_active: true, effective_from: null, effective_to: null } }, predecessor: null, successor: null } }));
  await page.route("**/api/account-inventory/poll-capacity", (route) => route.fulfill({ json: { status: "ready", enabled: true, eligible_node_count: 1, effective_capacity: 10, concurrency: 1, request_timeout_ms: 1000, finalize_timeout_ms: 1000, lifecycle_timeout_ms: 1000, claim_timeout_ms: 1000, evaluated_slot: "slot-1", evaluated_at: "2026-09-21T00:00:00Z" } }));
  await page.route(`**/api/account-inventory/nodes/${NODE}/providers`, (route) => route.fulfill({ json: { providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: null, snapshot_freshness: "fresh", health_scheduled_at: null, health_degraded: false, health_reason: null }], observed_at: "2026-09-21T00:00:00Z" } }));
  await page.route(`**/api/topology/nodes/${NODE}/account-quality**`, (route) => route.fulfill({ json: { instance_id: NODE, window: "15m", items: [{ account_key: ACCOUNT, email: "accounts@example.invalid", provider: "openai", quality: "good", request_count: 37, success_count: 36, failure_count: 1, success_rate: 36 / 37, p95_latency_ms: 123, last_success_at: "2026-09-21T00:00:00Z", last_failure_at: null, last_failure_class: null, lifecycle: "present", basic_status: "reported_active", availability: { state: "AVAILABLE", reason: "available", since: "2026-09-20T00:00:00Z" }, recent_requests: [], inventory: { lifecycle: "present", basic_status: "reported_active" } }], next_cursor: null } }));
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

for (const locale of ["zh-CN", "en"] as const) test(`Accounts JSON import ${locale} preserves proxy and partial results`, async ({ page }) => {
  await page.addInitScript((value) => localStorage.setItem("relay-control.locale", value), locale);
  const requests = await installAccountsFixture(page);
  const uploads: Array<{ command: string; account: string; body: string; csrf: string | undefined }> = [];
  await page.route("**/api/account-operations/upload-new", async (route) => {
    const request = route.request();
    const body = request.postDataBuffer()?.toString("utf8") ?? "";
    const command = body.match(/"command_id":"([a-f0-9-]+)"/u)?.[1] ?? "";
    const account = body.match(/"account_key":"([^"]+)"/u)?.[1] ?? "";
    uploads.push({ command, account, body, csrf: request.headers()["x-csrf-token"] });
    await route.fulfill(account.includes("second") ? { status: 503, json: { code: "service_unavailable", message: "Unavailable", request_id: "fixture" } } : { json: { operation: { command_id: command, node_instance_id: NODE, account_key: account, operation_kind: "upload_new", execution_state: "remote_applied", result: "applied", error_code: null, lifecycle_overridden: false, same_account_overridden: false, created_at: "2026-10-09T00:00:00Z", updated_at: "2026-10-09T00:00:00Z" } } });
  });
  const proxyPassword = "synthetic-proxy-canary";
  const token = "synthetic-import-canary";
  const consoleMessages: string[] = [];
  page.on("console", (message) => consoleMessages.push(message.text()));
  await page.goto(`/accounts?instance_id=${NODE}`);
  await page.getByTestId("account-upload-new").click();
  await expect(page.getByTestId("account-upload-new-email")).toHaveCount(0);
  const chooseFile = async (json: unknown) => {
    const chooser = page.waitForEvent("filechooser");
    await page.getByTestId("account-upload-new-file-button").click();
    await (await chooser).setFiles({ name: "not-the-account.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(json)) });
  };
  await chooseFile({ type: "antigravity", email: "single@example.invalid", refresh_token: token, proxy_url: `socks5h://user:${proxyPassword}@proxy.example.invalid:1080`, disabled: true });
  await expect(page.getByTestId("account-import-preview-single@example.invalid")).toContainText(locale === "zh-CN" ? "禁用" : "Disabled");
  await page.getByTestId("account-upload-new-submit").click();
  await expect(page.getByTestId("account-import-result-single@example.invalid")).toContainText(locale === "zh-CN" ? "已应用" : "Applied");
  await page.getByTestId("account-upload-new").click();
  await chooseFile({ accounts: ["first", "second"].map((name) => ({ platform: "antigravity", type: "oauth", credentials: { email: `${name}@example.invalid`, refresh_token: token }, proxy_key: "p", auto_pause_on_expired: true })), proxies: [{ proxy_key: "p", protocol: "socks5h", host: "proxy.example.invalid", port: 1080, username: "user", password: proxyPassword }] });
  await expect(page.getByTestId("account-import-preview-first@example.invalid")).toContainText(locale === "zh-CN" ? "启用" : "Enabled");
  await page.getByTestId("account-upload-new-submit").click();
  await expect(page.getByTestId("account-import-result-second@example.invalid")).toBeVisible();
  await expect(page.getByTestId("account-import-result-second@example.invalid")).toContainText("不可用");
  expect(uploads.map((item) => item.account)).toEqual(["antigravity:single@example.invalid", "antigravity:first@example.invalid", "antigravity:second@example.invalid"]);
  expect(new Set(uploads.map((item) => item.command)).size).toBe(3);
  for (const item of uploads) { expect(item.csrf).toBe(session.csrf_token); expect(item.body).toContain(`socks5h://user:${proxyPassword}@proxy.example.invalid:1080`); }
  expect(uploads[0]?.body).toContain('"disabled":true');
  expect(uploads[1]?.body).toContain('"disabled":false');
  for (const secret of [token, proxyPassword]) {
    expect(await page.locator("body").innerText()).not.toContain(secret);
    expect(page.url()).not.toContain(secret);
    expect(await page.evaluate(() => JSON.stringify({ local: localStorage, session: sessionStorage, history: history.state }))).not.toContain(secret);
    expect(consoleMessages.join("\n")).not.toContain(secret);
  }
  expect(requests.every((item) => item.origin === new URL(page.url()).origin)).toBe(true);
  expect(requests.filter((item) => item.method !== "GET").map(({ method, pathname }) => ({ method, pathname }))).toEqual([
    // Account quality is an existing owning read transported with POST.
    { method: "POST", pathname: `/api/topology/nodes/${NODE}/account-quality/query` },
    { method: "POST", pathname: "/api/account-operations/upload-new" },
    { method: "POST", pathname: "/api/account-operations/upload-new" },
    { method: "POST", pathname: "/api/account-operations/upload-new" },
  ]);
  expect(requests.some((item) => /health|connection-test|monitoring-enable|monitoring-disable/u.test(item.pathname))).toBe(false);
});

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

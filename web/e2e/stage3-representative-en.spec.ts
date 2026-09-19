import { expect, test, type Page, type Route } from "@playwright/test";

const nodeID = "11111111-1111-4111-8111-111111111111";
const accountKey = "antigravity:english@example.invalid";
const commandID = "22222222-2222-4222-8222-222222222222";
const jobID = "33333333-3333-4333-8333-333333333333";

const session = {
  state: "authenticated",
  administrator: { id: "44444444-4444-4444-8444-444444444444", login_name: "e2e", display_name: "English Operator", auth_source: "local", role: "super_admin", status: "enabled" },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "english-csrf",
  recovery_codes_remaining: 10,
  created_at: "2026-09-19T00:00:00Z",
  last_activity_at: "2026-09-19T00:00:00Z",
  idle_expires_at: "2026-09-19T01:00:00Z",
  absolute_expires_at: "2026-09-20T00:00:00Z",
  reauthenticated_until: null,
};

const node = {
  instance_id: nodeID,
  display_name: "English Node",
  node_type: "relay",
  driver_contract_version: "v1",
  management_endpoint: "https://node.invalid",
  secret_configured: true,
  capabilities: [],
  monitoring: { monitoring_active: true, effective_from: null, effective_to: null },
  lifecycle_status: "active",
  revision: "1",
  retired_at: null,
  retired_by: null,
  retire_reason: null,
};

const account = {
  account_key: accountKey,
  email: "english@example.invalid",
  provider: "antigravity",
  quality: "good",
  request_count: 2,
  success_count: 2,
  failure_count: 0,
  success_rate: 1,
  p95_latency_ms: 120,
  last_success_at: "2026-09-19T00:00:00Z",
  last_failure_at: null,
  last_failure_class: null,
};

const operation = {
  command_id: commandID,
  node_instance_id: nodeID,
  account_key: accountKey,
  operation_kind: "disable",
  execution_state: "outcome_unknown",
  result: null,
  error_code: null,
  lifecycle_overridden: false,
  same_account_overridden: false,
  created_at: "2026-09-19T00:00:00Z",
  updated_at: "2026-09-19T00:00:00Z",
};

const job = {
  job_id: jobID,
  operation_id: commandID,
  job_kind: "account_operation",
  status: "completed",
  attempt_count: 1,
  max_attempts: 3,
  available_at: "2026-09-19T00:00:00Z",
  started_at: "2026-09-19T00:00:01Z",
  completed_at: "2026-09-19T00:00:02Z",
  cancel_requested: false,
  error_code: null,
  outbox_status: "sent",
  created_at: "2026-09-19T00:00:00Z",
  updated_at: "2026-09-19T00:00:02Z",
  events: [],
};

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, headers: { "Cache-Control": "no-store" }, json: body });
}

async function installAuthenticatedRoutes(page: Page) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/api/bootstrap/status") return json(route, { status: "completed" });
    if (url.pathname === "/api/auth/session") return json(route, session);
    if (url.pathname === "/api/admins") return json(route, { items: [], next_cursor: null });
    if (url.pathname === "/api/assets/nodes") return json(route, { items: [node], next_cursor: null });
    if (url.pathname === `/api/assets/nodes/${nodeID}`) return json(route, { asset: node, predecessor: null, successor: null });
    if (url.pathname.endsWith("/account-quality/query") || url.pathname.endsWith("/account-quality")) return json(route, { instance_id: nodeID, window: "15m", items: [account], next_cursor: null });
    if (url.pathname.endsWith("/providers")) return json(route, { instance_id: nodeID, observed_at: "2026-09-19T00:00:00Z", providers: [] });
    if (url.pathname.endsWith("/binding")) return json(route, { relay_node_id: nodeID, resolution: "unbound", directory_freshness: "fresh", context_source: "none", observed_at: "2026-09-19T00:00:00Z" });
    if (url.pathname.includes("account-request-history") || url.pathname.includes("account-availability-occurrences") || url.pathname.includes("quality-incidents")) return json(route, { items: [], next_cursor: null });
    if (url.pathname.includes("duplicates") || url.pathname.includes("duplicate-history")) return json(route, { items: [], next_cursor: null });
    if (url.pathname === "/api/jobs") return json(route, { items: [job], next_cursor: null });
    if (url.pathname === `/api/jobs/${jobID}`) return json(route, job);
    if (url.pathname === `/api/account-operations/${commandID}`) return json(route, { operation });
    if (url.pathname === "/api/environment") return json(route, { environment_id: "e2e", environment_type: "production", name: "English acceptance" });
    if (url.pathname === "/api/assets/drivers") return json(route, { items: [] });
    if (url.pathname === "/api/assets/provider-policies/current") return json(route, { status: "not_configured" });
    return json(route, { items: [], next_cursor: null });
  });
}

test.describe("Stage 3 representative English surfaces", () => {
  test.use({ locale: "en" });

  test("renders the Auth surface in English", async ({ page }) => {
    await page.route("**/api/bootstrap/status", (route) => json(route, { status: "completed" }));
    await page.route("**/api/auth/session", (route) => json(route, { code: "unauthorized", message: "unauthorized", request_id: "english-auth" }, 401));
    await page.goto("/");
    await expect(page.getByTestId("login-page")).toBeVisible();
    await expect(page.getByTestId("locale-selector")).toHaveValue("en");
    await page.getByTestId("login-activation-link").click();
    await expect(page.getByTestId("activation-page")).toBeVisible();
    await expect(page.locator("body")).not.toContainText(/auth\.|common\.|topology\./u);
  });

  test("renders Management, data, operation, and Jobs surfaces in English", async ({ page }) => {
    await installAuthenticatedRoutes(page);
    await page.goto("/");
    await expect(page.getByTestId("management-page")).toBeVisible();
    await expect(page.getByTestId("locale-selector")).toHaveValue("en");
    await expect(page.getByTestId("management-page")).toContainText("Persistent jobs");

    await page.getByTestId("management-nav-jobs").click();
    await expect(page.getByTestId("jobs-page")).toBeVisible();
    await expect(page.getByTestId("jobs-card")).toContainText("Persistent jobs");
    await expect(page.getByTestId("job-row")).toBeVisible();

    await page.goto(`/topology?instance_id=${nodeID}`);
    await expect(page.getByTestId("topology-page")).toBeVisible();
    await page.getByTestId("account-query").click();
    const accountDetail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
    await expect(accountDetail).toBeVisible();
    await accountDetail.click();
    await page.getByTestId("account-operations-tab").click();
    await page.getByTestId("account-operation-command-id").fill(commandID);
    await page.getByTestId("account-operation-read").click();
    await expect(page.getByTestId("account-operation-result")).toContainText("Outcome unknown");
    await expect(page.locator("body")).not.toContainText(/translation(?:\.|$)|operations\.|topology\./u);
  });
});

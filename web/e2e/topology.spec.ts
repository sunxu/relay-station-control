import { expect, test } from "@playwright/test";

const nodes = ["11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"];
const asset = (id: string, name: string) => ({ instance_id: id, display_name: name, node_type: "relay", driver_contract_version: "v1", management_endpoint: "https://node.invalid", secret_configured: false, capabilities: [], monitoring: { active: true, effective_from: null, effective_to: null } });
const session = { state: "authenticated", administrator: { id: "00000000-0000-4000-8000-000000000001", login_name: "e2e", display_name: "E2E", auth_source: "local", role: "super_admin", status: "enabled", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }, mfa: { required: true, completed: true, method: "totp" }, csrf_token: "c", created_at: "2026-01-01T00:00:00Z", last_activity_at: "2026-01-01T00:00:00Z", idle_expires_at: "2099-01-01T00:30:00Z", absolute_expires_at: "2099-01-01T12:00:00Z", reauthenticated_until: null, recovery_codes_remaining: 10 };

test("Topology compatibility is the read-only Monitoring diagnostic surface", async ({ page }) => {
  const requests: string[] = [];
  await page.route("**/*", async (route) => {
    const request = route.request(); const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) return route.continue();
    requests.push(`${request.method()} ${url.pathname}${url.search}`);
    if (request.method() !== "GET") throw new Error(`unexpected Monitoring mutation: ${request.method()} ${url.pathname}`);
    if (url.pathname === "/api/bootstrap/status") return route.fulfill({ json: { status: "completed" } });
    if (url.pathname === "/api/auth/session") return route.fulfill({ json: session });
    if (url.pathname === "/api/assets/nodes") return route.fulfill({ json: { items: [asset(nodes[0], "Node A"), asset(nodes[1], "Node B")], next_cursor: null } });
    if (url.pathname === `/api/assets/nodes/${nodes[0]}` || url.pathname === `/api/assets/nodes/${nodes[1]}`) return route.fulfill({ json: asset(url.pathname.endsWith(nodes[1]) ? nodes[1] : nodes[0], url.pathname.endsWith(nodes[1]) ? "Node B" : "Node A") });
    if (url.pathname.endsWith("/providers")) return route.fulfill({ json: { instance_id: url.pathname.includes(nodes[1]) ? nodes[1] : nodes[0], observed_at: "2026-09-07T00:00:00Z", providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: "2026-09-07T00:00:00Z", snapshot_freshness: "fresh", health_scheduled_at: "2026-09-07T00:05:00Z", health_degraded: false, health_reason: null }] } });
    if (url.pathname.includes("/relay-bindings/nodes/")) return route.fulfill({ json: { relay_node_id: nodes[0], current_binding: null, resolution: "unbound", directory_freshness: "unknown", context_source: "none", account_context: null, observed_at: "2026-09-07T00:00:00Z", last_success_observation_at: null } });
    if (url.pathname.includes("duplicate-history")) return route.fulfill({ json: { involvement: "historical", instance_id: nodes[0], observed_at: "2026-09-07T00:00:00Z", items: [], next_cursor: null } });
    if (url.pathname.includes("cross-node-duplicate-occurrences")) return route.fulfill({ json: { items: [], next_cursor: null } });
    if (url.pathname.includes("/evidence")) return route.fulfill({ json: { items: [], next_cursor: null } });
    throw new Error(`unhandled Monitoring API: ${url.pathname}`);
  });
  await page.goto(`/topology?instance_id=${nodes[0]}`);
  await expect(page.getByTestId("monitoring-page")).toBeVisible();
  await expect(page.getByTestId("monitoring-view")).toBeVisible();
  expect(requests.some((request) => request.includes("account-quality") || request.includes("problem-accounts") || request.includes("account-operations"))).toBe(false);
  expect(requests.some((request) => /health|connection-test|monitoring-(enable|disable)/.test(request))).toBe(false);
  await expect(page.getByText("unbound", { exact: true })).toBeVisible();
  await page.getByTestId("monitoring-node-selector").click();
  await page.getByTestId(`monitoring-node-option-${nodes[1]}`).click();
  await expect(page.getByText(`实例 ID：${nodes[1]}`)).toBeVisible();
  await page.goBack();
  await expect(page.getByText(`实例 ID：${nodes[0]}`)).toBeVisible();
  await page.goForward();
  await expect(page.getByText(`实例 ID：${nodes[1]}`)).toBeVisible();
  expect(requests.every((request) => request.startsWith("GET "))).toBe(true);
});

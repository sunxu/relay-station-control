import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

const nodeA = "11111111-1111-4111-8111-111111111111";
const nodeB = "22222222-2222-4222-8222-222222222222";
const session = { state: "authenticated", administrator: { id: "00000000-0000-4000-8000-000000000001", login_name: "e2e", display_name: "E2E", auth_source: "local", role: "super_admin", status: "enabled", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }, mfa: { required: true, completed: true, method: "totp" }, csrf_token: "c", created_at: "2026-01-01T00:00:00Z", last_activity_at: "2026-01-01T00:00:00Z", idle_expires_at: "2099-01-01T00:30:00Z", absolute_expires_at: "2099-01-01T12:00:00Z", reauthenticated_until: null, recovery_codes_remaining: 10 };
const asset = (id: string, name: string) => ({ instance_id: id, display_name: name, node_type: "relay", driver_contract_version: "v1", management_endpoint: "https://node.invalid", secret_configured: false, capabilities: [], monitoring: { active: true, effective_from: null, effective_to: null } });
const occurrence = (id: string, status: string) => ({ occurrence_id: id, account_key: `account-${id}`, status, severity: "high", evidence_state: "fresh", last_fully_verified_at: "2026-09-07T00:00:00Z", last_seen_at: "2026-09-07T00:00:00Z", resolved_at: status === "RESOLVED" ? "2026-09-07T00:00:00Z" : null, affected_nodes: [] });

async function install(page: Page) {
  const requests: { method: string; url: string; origin: string; pathname: string }[] = [];
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.resourceType() === "fetch" || request.resourceType() === "xhr") requests.push({ method: request.method(), url: request.url(), origin: url.origin, pathname: url.pathname });
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (request.method() !== "GET") throw new Error(`unexpected Monitoring mutation ${request.method()} ${url.pathname}`);
    if (url.pathname === "/api/bootstrap/status") return route.fulfill({ json: { status: "completed" } });
    if (url.pathname === "/api/auth/session") return route.fulfill({ json: session });
    if (url.pathname === "/api/account-inventory/poll-capacity") return route.fulfill({ json: { status: "ready", enabled: true, eligible_node_count: 1, effective_capacity: 10, concurrency: 1, request_timeout_ms: 1000, finalize_timeout_ms: 1000, lifecycle_timeout_ms: 1000, claim_timeout_ms: 1000, dispatch_margin_ms: 100, poll_start_grace_ms: 100, evaluated_slot: "2026-09-07T00:00:00Z", evaluated_at: "2026-09-07T00:00:00Z" } });
    if (url.pathname === "/api/assets/nodes") return route.fulfill({ json: url.searchParams.get("cursor") ? { items: [asset(nodeB, "Acceptance Relay B")], next_cursor: null } : { items: [asset(nodeA, "Acceptance Relay A")], next_cursor: "nodes-2" } });
    if (url.pathname === `/api/assets/nodes/${nodeA}` || url.pathname === `/api/assets/nodes/${nodeB}`) return route.fulfill({ json: asset(url.pathname.endsWith(nodeB) ? nodeB : nodeA, url.pathname.endsWith(nodeB) ? "Acceptance Relay B" : "Acceptance Relay A") });
    if (url.pathname.endsWith("/providers")) return route.fulfill({ json: { instance_id: url.pathname.endsWith(nodeB + "/providers") ? nodeB : nodeA, observed_at: "2026-09-07T00:00:00Z", providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: "2026-09-07T00:00:00Z", snapshot_freshness: "fresh", health_scheduled_at: "2026-09-07T00:05:00Z", health_degraded: false, health_reason: null }] } });
    if (url.pathname.includes("/relay-bindings/nodes/")) return route.fulfill({ json: { relay_node_id: nodeA, current_binding: null, resolution: "unbound", directory_freshness: "unknown", context_source: "none", account_context: null, observed_at: "2026-09-07T00:00:00Z", last_success_observation_at: null } });
    if (url.pathname === "/api/cross-node-duplicate-occurrences") return route.fulfill({ json: { items: [occurrence("current-1", "ACTIVE")], next_cursor: "current-2" } });
    if (url.pathname === `/api/topology/nodes/${nodeA}/duplicate-history`) return route.fulfill({ json: { involvement: "historical", instance_id: nodeA, observed_at: "2026-09-07T00:00:00Z", items: [occurrence("history-1", "RESOLVED")], next_cursor: "history-2" } });
    if (url.pathname.startsWith("/api/cross-node-duplicate-occurrences/current-1/evidence")) return route.fulfill({ json: { items: [{ observation_id: "evidence-1", instance_id: nodeA, observation_kind: "provider_snapshot", source_provider: "openai", source_scheduled_at: "2026-09-07T00:00:00Z", evaluation_at: "2026-09-07T00:00:00Z", recorded_at: "2026-09-07T00:00:00Z" }], next_cursor: "evidence-2" } });
    if (url.pathname.includes("/evidence")) return route.fulfill({ json: { items: [], next_cursor: null } });
    throw new Error(`unhandled Monitoring read ${url.pathname}`);
  });
  return requests;
}

async function assertInteractiveDiagnostics(page: Page) {
  await expect(page.getByTestId("monitoring-node-selector")).toBeVisible();
  await page.getByTestId("monitoring-node-next").click();
  await expect(page.getByText(`实例 ID：${nodeB}`)).toBeVisible();
  await page.getByTestId("monitoring-node-selector").click();
  await page.getByTestId(`monitoring-node-option-${nodeA}`).click();
  await expect(page.getByText(`实例 ID：${nodeA}`)).toBeVisible();
  await page.getByTestId("monitoring-provider-refresh").click();
  await page.getByTestId("monitoring-binding-refresh").click();
  await page.getByTestId("monitoring-current-refresh").click();
  await page.getByTestId("monitoring-current-next").click();
  await page.getByTestId("monitoring-history-status").click();
  await page.getByTestId("monitoring-history-status-resolved").click();
  await page.getByTestId("monitoring-history-refresh").click();
  await page.getByTestId("monitoring-history-next").click();
  await page.getByTestId("monitoring-evidence-expand-current-1").click();
  await expect(page.getByText("evidence-1")).toBeVisible();
  await page.getByTestId("monitoring-evidence-next").click();
  await page.getByTestId("monitoring-capacity-refresh").click();
  await expect(page.getByTestId("monitoring-open-nodes")).toBeVisible();
  await expect(page.getByTestId("monitoring-open-accounts")).toBeVisible();
  await expect(page.getByTestId("monitoring-open-problems")).toBeVisible();
  await expect(page.getByTestId("monitoring-open-assets")).toBeVisible();
}

for (const [name, viewport, locale] of [["zh-CN 1280", { width: 1280, height: 720 }, "zh-CN"], ["en 1280", { width: 1280, height: 720 }, "en"], ["zh-CN 1440", { width: 1440, height: 900 }, "zh-CN"]] as const) {
  test(name, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript((value) => localStorage.setItem("relay-control.locale", value), locale);
    const requests = await install(page);
    await page.goto(`/monitoring?instance_id=${nodeA}`);
    await expect(page.getByTestId("monitoring-page")).toBeVisible();
    await expect(page.getByTestId("monitoring-view")).toBeVisible();
    await assertInteractiveDiagnostics(page);
    expect(requests.every((request) => request.origin === new URL(page.url()).origin)).toBe(true);
    expect(requests.some((request) => /health|connection-test|monitoring-(enable|disable)/.test(request.pathname))).toBe(false);
    expect(requests.some((request) => /problem-accounts|account-quality|account-operations/.test(request.pathname))).toBe(false);
    expect(requests.some((request) => request.method !== "GET")).toBe(false);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    if (locale === "zh-CN") {
      const text = await page.getByTestId("monitoring-page").innerText();
      expect(text).not.toMatch(/\b(?:Unknown|History|Evidence|Health|Node|Account|Problems|Connection Test|Monitoring|Credential|Retry|Previous|Next)\b/u);
    } else {
      await expect(page.getByText("Monitoring", { exact: true }).first()).toBeVisible();
    }
  });
}

import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

const nodeId = "11111111-1111-4111-8111-111111111111";
const session = { state: "authenticated", administrator: { id: "00000000-0000-4000-8000-000000000001", login_name: "e2e", display_name: "E2E", auth_source: "local", role: "super_admin", status: "enabled", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }, mfa: { required: true, completed: true, method: "totp" }, csrf_token: "c", created_at: "2026-01-01T00:00:00Z", last_activity_at: "2026-01-01T00:00:00Z", idle_expires_at: "2099-01-01T00:30:00Z", absolute_expires_at: "2099-01-01T12:00:00Z", reauthenticated_until: null, recovery_codes_remaining: 10 };
const node = { instance_id: nodeId, display_name: "Acceptance Relay", node_type: "relay", driver_contract_version: "v1", management_endpoint: "https://node.invalid", secret_configured: false, capabilities: [], monitoring: { active: true, effective_from: null, effective_to: null } };

async function install(page: Page) {
  const requests: { method: string; url: string; origin: string; pathname: string }[] = [];
  await page.route("**/*", async (route) => {
    const request = route.request(); const url = new URL(request.url());
    if (request.resourceType() === "fetch" || request.resourceType() === "xhr") requests.push({ method: request.method(), url: request.url(), origin: url.origin, pathname: url.pathname });
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (request.method() !== "GET") throw new Error(`unexpected Monitoring mutation ${request.method()} ${url.pathname}`);
    if (url.pathname === "/api/bootstrap/status") return route.fulfill({ json: { status: "completed" } });
    if (url.pathname === "/api/auth/session") return route.fulfill({ json: session });
    if (url.pathname === "/api/assets/nodes") return route.fulfill({ json: { items: [node], next_cursor: null } });
    if (url.pathname === `/api/assets/nodes/${nodeId}`) return route.fulfill({ json: node });
    if (url.pathname.endsWith("/providers")) return route.fulfill({ json: { instance_id: nodeId, observed_at: "2026-09-07T00:00:00Z", providers: [] } });
    if (url.pathname.includes("/relay-bindings/nodes/")) return route.fulfill({ json: { relay_node_id: nodeId, current_binding: null, resolution: "unbound", directory_freshness: "unknown", context_source: "none", account_context: null, observed_at: "2026-09-07T00:00:00Z" } });
    if (url.pathname.includes("duplicate-history")) return route.fulfill({ json: { involvement: "historical", instance_id: nodeId, observed_at: "2026-09-07T00:00:00Z", items: [], next_cursor: null } });
    if (url.pathname.includes("cross-node-duplicate-occurrences") || url.pathname.includes("/evidence")) return route.fulfill({ json: { items: [], next_cursor: null } });
    throw new Error(`unhandled Monitoring read ${url.pathname}`);
  });
  return requests;
}

for (const [name, viewport, locale] of [["zh-CN 1280", { width: 1280, height: 720 }, "zh-CN"], ["en 1280", { width: 1280, height: 720 }, "en"], ["zh-CN 1440", { width: 1440, height: 900 }, "zh-CN"]] as const) {
  test(name, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript((value) => localStorage.setItem("relay-control.locale", value), locale);
    const requests = await install(page);
    await page.goto(`/monitoring?instance_id=${nodeId}`);
    await expect(page.getByTestId("monitoring-page")).toBeVisible();
    await expect(page.getByTestId("monitoring-view")).toBeVisible();
    await expect(page.getByTestId("monitoring-navigation")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(requests.every((request) => request.origin === new URL(page.url()).origin)).toBe(true);
    expect(requests.some((request) => /health|connection-test|monitoring-(enable|disable)/.test(request.pathname))).toBe(false);
    expect(requests.some((request) => /problem-accounts|account-quality|account-operations/.test(request.pathname))).toBe(false);
    if (locale === "zh-CN") {
      const text = await page.getByTestId("monitoring-page").innerText();
      expect(text).not.toMatch(/\b(?:Node|Evidence|Health|Connection Test|Monitoring|Credential|Account|Problems)\b/u);
    } else {
      await expect(page.getByText("Monitoring", { exact: true }).first()).toBeVisible();
    }
  });
}

import { expect, test } from "@playwright/test";

const nodes = ["11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"];
const asset = (id: string, name: string) => ({ instance_id: id, display_name: name, node_type: "relay", driver_contract_version: "v1", management_endpoint: "https://node.invalid", secret_configured: false, capabilities: [], monitoring: { active: true, effective_from: null, effective_to: null } });
const occurrence = { occurrence_id: "33333333-3333-4333-8333-333333333333", environment_id: "env", account_key: "openai:user@example.invalid", conflict_type: "cross_node_duplicate_ownership", status: "ACTIVE", severity: "Critical", first_seen_at: "2026-09-07T00:00:00Z", last_seen_at: "2026-09-07T00:01:00Z", resolved_at: null, evidence_state: "degraded", last_fully_verified_at: null, latest_evaluation_id: null, affected_nodes: nodes };
const session = { state: "authenticated", administrator: { id: "00000000-0000-4000-8000-000000000001", login_name: "e2e", display_name: "E2E", auth_source: "local", role: "super_admin", status: "enabled", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }, mfa: { required: true, completed: true, method: "totp" }, csrf_token: "c", created_at: "2026-01-01T00:00:00Z", last_activity_at: "2026-01-01T00:00:00Z", idle_expires_at: "2099-01-01T00:30:00Z", absolute_expires_at: "2099-01-01T12:00:00Z", reauthenticated_until: null, recovery_codes_remaining: 10 };

test("Topology covers responsive navigation, independent reads, pagination, and readonly recovery", async ({ page }) => {
  const requests: string[] = []; let bProviderReads = 0; let expireEvidence = false;
  await page.route("**/*", async (route) => {
    const request = route.request(); const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) return route.continue();
    requests.push(`${request.method()} ${url.pathname}${url.search}`);
    if (request.method() !== "GET") throw new Error(`unexpected non-GET request: ${request.method()} ${url.pathname}`);
    if (url.pathname === "/api/bootstrap/status") return route.fulfill({ json: { status: "completed" } });
    if (url.pathname === "/api/auth/session") return route.fulfill({ json: session });
    if (url.pathname === "/api/assets/nodes") return route.fulfill({ json: url.searchParams.has("cursor") ? { items: [asset(nodes[1], "Node B")], next_cursor: null } : { items: [asset(nodes[0], "Node A")], next_cursor: "nodes-page-2" } });
    if (url.pathname === `/api/assets/nodes/${nodes[0]}`) return route.fulfill({ json: asset(nodes[0], "Node A") });
    if (url.pathname === `/api/assets/nodes/${nodes[1]}`) return route.fulfill({ json: asset(nodes[1], "Node B") });
    if (url.pathname.endsWith("/providers")) { if (url.pathname.includes(nodes[1])) { bProviderReads += 1; if (bProviderReads === 1) return route.fulfill({ status: 503, json: { code: "unavailable", message: "unavailable", request_id: "fixture" } }); } return route.fulfill({ json: { instance_id: url.pathname.includes(nodes[1]) ? nodes[1] : nodes[0], observed_at: "2026-09-07T00:00:00Z", providers: [{ provider: "openai", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: "2026-09-07T00:00:00Z", snapshot_freshness: "fresh", health_scheduled_at: "2026-09-07T00:05:00Z", health_degraded: true, health_reason: "transport_failed" }, { provider: "anthropic", monitoring_status: "active", state: "current", current_scheduled_at: null, last_complete_at: "2026-09-06T00:00:00Z", snapshot_freshness: "stale", health_scheduled_at: "2026-09-07T00:05:00Z", health_degraded: true, health_reason: "contract_invalid" }, { provider: "gemini", monitoring_status: "active", state: null, current_scheduled_at: null, last_complete_at: null, snapshot_freshness: "unknown", health_scheduled_at: null, health_degraded: null, health_reason: null }] } }); }
    if (url.pathname.includes("/relay-bindings/nodes/")) return route.fulfill({ json: { relay_node_id: url.pathname.includes(nodes[1]) ? nodes[1] : nodes[0], current_binding: null, gateway_instance_id: null, gateway_account_id: null, resolution: "unbound", directory_freshness: "unknown", context_source: "none", account_context: null, observed_at: "2026-09-07T00:00:00Z", last_success_observation_at: null } });
    if (url.pathname.includes("/evidence")) { if (expireEvidence) return route.fulfill({ status: 401, json: { code: "unauthorized", message: "unauthorized", request_id: "fixture" } }); return route.fulfill({ json: { items: url.searchParams.has("cursor") ? [] : [{ observation_id: "44444444-4444-4444-8444-444444444444", instance_id: nodes[0], observation_kind: "owner_confirmed", source_provider: "openai", source_scheduled_at: "2026-09-07T00:00:00Z", source_completed_at: "2026-09-07T00:00:01Z", evaluation_id: "55555555-5555-4555-8555-555555555555", evaluation_at: "2026-09-07T00:00:02Z", recorded_at: "2026-09-07T00:00:03Z" }], next_cursor: url.searchParams.has("cursor") ? null : "evidence-page-2" } }); }
    if (url.pathname.includes("duplicate-history")) return route.fulfill({ json: { involvement: "historical", instance_id: nodes[0], observed_at: "2026-09-07T00:00:00Z", items: url.searchParams.has("cursor") ? [] : [{ ...occurrence, status: url.searchParams.get("status") ?? "ACTIVE" }], next_cursor: url.searchParams.has("cursor") ? null : "history-page-2" } });
    if (url.pathname.includes("cross-node-duplicate-occurrences")) return route.fulfill({ json: { items: url.searchParams.has("cursor") ? [] : [occurrence], next_cursor: url.searchParams.has("cursor") ? null : "current-page-2" } });
    throw new Error(`unhandled API fixture: ${request.method()} ${url.pathname}`);
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/topology?instance_id=${nodes[0]}`);
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await expect(page.getByText("fresh", { exact: true }).first()).toBeVisible();
  const currentCard = page.locator(".ant-card").filter({ has: page.getByText("Ownership Fact · 当前 duplicate", { exact: true }) });
  const historyCard = page.locator(".ant-card").filter({ has: page.getByText("Ownership Fact · 历史评估涉及", { exact: true }) });
  await test.step("current/history have independent pagination and status", async () => {
    await page.getByRole("button", { name: "Current 下一页" }).click();
    await expect(currentCard.getByText("没有符合条件的 occurrence")).toBeVisible();
    await expect(historyCard.getByText(occurrence.account_key)).toBeVisible();
    await page.getByRole("button", { name: "Current 首页" }).click();
    await expect(currentCard.getByText(occurrence.account_key)).toBeVisible();
    const filter = page.getByRole("combobox", { name: "历史状态" });
    await filter.focus();
    await filter.press("ArrowDown");
    await filter.press("ArrowDown");
    await filter.press("Enter");
    await expect.poll(() => requests.some((r) => r.includes("duplicate-history?status=RESOLVED"))).toBe(true);
    await expect(historyCard.getByText("RESOLVED", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "History 下一页" }).click();
    await expect(historyCard.getByText("没有符合条件的 occurrence")).toBeVisible();
    await expect(currentCard.getByText(occurrence.account_key)).toBeVisible();
    await page.getByRole("button", { name: "History 首页" }).click();
    await expect(historyCard.getByText(occurrence.account_key)).toBeVisible();
  });
  await test.step("evidence pagination is an actual expanded UI read", async () => {
    await currentCard.getByRole("button", { name: "Expand row" }).click();
    await expect(page.getByRole("button", { name: "Evidence 下一页" })).toBeVisible();
    await page.getByRole("button", { name: "Evidence 下一页" }).click();
    await expect(page.getByRole("button", { name: "Evidence 下一页" })).toBeDisabled();
    await expect.poll(() => requests.some((r) => r.includes("/evidence?cursor=evidence-page-2"))).toBe(true);
    await page.getByRole("button", { name: "Evidence 首页" }).click();
    await expect(page.getByText("owner_confirmed", { exact: true })).toBeVisible();
  });
  await test.step("Node pagination, keyboard selection, local failure and recovery", async () => {
    await page.getByRole("button", { name: "下一页 Node" }).click();
    const selector = page.getByRole("combobox", { name: "Relay Node" });
    await selector.focus();
    await selector.press("ArrowDown");
    await selector.press("ArrowDown");
    await selector.press("Enter");
    await expect(page.getByText(`Instance ID：${nodes[1]}`)).toBeVisible();
    await expect(page.getByText("读取不可用（unavailable）")).toBeVisible();
    await expect(page.getByText("unbound", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "刷新 Provider" }).click();
    await expect(page.getByText("fresh", { exact: true }).first()).toBeVisible();
    await page.goBack();
    await expect(page.getByText(`Instance ID：${nodes[0]}`)).toBeVisible();
    await page.goForward();
    await expect(page.getByText(`Instance ID：${nodes[1]}`)).toBeVisible();
    await page.reload();
    await expect(page.getByText(`Instance ID：${nodes[1]}`)).toBeVisible();
  });
  await test.step("desktop and mobile show both badges without page overflow", async () => {
    await expect(page.getByText("fresh", { exact: true }).first()).toBeVisible();
    await page.screenshot({ path: "/Volumes/DevRAM/tmp/topology-desktop.png", fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByText("stale", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("degraded", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("not-yet-observed", { exact: true })).toBeVisible();
    await expect(page.getByText("健康观测：2026-09-07 00:05:00 UTC").first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await currentCard.scrollIntoViewIfNeeded();
    await expect(currentCard.getByText(occurrence.account_key)).toBeVisible();
    await historyCard.scrollIntoViewIfNeeded();
    await expect(historyCard.getByText(occurrence.account_key)).toBeVisible();
    await page.screenshot({ path: "/Volumes/DevRAM/tmp/topology-390.png", fullPage: true });
  });
  await test.step("evidence 401 clears the authenticated page", async () => {
    expireEvidence = true;
    await currentCard.getByRole("button", { name: "Expand row" }).click();
    await expect(page.getByTestId("login-page")).toBeVisible();
    await expect(page.getByTestId("topology-page")).toHaveCount(0);
    expect(requests.every((r) => r.startsWith("GET "))).toBe(true);
    expect(requests.every((r) => !r.includes(occurrence.account_key))).toBe(true);
    expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(occurrence.account_key);
  });
});

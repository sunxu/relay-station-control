import { expect, test } from "@playwright/test";

const firstID = "10000000-0000-4000-8000-000000000001";
const secretReference = "vault://e2e/NODE-SECRET-MUST-NOT-RENDER";

const session = {
  state: "authenticated",
  administrator: {
    id: "10000000-0000-4000-8000-000000000099",
    login_name: "node-e2e",
    display_name: "Node E2E Operator",
    auth_source: "local",
    role: "super_admin",
    status: "enabled",
  },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "node-e2e-csrf",
};

function node(instance_id: string, display_name: string, revision: string, lifecycle_status: "active" | "retired" = "active") {
  return {
    instance_id,
    display_name,
    node_type: "cliproxyapi",
    driver_contract_version: "v1",
    management_endpoint: "http://node.invalid:8317",
    secret_configured: true,
    capabilities: ["management_account_inventory_read"],
    monitoring: { monitoring_active: false, effective_from: null, effective_to: null },
    lifecycle_status,
    revision,
    created_at: "2026-09-13T00:00:00Z",
    updated_at: "2026-09-13T00:00:00Z",
    retired_at: lifecycle_status === "retired" ? "2026-09-13T00:05:00Z" : null,
    retired_by: lifecycle_status === "retired" ? session.administrator.id : null,
    retire_reason: lifecycle_status === "retired" ? "replacement" : null,
  };
}

test("authenticated administrator uses Node lifecycle and explicit Stage 3 controls", async ({ page, baseURL }) => {
  type NodeFixture = ReturnType<typeof node>;
  let current: NodeFixture | undefined = node(firstID, "Primary Node", "1");
  let replacementID: string | undefined;
  const history = new Map<string, NodeFixture>();
  const requests: string[] = [];
  const browserRequests: Array<{ method: string; url: string; origin: string; pathname: string }> = [];
  page.on("request", (request) => {
    if (request.resourceType() === "fetch" || request.resourceType() === "xhr") {
      const requestURL = new URL(request.url());
      browserRequests.push({ method: request.method(), url: request.url(), origin: requestURL.origin, pathname: requestURL.pathname });
    }
  });

  await page.route((url) => url.pathname.startsWith("/api/"), async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    requests.push(`${request.method()} ${url.pathname}`);
    const json = (body: unknown, status = 200) => route.fulfill({
      status,
      headers: { "Cache-Control": "no-store", "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    if (url.pathname === "/api/bootstrap/status") return json({ status: "completed" });
    if (url.pathname === "/api/auth/session") return json(session);
    if (url.pathname === "/api/environment") return json({ environment_id: "e2e", environment_type: "development", name: "Node E2E" });
    if (url.pathname === "/api/assets/gateway") return json({ status: "not_registered", gateway: null });
    if (url.pathname === "/api/assets/gateways") return json({ items: [], next_cursor: null, gateway_counts: { active: 0, retired: 0, total: 0 } });
    if (url.pathname === "/api/assets/drivers") return json({ items: [{ node_type: "cliproxyapi", driver_contract_version: "v1", display_name: "CLIProxyAPI", lifecycle_status: "active", capabilities: ["management_account_inventory_read"] }] });
    if (url.pathname === "/api/assets/provider-policies/current") return json({ status: "not_configured", node_type: "cliproxyapi", driver_contract_version: "v1" });

    if (request.method() === "GET" && url.pathname === "/api/assets/nodes") {
      const lifecycle = url.searchParams.get("lifecycle") ?? "active";
      const retired = [...history.values()];
      const items = lifecycle === "active" ? (current ? [current] : []) : lifecycle === "retired" ? retired : [...(current ? [current] : []), ...retired];
      return json({ items, next_cursor: null, node_counts: { active: current ? 1 : 0, retired: retired.length, total: retired.length + (current ? 1 : 0) } });
    }
    const health = url.pathname.match(/^\/api\/assets\/nodes\/([^/]+)\/health$/);
    if (request.method() === "GET" && health) {
      return json({ result: "success", reachable: true, reason: "none", latency_ms: 8 });
    }
    const connectionTest = url.pathname.match(/^\/api\/assets\/nodes\/([^/]+)\/connection-test$/);
    if (request.method() === "POST" && connectionTest) {
      return json({ result: "success", reachable: true, reason: "none", latency_ms: 9 });
    }
    const monitoring = url.pathname.match(/^\/api\/assets\/nodes\/([^/]+)\/monitoring-(enable|disable)$/);
    if (request.method() === "POST" && monitoring) {
      if (!current || monitoring[1] !== current.instance_id) return json({ code: "asset_not_found" }, 404);
      const enabled = monitoring[2] === "enable";
      current = { ...current, monitoring: { monitoring_active: enabled, effective_from: enabled ? "2026-09-13T00:10:00Z" : null, effective_to: null } };
      return json({
        result: enabled ? "enabled" : "disabled",
        instance_id: current.instance_id,
        lifecycle_status: "active",
        revision: current.revision,
        boundary: "2026-09-13T00:10:00Z",
        monitoring_active: enabled,
        monitoring_activation_id: enabled ? "10000000-0000-4000-8000-000000000003" : null,
        effective_from: enabled ? "2026-09-13T00:10:00Z" : null,
        effective_to: null,
        closed_monitoring_count: enabled ? 0 : 1,
        cancelled_future_monitoring_count: enabled ? 0 : 1,
      });
    }
    const detail = url.pathname.match(/^\/api\/assets\/nodes\/([^/]+)$/);
    if (request.method() === "GET" && detail) {
      const asset = detail[1] === current?.instance_id ? current : history.get(detail[1]);
      if (!asset) return json({ code: "asset_not_found" }, 404);
      return json({ asset, predecessor: asset.instance_id === replacementID ? { old_instance_id: firstID, new_instance_id: replacementID, replaced_at: "2026-09-13T00:03:00Z" } : null, successor: asset.instance_id === firstID ? { old_instance_id: firstID, new_instance_id: replacementID, replaced_at: "2026-09-13T00:03:00Z" } : null });
    }
    if (request.method() === "POST" && url.pathname === "/api/assets/nodes") {
      const body = request.postDataJSON() as { new_instance_id: string; display_name: string };
      current = node(body.new_instance_id, body.display_name, "1");
      return json({ asset: current }, 201);
    }
    if (request.method() === "PATCH" && detail) {
      const body = request.postDataJSON() as { display_name?: string };
      if (!current) return json({ code: "asset_not_found" }, 404);
      current = { ...current, display_name: body.display_name ?? current.display_name, revision: "2" };
      return json({ asset: current });
    }
    const mutation = url.pathname.match(/^\/api\/assets\/nodes\/([^/]+)\/(retire|replace)$/);
    if (request.method() === "POST" && mutation) {
      if (!current) return json({ code: "asset_not_found" }, 404);
      if (mutation[2] === "replace") {
        const body = request.postDataJSON() as { new_instance_id: string; display_name: string };
        replacementID = body.new_instance_id;
        const retired = { ...current, lifecycle_status: "retired" as const, retired_at: "2026-09-13T00:03:00Z", retired_by: session.administrator.id, retire_reason: "replacement" };
        history.set(retired.instance_id, retired);
        current = node(body.new_instance_id, body.display_name, "1");
        return json({ old_asset: retired, new_asset: current });
      }
      history.set(current.instance_id, { ...current, lifecycle_status: "retired", retired_at: "2026-09-13T00:04:00Z", retired_by: session.administrator.id, retire_reason: "administrator_retire" });
      current = undefined;
      return json({ result: "retired" });
    }
    throw new Error(`unexpected API request: ${request.method()} ${url.pathname}`);
  });

  await page.goto("/nodes");
  const card = page.getByTestId("nodes-card");
  await expect(card.getByText("Primary Node")).toBeVisible();
  expect(requests.some((request) => /\/api\/assets\/nodes\/[^/]+\/(health|connection-test|monitoring-(enable|disable))$/.test(request))).toBe(false);

  await card.getByTestId(`node-details-${firstID}`).click();
  const activeDetail = page.getByRole("dialog");
  await expect(activeDetail.getByTestId("node-management-operations")).toBeVisible();
  await expect(activeDetail.getByTestId("node-health-button")).toBeVisible();
  await expect(activeDetail.getByTestId("node-connection-test-button")).toBeVisible();
  await expect(activeDetail.getByTestId("node-monitoring-enable-button")).toBeVisible();
  await expect(activeDetail.getByTestId("node-monitoring-disable-button")).toBeVisible();
  await activeDetail.getByTestId("node-health-button").click();
  await expect(activeDetail.getByTestId("node-health-result")).toBeVisible();
  await activeDetail.getByTestId("node-connection-test-button").click();
  await expect(activeDetail.getByTestId("node-connection-test-result")).toBeVisible();
  await activeDetail.getByTestId("node-monitoring-enable-button").click();
  await expect(activeDetail.getByTestId("node-monitoring-result")).toContainText("enabled");
  await activeDetail.getByTestId("node-monitoring-disable-button").click();
  await page.getByTestId("node-monitoring-disable-confirm").click();
  await expect(activeDetail.getByTestId("node-monitoring-result")).toContainText("disabled");
  await page.getByTestId("node-detail-close").click();
  await expect(activeDetail).toBeHidden();

  await card.getByTestId(`node-edit-${firstID}`).click();
  await page.getByTestId("node-form-display-name").fill("Edited Node");
  await page.getByTestId("node-form-submit").click();
  await expect(card.getByText("Edited Node")).toBeVisible();

  await card.getByTestId(`node-replace-${firstID}`).click();
  const replacementInstance = page.getByTestId("node-replace-instance-id");
  await expect(replacementInstance).toHaveAttribute("readonly");
  await expect(replacementInstance).toHaveValue(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i);
  await page.getByTestId("node-form-display-name").fill("Replacement Node");
  await page.getByTestId("node-form-submit").click();
  await expect(card.getByText("Replacement Node")).toBeVisible();

  const lifecycleFilter = page.getByTestId("node-lifecycle-filter");
  await lifecycleFilter.click();
  await lifecycleFilter.press("ArrowDown");
  await lifecycleFilter.press("Enter");
  await expect(card.getByText("Edited Node")).toBeVisible();
  await card.getByTestId(`node-details-${firstID}`).click();
  const detailDialog = page.getByRole("dialog");
  await expect(detailDialog.getByText(replacementID!, { exact: true })).toBeVisible();
  await expect(detailDialog.getByText(firstID, { exact: true })).toBeVisible();
  await expect(card.getByTestId(`node-retire-${firstID}`)).toHaveCount(0);
  await expect(detailDialog.getByTestId("node-health-button")).toHaveCount(0);
  await expect(detailDialog.getByTestId("node-connection-test-button")).toHaveCount(0);
  await expect(detailDialog.getByTestId("node-monitoring-enable-button")).toHaveCount(0);
  await expect(detailDialog.getByTestId("node-monitoring-disable-button")).toHaveCount(0);

  expect(await page.getByText(secretReference, { exact: true }).count()).toBe(0);
  const controlOrigin = new URL(baseURL!).origin;
  expect(browserRequests.length).toBeGreaterThan(0);
  expect(browserRequests.every((request) => request.origin === controlOrigin)).toBe(true);
  expect(browserRequests.some((request) => request.origin !== controlOrigin)).toBe(false);
  expect(browserRequests.some((request) => /\/health$|connection-test|monitoring-(enable|disable)/.test(request.pathname) && request.origin !== controlOrigin)).toBe(false);
  expect(browserRequests.some((request) => request.url.includes("node.invalid") || request.url.includes(secretReference))).toBe(false);
  expect(requests).toContain(`POST /api/assets/nodes/${firstID}/replace`);
});

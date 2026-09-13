import { expect, test } from "@playwright/test";

const firstID = "00000000-0000-4000-8000-000000000001";
const replacementID = "00000000-0000-4000-8000-000000000002";
const retiredID = "00000000-0000-4000-8000-000000000003";
const secretReference = "vault://e2e/SHOULD-NOT-RENDER";

const session = {
  state: "authenticated",
  administrator: {
    id: "00000000-0000-4000-8000-000000000099",
    login_name: "e2e",
    display_name: "E2E Operator",
    auth_source: "local",
    role: "super_admin",
    status: "enabled",
  },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "e2e-csrf",
};

function gateway(instance_id: string, display_name: string, revision: string, lifecycle_status: "active" | "retired" = "active") {
  return {
    instance_id,
    display_name,
    management_endpoint: "http://gateway.invalid:8317",
    lifecycle_status,
    revision,
    singleton_id: lifecycle_status === "active" ? 1 : null,
    secret_configured: true,
    created_at: "2026-09-13T00:00:00Z",
    updated_at: "2026-09-13T00:00:00Z",
    retired_at: lifecycle_status === "retired" ? "2026-09-13T00:05:00Z" : null,
    retired_by: lifecycle_status === "retired" ? session.administrator.id : null,
    retire_reason: lifecycle_status === "retired" ? "replacement" : null,
  };
}

test("authenticated administrator can manage Gateway lifecycle through Control API only", async ({ page, baseURL }) => {
  test.setTimeout(90_000);
  type GatewayFixture = ReturnType<typeof gateway>;
  let current: GatewayFixture | undefined = gateway(firstID, "Primary Gateway", "1");
  const history = new Map<string, Record<string, unknown>>();
  let staleNextEdit = false;
  const requests: string[] = [];
  const browserRequestURLs: string[] = [];
  page.on("request", (request) => browserRequestURLs.push(request.url()));

  await page.route("**/api/**", async (route) => {
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
    if (url.pathname === "/api/environment") return json({ environment_id: "e2e", environment_type: "development", name: "Gateway E2E" });
    if (url.pathname === "/api/assets/gateway") return json({ status: current ? "configured" : "not_configured", gateway: current });
    if (url.pathname === "/api/assets/nodes") return json({ items: [], next_cursor: null });
    if (url.pathname === "/api/assets/drivers") return json({ items: [] });
    if (url.pathname === "/api/assets/provider-policies/current") return json({ status: "not_configured" });

    if (request.method() === "GET" && url.pathname === "/api/assets/gateways") {
      const lifecycle = url.searchParams.get("lifecycle") ?? "active";
      const values = [...history.values()];
      const items = lifecycle === "active" ? (current ? [current] : [])
        : lifecycle === "retired" ? values
        : [...(current ? [current] : []), ...values];
      return json({ items, next_cursor: null, gateway_counts: { active: current ? 1 : 0, retired: values.length, total: (current ? 1 : 0) + values.length } });
    }
    const detailMatch = url.pathname.match(/^\/api\/assets\/gateways\/([^/]+)$/);
    if (request.method() === "GET" && detailMatch) {
      const asset = detailMatch[1] === current?.instance_id ? current : history.get(detailMatch[1]);
      return asset ? json({ asset, predecessor: asset.instance_id === replacementID ? { old_instance_id: firstID, new_instance_id: replacementID } : null, successor: asset.instance_id === firstID ? { old_instance_id: firstID, new_instance_id: replacementID } : null }) : json({ code: "asset_not_found" }, 404);
    }

    if (request.method() === "POST" && url.pathname === "/api/assets/gateways") {
      const body = request.postDataJSON() as { new_instance_id: string; display_name: string; management_endpoint: string };
      current = gateway(body.new_instance_id, body.display_name, "1");
      return json({ asset: current });
    }
    const assetMutation = url.pathname.match(/^\/api\/assets\/gateways\/([^/]+)\/(retire|replace|health|connection-test)$/);
    const editMatch = url.pathname.match(/^\/api\/assets\/gateways\/([^/]+)$/);
    if (request.method() === "PATCH" && editMatch) {
      if (staleNextEdit) {
        staleNextEdit = false;
        return json({ code: "stale_revision", message: "stale revision" }, 409);
      }
      if (!current) return json({ code: "asset_not_found" }, 404);
      const body = request.postDataJSON() as { display_name?: string };
      current = { ...current, display_name: body.display_name ?? current.display_name, revision: "2", updated_at: "2026-09-13T00:01:00Z" };
      return json({ asset: current });
    }
    if (request.method() === "GET" && assetMutation?.[2] === "health") {
      return json({ instance_id: assetMutation[1], result: "healthy", observed_at: "2026-09-13T00:02:00Z" });
    }
    if (request.method() === "POST" && assetMutation) {
      const action = assetMutation[2];
      if (action === "health" || action === "connection-test") return json({ instance_id: assetMutation[1], result: "healthy", observed_at: "2026-09-13T00:02:00Z" });
      if (!current) return json({ code: "asset_not_found" }, 404);
      const body = request.postDataJSON() as { new_instance_id?: string };
      if (action === "replace") {
        const old = { ...current, lifecycle_status: "retired", singleton_id: null, retired_at: "2026-09-13T00:03:00Z", retire_reason: "replacement" };
        history.set(old.instance_id, old);
        current = gateway(body.new_instance_id ?? replacementID, "Replacement Gateway", "1");
        return json({ old_asset: old, new_asset: current });
      }
      const retired = { ...current, lifecycle_status: "retired", singleton_id: null, retired_at: "2026-09-13T00:04:00Z", retire_reason: "administrator_retire" };
      history.set(retired.instance_id, retired);
      current = undefined;
      return json({ asset: retired });
    }
    throw new Error(`unexpected API request: ${request.method()} ${url.pathname}`);
  });

  await page.goto("/assets");
  const card = page.getByTestId("gateway-management-card");
  await expect(card).toBeVisible();
  await expect(card.getByText("Primary Gateway")).toBeVisible();

  await card.getByRole("button", { name: /编\s*辑/ }).click();
  await page.getByLabel("显示名称").fill("Edited Gateway");
  await page.getByRole("button", { name: /保\s*存/ }).click();
  await expect(card.getByText("Edited Gateway")).toBeVisible();

  staleNextEdit = true;
  await card.getByRole("button", { name: /编\s*辑/ }).click();
  await page.getByLabel("显示名称").fill("Stale Edit");
  await page.getByRole("button", { name: /保\s*存/ }).click();
  await expect(page.getByText("资产已被其他管理员修改，请刷新后重试。")).toBeVisible();
  await page.getByRole("button", { name: /取\s*消/ }).click();

  await card.getByRole("button", { name: "Health" }).click();
  await expect(page.getByText("Health 检查已完成。")).toBeVisible();
  await card.getByRole("button", { name: "Connection Test" }).click();
  await expect(page.getByText("Connection Test 已完成。")).toBeVisible();

  await card.getByRole("button", { name: "Replace" }).click();
  await page.getByLabel("新 Instance ID").fill(replacementID);
  await page.getByRole("button", { name: /保\s*存/ }).click();
  await expect(card.getByText("Replacement Gateway")).toBeVisible();

  await card.getByRole("button", { name: /Retire/ }).click();
  await page.getByRole("button", { name: /退\s*役/ }).click();
  await expect(card.getByRole("button", { name: /登\s*记 Gateway/ })).toBeVisible();

  await card.getByRole("button", { name: /登\s*记 Gateway/ }).click();
  await page.getByLabel("新 Instance ID").fill(retiredID);
  await page.getByLabel("显示名称").fill("Registered Gateway");
  await page.getByLabel("Management endpoint").fill("http://registered-gateway.invalid:8317");
  await page.getByRole("button", { name: /保\s*存/ }).click();
  await expect(card.getByText("Registered Gateway")).toBeVisible();

  await card.getByRole("combobox", { name: "Gateway 生命周期过滤" }).click();
  await page.getByText("历史 Gateway", { exact: true }).click();
  await expect(card.getByText("Edited Gateway")).toBeVisible();
  await card.getByRole("button", { name: /详\s*情/ }).first().click();
  await expect(page.getByText(replacementID, { exact: true })).toBeVisible();
  await expect(page.getByText(firstID, { exact: true })).toBeVisible();
  await page.locator(".ant-modal-close").click();
  await card.getByRole("button", { name: /详\s*情/ }).nth(1).click();
  await expect(page.getByLabel("Gateway 详情").getByText(firstID, { exact: true })).toBeVisible();
  expect(await page.getByText(secretReference, { exact: true }).count()).toBe(0);

  const controlOrigin = new URL(baseURL!).origin;
  expect(requests.every((entry) => entry.startsWith("GET ") || entry.startsWith("POST ") || entry.startsWith("PATCH "))).toBe(true);
  expect(browserRequestURLs.every((url) => new URL(url).origin === controlOrigin)).toBe(true);
  expect(browserRequestURLs.some((url) => url.includes("gateway.invalid") || url.includes(secretReference))).toBe(false);
  expect(requests.some((entry) => entry === "POST /api/assets/gateways/" + firstID + "/connection-test")).toBe(true);
  expect(controlOrigin).toBe(new URL(baseURL!).origin);
});

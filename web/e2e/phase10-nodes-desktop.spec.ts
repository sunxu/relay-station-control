import { expect, test } from "@playwright/test";

test.use({ locale: "zh-CN", viewport: { width: 1440, height: 900 } });

const session = {
  state: "authenticated",
  administrator: { id: "00000000-0000-4000-8000-000000000099", login_name: "nodes-desktop", display_name: "节点验收", auth_source: "local", role: "super_admin", status: "enabled" },
  mfa: { required: true, completed: true, method: "totp" },
  csrf_token: "nodes-desktop-csrf",
};

test("renders the canonical Node workspace at the 1440 desktop contract", async ({ page }) => {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const json = (body: unknown) => route.fulfill({ status: 200, headers: { "Cache-Control": "no-store", "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (url.pathname === "/api/bootstrap/status") return json({ status: "completed" });
    if (url.pathname === "/api/auth/session") return json(session);
    if (url.pathname === "/api/assets/drivers") return json({ items: [{ node_type: "cliproxyapi", driver_contract_version: "v1", display_name: "CLIProxyAPI", lifecycle_status: "active", capabilities: ["management_health_read"] }] });
    if (url.pathname === "/api/assets/nodes") return json({ items: [{ instance_id: "00000000-0000-4000-8000-000000000101", display_name: "节点一", node_type: "cliproxyapi", driver_contract_version: "v1", management_endpoint: "http://node.invalid:8317", secret_configured: true, capabilities: ["management_health_read"], monitoring: { monitoring_active: false, effective_from: null, effective_to: null }, lifecycle_status: "active", revision: "1", retired_at: null, retired_by: null, retire_reason: null }], next_cursor: null });
    throw new Error(`unexpected API request: ${route.request().method()} ${url.pathname}`);
  });

  await page.goto("/nodes");
  await expect(page.getByTestId("nodes-page")).toBeVisible();
  await expect(page.getByTestId("nodes-registry")).toBeVisible();
  await expect(page.getByTestId("nodes-page")).toContainText("节点");
  await expect(page.getByTestId("node-register")).toBeVisible();
  const ordinaryCopy = await page.getByTestId("nodes-page").innerText();
  for (const forbidden of ["Node", "Replace", "Retire", "Health", "Connection Test", "Monitoring", "Credential", "Keep existing", "Set new credential", "Clear credential"]) {
    expect(ordinaryCopy).not.toContain(forbidden);
  }
  expect(await page.locator("html").evaluate((element) => element.scrollWidth <= window.innerWidth)).toBe(true);
});

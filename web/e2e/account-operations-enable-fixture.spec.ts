import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const enableEmail = process.env.ACCEPTANCE_ENABLE_EMAIL;
const nodePort = process.env.ACCEPTANCE_NODE_PORT;
const nodePassword = process.env.ACCEPTANCE_NODE_MANAGEMENT_PASSWORD;
const evidenceFile = process.env.ACCEPTANCE_ENABLE_FIXTURE_EVIDENCE_FILE;
if (!storageState || !enableEmail || !nodePort || !nodePassword || !evidenceFile) throw new Error("Enable fixture environment is incomplete");

test.use({ storageState });
test.setTimeout(720_000);

async function selectNode(page: Page) {
  const selector = page.getByTestId("relay-node-selector");
  await expect(selector).toBeVisible();
  await selector.click();
  await selector.press("ArrowDown");
  await selector.press("Enter");
}

async function openAccount(page: Page, accountKey: string, inventoryResponses: Array<{ status: number; items: Array<{ account_key?: string; email?: string; provider?: string; inventory?: { basic_status?: string; lifecycle?: string } }> }>) {
  const detail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
  const requests: string[] = [];
  page.on("request", (request) => { const path = new URL(request.url()).pathname; if (path.startsWith("/api/")) requests.push(`${request.method()} ${path}`); });
  await waitForInventoryStatus(page, inventoryResponses, accountKey, "reported_active", 315_000);
  await expect(detail).toHaveCount(1);
  await expect(detail).toBeVisible();
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-disable")).toBeVisible();
}

async function waitForInventoryStatus(
  page: Page,
  inventoryResponses: Array<{ status: number; items: Array<{ account_key?: string; email?: string; provider?: string; inventory?: { basic_status?: string; lifecycle?: string } }> }>,
  accountKey: string,
  status: string,
  timeout: number,
) {
  const query = page.getByTestId("account-query");
  await expect.poll(async () => {
    const before = inventoryResponses.length;
    await query.click();
    await expect.poll(() => inventoryResponses.length, { timeout: 10_000, intervals: [250, 500, 1000] }).toBeGreaterThan(before);
    const response = inventoryResponses.at(-1);
    const item = response?.items.find((candidate) => candidate.account_key === accountKey);
    return item?.inventory?.basic_status ?? "missing";
  }, { timeout, intervals: [1000, 2000, 5000] }).toBe(status);
}

test("proves the disabled Enable fixture through Node, Inventory, and Browser", async ({ page }) => {
  const nodeResponse = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, {
    headers: { Authorization: `Bearer ${nodePassword}` },
  });
  const nodeBody = await nodeResponse.json() as { files?: Array<{ provider?: string; type?: string; email?: string; source?: string; runtime_only?: boolean; disabled?: boolean }> };
  const nodeAccount = (nodeBody.files ?? []).find((file) => file.email?.trim().toLowerCase() === enableEmail.trim().toLowerCase());
  expect(nodeResponse.status()).toBe(200);
  expect(nodeAccount).toMatchObject({ provider: "antigravity", email: enableEmail, source: "file", runtime_only: false, disabled: false });

  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  const inventoryResponses: Array<{ status: number; items: Array<{ account_key?: string; email?: string; provider?: string; inventory?: { basic_status?: string; lifecycle?: string } }> }> = [];
  page.on("response", (response) => {
    if (!new URL(response.url()).pathname.includes("/account-quality/query")) return;
    void (async () => {
      try {
        const body = response.ok() ? await response.json() as { items?: typeof inventoryResponses[number]["items"] } : {};
        inventoryResponses.push({ status: response.status(), items: body.items ?? [] });
      } catch {
        // The response may be closed while the test is ending; no evidence is lost.
      }
    })();
  });
  await selectNode(page);
  await openAccount(page, `antigravity:${enableEmail}`, inventoryResponses);
  const disableResponse = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/disable");
  await page.getByTestId("account-disable").click();
  const disabled = await disableResponse;
  expect(disabled.status()).toBe(200);
  await expect(page.getByTestId("account-operations-panel").getByTestId("account-operation-result")).toContainText("已应用");
  const disabledNodeResponse = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, { headers: { Authorization: `Bearer ${nodePassword}` } });
  const disabledNodeBody = await disabledNodeResponse.json() as { files?: Array<{ email?: string; disabled?: boolean }> };
  expect(disabledNodeBody.files?.find((file) => file.email?.trim().toLowerCase() === enableEmail.trim().toLowerCase())?.disabled).toBe(true);
  await page.keyboard.press("Escape");
  await waitForInventoryStatus(page, inventoryResponses, `antigravity:${enableEmail}`, "disabled", 315_000);
  const inventoryItems = inventoryResponses.at(-1)?.items ?? [];
  writeFileSync(evidenceFile, JSON.stringify({ node: { provider: nodeAccount?.provider, email: nodeAccount?.email, source: nodeAccount?.source, runtime_only: nodeAccount?.runtime_only, disabled: nodeAccount?.disabled }, inventory_status: inventoryResponses.at(-1)?.status, inventory_items: inventoryItems }), { mode: 0o600 });
  const detail = page.getByTestId(`account-details-${encodeURIComponent(`antigravity:${enableEmail}`)}`);
  await expect(detail).toHaveCount(1);
  await expect(detail).toBeVisible();
  expect(inventoryResponses.at(-1)?.status).toBe(200);
  expect(inventoryItems).toContainEqual(expect.objectContaining({ account_key: `antigravity:${enableEmail}`, provider: "antigravity", email: enableEmail, inventory: expect.objectContaining({ basic_status: "disabled" }) }));

  writeFileSync(evidenceFile, JSON.stringify({ node: { provider: nodeAccount?.provider, email: nodeAccount?.email, source: nodeAccount?.source, runtime_only: nodeAccount?.runtime_only, disabled: nodeAccount?.disabled }, inventory_status: inventoryResponses.at(-1)?.status, inventory_items: inventoryItems, browser_account: `antigravity:${enableEmail}` }), { mode: 0o600 });
});

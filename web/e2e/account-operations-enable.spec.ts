import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const enableEmail = process.env.ACCEPTANCE_ENABLE_EMAIL;
const nodePort = process.env.ACCEPTANCE_NODE_PORT;
const nodePassword = process.env.ACCEPTANCE_NODE_MANAGEMENT_PASSWORD;
const evidenceFile = process.env.ACCEPTANCE_ENABLE_EVIDENCE_FILE;
if (!storageState || !enableEmail || !nodePort || !nodePassword || !evidenceFile) throw new Error("Enable acceptance environment is incomplete");
test.use({ storageState });
test.setTimeout(480_000);

type InventoryResponse = { status: number; items: Array<{ account_key?: string; email?: string; provider?: string; inventory?: { basic_status?: string; lifecycle?: string } }> };

async function waitForInventoryStatus(page: Page, responses: InventoryResponse[], accountKey: string, status: string, timeout: number) {
  const query = page.getByTestId("account-query");
  await expect.poll(async () => {
    const before = responses.length;
    await query.click();
    await expect.poll(() => responses.length, { timeout: 10_000, intervals: [250, 500, 1000] }).toBeGreaterThan(before);
    const item = responses.at(-1)?.items.find((candidate) => candidate.account_key === accountKey);
    return item?.inventory?.basic_status ?? "missing";
  }, { timeout, intervals: [1000, 2000, 5000] }).toBe(status);
}

async function openAccount(page: Page, accountKey: string, responses: InventoryResponse[]) {
  const detail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
  await waitForInventoryStatus(page, responses, accountKey, "disabled", 315_000);
  await expect(detail).toHaveCount(1);
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-enable")).toBeVisible();
  await expect(page.getByTestId("account-operations-panel")).toBeVisible();
}

test("completes the real Enable operation", async ({ page }) => {
  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  const inventoryResponses: InventoryResponse[] = [];
  page.on("response", (response) => {
    if (!new URL(response.url()).pathname.includes("/account-quality/query")) return;
    void (async () => {
      try {
        const body = response.ok() ? await response.json() as { items?: InventoryResponse["items"] } : {};
        inventoryResponses.push({ status: response.status(), items: body.items ?? [] });
      } catch {
        // The response may be closed while the test is ending.
      }
    })();
  });
  const node = page.getByTestId("relay-node-selector");
  await node.click();
  await node.press("ArrowDown");
  await node.press("Enter");
  const accountKey = `antigravity:${enableEmail}`;
  await openAccount(page, accountKey, inventoryResponses);
  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/enable");
  await page.getByTestId("account-enable").click();
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; operation_kind?: string; execution_state?: string } };
  expect(response.status()).toBe(200);
  expect(body.operation).toMatchObject({ operation_kind: "enable", execution_state: "remote_applied" });
  await expect(page.getByTestId("account-operations-panel").getByTestId("account-operation-result")).toContainText("已应用");
  const nodeResponse = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, { headers: { Authorization: `Bearer ${nodePassword}` } });
  const nodeBody = await nodeResponse.json() as { files?: Array<{ email?: string; disabled?: boolean }> };
  expect(nodeBody.files?.find((file) => file.email?.trim().toLowerCase() === enableEmail.trim().toLowerCase())?.disabled).toBe(false);
  await page.keyboard.press("Escape");
  await waitForInventoryStatus(page, inventoryResponses, accountKey, "reported_active", 315_000);
  const inventoryItems = inventoryResponses.at(-1)?.items ?? [];
  expect(inventoryItems).toContainEqual(expect.objectContaining({ account_key: accountKey, provider: "antigravity", email: enableEmail, inventory: expect.objectContaining({ basic_status: "reported_active" }) }));
  await expect(page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`)).toHaveCount(1);
  writeFileSync(evidenceFile, JSON.stringify({ command_id: body.operation?.command_id, email: enableEmail, node_disabled: false, inventory_status: inventoryResponses.at(-1)?.status, inventory_items: inventoryItems, browser_account: accountKey }), { mode: 0o600 });
});

import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { requireAcceptanceEnv } from "./acceptance-env";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const enableEmail = process.env.ACCEPTANCE_ENABLE_EMAIL;
const nodePort = process.env.ACCEPTANCE_NODE_PORT;
const nodePassword = process.env.ACCEPTANCE_NODE_MANAGEMENT_PASSWORD;
const evidenceFile = process.env.ACCEPTANCE_ENABLE_FIXTURE_EVIDENCE_FILE;
test.use({ storageState });
test.setTimeout(120_000);
test.beforeEach(() => requireAcceptanceEnv("enable-fixture", ["ACCEPTANCE_STORAGE_STATE", "ACCEPTANCE_ENABLE_EMAIL", "ACCEPTANCE_NODE_PORT", "ACCEPTANCE_NODE_MANAGEMENT_PASSWORD", "ACCEPTANCE_ENABLE_FIXTURE_EVIDENCE_FILE"]));
const inventoryWaitTimeout = 30_000;

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
  await waitForInventoryAccount(page, inventoryResponses, accountKey, inventoryWaitTimeout);
  await expect(detail).toHaveCount(1);
  await expect(detail).toBeVisible();
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-enable")).toBeVisible();
}

async function waitForInventoryAccount(
  page: Page,
  inventoryResponses: Array<{ status: number; items: Array<{ account_key?: string; email?: string; provider?: string; inventory?: { basic_status?: string; lifecycle?: string } }> }>,
  accountKey: string,
  timeout: number,
) {
  const query = page.getByTestId("account-query");
  await expect.poll(async () => {
    const before = inventoryResponses.length;
    await query.click();
    await expect.poll(() => inventoryResponses.length, { timeout: 10_000, intervals: [250, 500, 1000] }).toBeGreaterThan(before);
    return inventoryResponses.at(-1)?.items.some((candidate) => candidate.account_key === accountKey) ?? false;
  }, { timeout, intervals: [1000, 2000, 5000] }).toBe(true);
}

test("proves the disabled Enable fixture through Node, Inventory, and Browser", async ({ page }) => {
  const nodeResponse = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, {
    headers: { Authorization: `Bearer ${nodePassword}` },
  });
  const nodeBody = await nodeResponse.json() as { files?: Array<{ provider?: string; type?: string; email?: string; source?: string; runtime_only?: boolean; disabled?: boolean }> };
  const nodeAccount = (nodeBody.files ?? []).find((file) => file.email?.trim().toLowerCase() === enableEmail.trim().toLowerCase());
  expect(nodeResponse.status()).toBe(200);
  expect(nodeAccount).toMatchObject({ provider: "antigravity", email: enableEmail, source: "file", runtime_only: false, disabled: true });

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
  writeFileSync(evidenceFile, JSON.stringify({ node: { provider: nodeAccount?.provider, email: nodeAccount?.email, source: nodeAccount?.source, runtime_only: nodeAccount?.runtime_only, disabled: true }, browser_account: `antigravity:${enableEmail}` }), { mode: 0o600 });
});

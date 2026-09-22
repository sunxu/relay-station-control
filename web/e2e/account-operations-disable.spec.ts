import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { requireAcceptanceEnv } from "./acceptance-env";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const disableEmail = process.env.ACCEPTANCE_DISABLE_EMAIL;
const evidenceFile = process.env.ACCEPTANCE_DISABLE_EVIDENCE_FILE;
test.use({ storageState });
test.setTimeout(480_000);
test.beforeEach(() => requireAcceptanceEnv("disable", ["ACCEPTANCE_STORAGE_STATE", "ACCEPTANCE_DISABLE_EMAIL", "ACCEPTANCE_DISABLE_EVIDENCE_FILE"]));

async function openAccount(page: Page, accountKey: string) {
  const detail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
  let lastObservedCount = 0;
  const startedAt = Date.now();
  try {
    await expect.poll(async () => {
      lastObservedCount = await detail.count();
      if (lastObservedCount === 0) await page.getByTestId("account-query").click();
      lastObservedCount = await detail.count();
      return lastObservedCount;
    }, { timeout: 30_000, intervals: [250, 500, 1000] }).toBeGreaterThan(0);
  } catch (error) {
    const elapsed = Date.now() - startedAt;
    throw new Error(
      `DISABLE_FIXTURE_DISCOVERY_TIMEOUT layer=browser/inventory expected=${accountKey} observed_detail_count=${lastObservedCount} elapsed=${elapsed}ms`,
      { cause: error },
    );
  }
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-operation-read")).toBeVisible();
}

test("renders the real Disable operation result", async ({ page }) => {
  await page.goto("/accounts");
  await expect(page.getByTestId("accounts-page")).toBeVisible();
  const node = page.getByTestId("relay-node-selector");
  await node.click();
  await node.press("ArrowDown");
  await node.press("Enter");
  await openAccount(page, `antigravity:${disableEmail}`);
  await expect(page.getByTestId("account-operations-panel")).toBeVisible();
  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/disable");
  await page.getByTestId("account-disable").click();
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; operation_kind?: string; execution_state?: string } };
  expect(response.status()).toBe(200);
  expect(body.operation).toMatchObject({ operation_kind: "disable", execution_state: "remote_applied" });
  await expect(page.getByTestId("account-operations-panel").getByTestId("account-operation-result")).toContainText("已应用");
  writeFileSync(evidenceFile, JSON.stringify({ command_id: body.operation?.command_id, email: disableEmail }), { mode: 0o600 });
});

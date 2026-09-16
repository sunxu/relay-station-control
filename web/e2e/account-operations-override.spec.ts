import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { requireAcceptanceEnv } from "./acceptance-env";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const lifecycleEmail = process.env.ACCEPTANCE_OVERRIDE_LIFECYCLE_EMAIL;
const sameEmail = process.env.ACCEPTANCE_OVERRIDE_SAME_EMAIL;
const lifecycleCommandID = process.env.ACCEPTANCE_OVERRIDE_LIFECYCLE_COMMAND_ID;
const sameCommandID = process.env.ACCEPTANCE_OVERRIDE_SAME_COMMAND_ID;
const evidenceFile = process.env.ACCEPTANCE_OVERRIDE_EVIDENCE_FILE;
test.use({ storageState });
test.setTimeout(120_000);
test.beforeEach(() => requireAcceptanceEnv("override", ["ACCEPTANCE_STORAGE_STATE", "ACCEPTANCE_OVERRIDE_LIFECYCLE_EMAIL", "ACCEPTANCE_OVERRIDE_SAME_EMAIL", "ACCEPTANCE_OVERRIDE_LIFECYCLE_COMMAND_ID", "ACCEPTANCE_OVERRIDE_SAME_COMMAND_ID", "ACCEPTANCE_OVERRIDE_EVIDENCE_FILE"]));

async function selectNode(page: Page) {
  const selector = page.getByTestId("relay-node-selector");
  await expect(selector).toBeVisible();
  await selector.click();
  await selector.press("ArrowDown");
  await selector.press("Enter");
}

async function openAccount(page: Page, accountKey: string) {
  const detail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
  const query = page.getByTestId("account-query");
  await expect.poll(async () => {
    if (await detail.count() === 0) await query.click();
    return detail.count();
  }, { timeout: 30_000, intervals: [500, 1000, 2000] }).toBeGreaterThan(0);
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-operation-read")).toBeVisible();
}

async function loadTarget(page: Page, commandID: string) {
  await page.getByTestId("account-operation-command-id").fill(commandID);
  await page.getByTestId("account-operation-read").click();
  await expect(page.getByTestId("account-override-submit")).toBeVisible();
}

test("performs lifecycle and same-account overrides with closed-set reasons", async ({ page }) => {
  const requests: string[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path.includes("/lifecycle-override") || path.includes("/same-account-override")) requests.push(`${request.method()} ${path}`);
  });

  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);

  await openAccount(page, `antigravity:${lifecycleEmail}`);
  await loadTarget(page, lifecycleCommandID);
  expect(await page.getByTestId("account-override-detail").count()).toBe(1);
  await page.getByTestId("account-override-submit").click();
  await expect(page.getByTestId("account-override-cancel")).toBeVisible();
  await page.getByTestId("account-override-cancel").click();
  await expect(page.getByTestId("account-override-cancel")).toHaveCount(0);
  expect(requests).toEqual([]);

  const lifecycleResponsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname.endsWith(`/api/account-operations/${lifecycleCommandID}/lifecycle-override`));
  await page.getByTestId("account-override-submit").click();
  await page.getByTestId("account-override-confirm").click();
  const lifecycleResponse = await lifecycleResponsePromise;
  const lifecycleBody = await lifecycleResponse.json() as { operation?: { command_id?: string; execution_state?: string; lifecycle_overridden?: boolean; lifecycle_override_reason?: string } };
  expect(lifecycleResponse.status()).toBe(200);
  expect(lifecycleBody.operation).toMatchObject({ command_id: lifecycleCommandID, execution_state: "outcome_unknown", lifecycle_overridden: true, lifecycle_override_reason: "process_restarted" });
  await expect(page.getByTestId("account-operation-result")).toContainText("远端结果不确定");

  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);
  await openAccount(page, `antigravity:${sameEmail}`);
  await loadTarget(page, sameCommandID);
  await page.getByTestId("account-override-kind").click();
  await page.getByTestId("account-override-kind").press("ArrowDown");
  await page.getByTestId("account-override-kind").press("Enter");
  await page.getByTestId("account-override-submit").click();
  await expect(page.getByTestId("account-override-cancel")).toBeVisible();
  await page.getByTestId("account-override-cancel").click();
  await expect(page.getByTestId("account-override-cancel")).toHaveCount(0);
  expect(requests).toEqual([`POST /api/account-operations/${lifecycleCommandID}/lifecycle-override`]);

  const sameResponsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname.endsWith(`/api/account-operations/${sameCommandID}/same-account-override`));
  await page.getByTestId("account-override-submit").click();
  await page.getByTestId("account-override-confirm").click();
  const sameResponse = await sameResponsePromise;
  const sameBody = await sameResponse.json() as { operation?: { command_id?: string; execution_state?: string; same_account_overridden?: boolean; same_account_override_reason?: string } };
  expect(sameResponse.status()).toBe(200);
  expect(sameBody.operation).toMatchObject({ command_id: sameCommandID, execution_state: "outcome_unknown", same_account_overridden: true, same_account_override_reason: "process_restarted" });
  await expect(page.getByTestId("account-operation-result")).toContainText("远端结果不确定");

  writeFileSync(evidenceFile, JSON.stringify({ lifecycle: { target_command_id: lifecycleCommandID, reason: "process_restarted", response_status: lifecycleResponse.status() }, same_account: { target_command_id: sameCommandID, reason: "process_restarted", response_status: sameResponse.status() }, override_requests: requests, cancel_requests: 0 }), { mode: 0o600 });
});

import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { requireAcceptanceEnv } from "./acceptance-env";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const disableEmail = process.env.ACCEPTANCE_DISABLE_EMAIL;
const evidenceFile = process.env.ACCEPTANCE_SECURITY_REPLAY_EVIDENCE_FILE;

test.use({ storageState });
test.setTimeout(120_000);
test.beforeEach(() => requireAcceptanceEnv("security-replay", ["ACCEPTANCE_STORAGE_STATE", "ACCEPTANCE_DISABLE_EMAIL", "ACCEPTANCE_SECURITY_REPLAY_EVIDENCE_FILE"]));

type Evidence = {
  duplicate: Record<string, unknown>;
};

const evidence: Evidence = { duplicate: {} };

async function selectNode(page: Page): Promise<void> {
  const selector = page.getByTestId("relay-node-selector");
  await expect(selector).toBeVisible();
  await selector.click();
  await selector.press("ArrowDown");
  await selector.press("Enter");
}

async function openAccount(page: Page, email: string): Promise<void> {
  const key = `antigravity:${email}`;
  const detail = page.getByTestId(`account-details-${encodeURIComponent(key)}`);
  const query = page.getByTestId("account-query");
  await expect.poll(async () => {
    if (await detail.count() === 0) await query.click();
    return detail.count();
  }, { timeout: 30_000, intervals: [500, 1000, 2000] }).toBeGreaterThan(0);
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-operation-read")).toBeVisible();
}

test("prevents duplicate browser Disable submission", async ({ page }) => {
  const requests: string[] = [];
  page.on("request", (request) => { if (new URL(request.url()).pathname === "/api/account-operations/disable") requests.push(request.method()); });
  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);
  await openAccount(page, disableEmail);
  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/disable");
  const button = page.getByTestId("account-disable");
  await button.click();
  await button.dispatchEvent("click");
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; execution_state?: string } };
  expect(response.status()).toBe(200);
  expect(body.operation).toMatchObject({ execution_state: "remote_applied" });
  await expect(page.getByTestId("account-operation-result")).toContainText("已应用");
  expect(requests).toEqual(["POST"]);
  evidence.duplicate = { http_mutations: requests.length, command_id: body.operation?.command_id, execution_state: body.operation?.execution_state };
});

test.afterAll(() => writeFileSync(evidenceFile, JSON.stringify(evidence), { mode: 0o600 }));

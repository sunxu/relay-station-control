import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const removeEmail = process.env.ACCEPTANCE_REMOVE_EMAIL;
const nodePort = process.env.ACCEPTANCE_NODE_PORT;
const nodePassword = process.env.ACCEPTANCE_NODE_MANAGEMENT_PASSWORD;
const evidenceFile = process.env.ACCEPTANCE_REMOVE_EVIDENCE_FILE;
if (!storageState || !removeEmail || !nodePort || !nodePassword || !evidenceFile) throw new Error("Remove acceptance environment is incomplete");

test.use({ storageState });
test.setTimeout(120_000);

async function selectNode(page: Page) {
  const selector = page.getByTestId("relay-node-selector");
  await expect(selector).toBeVisible();
  await selector.click();
  await selector.press("ArrowDown");
  await selector.press("Enter");
}

async function openAccount(page: Page, accountKey: string) {
  const detail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
  await expect.poll(async () => {
    if (await detail.count() === 0) await page.getByTestId("account-query").click();
    return detail.count();
  }, { timeout: 30_000, intervals: [1000, 2000, 5000] }).toBeGreaterThan(0);
  await expect(detail).toBeVisible();
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-operation-read")).toBeVisible();
}

test("removes an existing account and preserves destructive cancel", async ({ page }) => {
  const accountKey = `antigravity:${removeEmail}`;
  const mutationRequests: string[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path === "/api/account-operations/remove") mutationRequests.push(`${request.method()} ${path}`);
  });

  const beforeNode = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, { headers: { Authorization: `Bearer ${nodePassword}` } });
  const beforeBody = await beforeNode.json() as { files?: Array<{ provider?: string; email?: string; source?: string; runtime_only?: boolean }> };
  expect(beforeNode.status()).toBe(200);
  expect(beforeBody.files?.find((file) => file.email?.trim().toLowerCase() === removeEmail.trim().toLowerCase())).toMatchObject({ provider: "antigravity", email: removeEmail, source: "file", runtime_only: false });

  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);
  await openAccount(page, accountKey);

  await page.getByTestId("account-remove").click();
  await expect(page.getByTestId("account-remove-cancel")).toBeVisible();
  await page.getByTestId("account-remove-cancel").click();
  expect(mutationRequests).toEqual([]);

  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/remove");
  await page.getByTestId("account-remove").click();
  await page.getByTestId("account-remove-confirm").click();
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; operation_kind?: string; execution_state?: string } };
  expect(response.status()).toBe(200);
  expect(body.operation).toMatchObject({ operation_kind: "remove", account_key: accountKey, execution_state: "remote_applied" });
  expect(body.operation?.command_id).toMatch(/^[0-9a-f-]{36}$/u);
  await expect(page.getByTestId("account-operations-panel").getByTestId("account-operation-result")).toContainText("已应用");
  expect(mutationRequests).toEqual(["POST /api/account-operations/remove"]);

  const afterNode = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, { headers: { Authorization: `Bearer ${nodePassword}` } });
  const afterBody = await afterNode.json() as { files?: Array<{ email?: string }> };
  expect(afterNode.status()).toBe(200);
  expect(afterBody.files?.some((file) => file.email?.trim().toLowerCase() === removeEmail.trim().toLowerCase())).toBe(false);

  writeFileSync(evidenceFile, JSON.stringify({ command_id: body.operation?.command_id, email: removeEmail, operation_kind: body.operation?.operation_kind, cancel_requests: [], browser_requests: mutationRequests }), { mode: 0o600 });
});

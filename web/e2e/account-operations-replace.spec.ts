import { expect, test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const replaceEmail = process.env.ACCEPTANCE_REPLACE_EMAIL;
const replacementCredential = process.env.ACCEPTANCE_REPLACE_CREDENTIAL_FILE;
const replacementMarker = process.env.ACCEPTANCE_REPLACE_SECRET_MARKER;
const nodePort = process.env.ACCEPTANCE_NODE_PORT;
const nodePassword = process.env.ACCEPTANCE_NODE_MANAGEMENT_PASSWORD;
const evidenceFile = process.env.ACCEPTANCE_REPLACE_EVIDENCE_FILE;
const browserConsoleFile = process.env.ACCEPTANCE_BROWSER_CONSOLE_FILE;
const discoveryOnly = process.env.ACCEPTANCE_REPLACE_DISCOVERY_ONLY === "1";
if (!storageState || !replaceEmail || !replacementCredential || !replacementMarker || !nodePort || !nodePassword || !evidenceFile || !browserConsoleFile) {
  throw new Error("Replace acceptance environment is incomplete");
}

test.use({ storageState });
test.setTimeout(120_000);

const consoleMessages: string[] = [];
test.beforeEach(({ page }) => page.on("console", (message) => consoleMessages.push(`${message.type()}: ${message.text()}`)));
test.afterEach(() => {
  writeFileSync(browserConsoleFile, consoleMessages.join("\n"), { mode: 0o600 });
  consoleMessages.length = 0;
});

type InventoryItem = { account_key?: string; email?: string; provider?: string; inventory?: { basic_status?: string; lifecycle?: string } };

async function selectNode(page: Page) {
  const selector = page.getByTestId("relay-node-selector");
  await expect(selector).toBeVisible();
  await selector.click();
  await selector.press("ArrowDown");
  await selector.press("Enter");
}

async function waitForInventory(page: Page, responses: Array<{ status: number; items: InventoryItem[] }>, accountKey: string) {
  const query = page.getByTestId("account-query");
  await expect.poll(async () => {
    const before = responses.length;
    await query.click();
    await expect.poll(() => responses.length, { timeout: 10_000, intervals: [250, 500, 1000] }).toBeGreaterThan(before);
    const item = responses.at(-1)?.items.find((candidate) => candidate.account_key === accountKey);
    return item?.account_key === accountKey;
  }, { timeout: 30_000, intervals: [1000, 2000, 5000] }).toBe(true);
}

async function openAccount(page: Page, responses: Array<{ status: number; items: InventoryItem[] }>, accountKey: string) {
  const detail = page.getByTestId(`account-details-${encodeURIComponent(accountKey)}`);
  await waitForInventory(page, responses, accountKey);
  await expect(detail).toHaveCount(1);
  await detail.click();
  await page.getByTestId("account-operations-tab").click();
  await expect(page.getByTestId("account-operation-read")).toBeVisible();
}

test("replaces an existing account through the real Control and pinned Node", async ({ page }) => {
  const accountKey = `antigravity:${replaceEmail}`;
  const responses: Array<{ status: number; items: InventoryItem[] }> = [];
  const controlRequests: string[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path === "/api/account-operations/replace-existing") controlRequests.push(`${request.method()} ${path}`);
  });
  page.on("response", (response) => {
    if (!new URL(response.url()).pathname.includes("/account-quality/query")) return;
    void (async () => {
      try {
        const body = response.ok() ? await response.json() as { items?: InventoryItem[] } : {};
        responses.push({ status: response.status(), items: body.items ?? [] });
      } catch {
        // The browser may close while the response is settling.
      }
    })();
  });

  const beforeNode = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, { headers: { Authorization: `Bearer ${nodePassword}` } });
  const beforeBody = await beforeNode.json() as { files?: Array<{ provider?: string; email?: string; source?: string; runtime_only?: boolean; disabled?: boolean }> };
  expect(beforeNode.status()).toBe(200);
  expect(beforeBody.files?.find((file) => file.email?.trim().toLowerCase() === replaceEmail.trim().toLowerCase())).toMatchObject({ provider: "antigravity", email: replaceEmail, source: "file", runtime_only: false, disabled: false });

  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);
  await openAccount(page, responses, accountKey);

  if (discoveryOnly) {
    writeFileSync(evidenceFile, JSON.stringify({ email: replaceEmail, discovery: "browser_account_present", browser_requests: controlRequests }), { mode: 0o600 });
    return;
  }

  const credentialInput = page.getByTestId("account-existing-credential-file");
  await credentialInput.setInputFiles(replacementCredential);
  await expect(page.getByTestId("account-replace-existing")).toBeEnabled();

  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/replace-existing");
  await page.getByTestId("account-replace-existing").click();
  await page.getByTestId("account-replace-confirm").click();
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; operation_kind?: string; account_key?: string; execution_state?: string } };
  expect(response.status(), JSON.stringify({ body })).toBe(200);
  expect(body.operation).toMatchObject({ operation_kind: "replace_existing", account_key: accountKey, execution_state: "remote_applied" });
  expect(body.operation?.command_id).toMatch(/^[0-9a-f-]{36}$/u);
  await expect(page.getByTestId("account-operations-panel").getByTestId("account-operation-result")).toContainText("已应用");
  await expect(page.getByTestId("account-existing-credential-file")).toHaveValue("");
  expect(controlRequests).toEqual(["POST /api/account-operations/replace-existing"]);
  expect((await page.content()).includes(replacementMarker)).toBe(false);

  const afterNode = await page.request.get(`http://127.0.0.1:${nodePort}/v0/management/auth-files`, { headers: { Authorization: `Bearer ${nodePassword}` } });
  const afterBody = await afterNode.json() as { files?: Array<{ provider?: string; email?: string; source?: string; runtime_only?: boolean; disabled?: boolean }> };
  const account = afterBody.files?.find((file) => file.email?.trim().toLowerCase() === replaceEmail.trim().toLowerCase());
  expect(afterNode.status()).toBe(200);
  expect(account).toMatchObject({ provider: "antigravity", email: replaceEmail, source: "file", runtime_only: false, disabled: false });

  writeFileSync(evidenceFile, JSON.stringify({ command_id: body.operation?.command_id, email: replaceEmail, operation_kind: body.operation?.operation_kind, inventory_status: responses.at(-1)?.status, browser_requests: controlRequests }), { mode: 0o600 });
});

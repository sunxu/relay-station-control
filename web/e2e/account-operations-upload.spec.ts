import { expect, test } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { requireAcceptanceEnv } from "./acceptance-env";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const credentialFile = process.env.ACCEPTANCE_UPLOAD_CREDENTIAL_FILE;
const uploadEmail = process.env.ACCEPTANCE_UPLOAD_EMAIL;
const secretMarker = process.env.ACCEPTANCE_UPLOAD_SECRET_MARKER;
const evidenceFile = process.env.ACCEPTANCE_UPLOAD_EVIDENCE_FILE;
const browserConsoleFile = process.env.ACCEPTANCE_BROWSER_CONSOLE_FILE;

test.use({ storageState });
test.beforeEach(() => requireAcceptanceEnv("upload", ["ACCEPTANCE_STORAGE_STATE", "ACCEPTANCE_UPLOAD_CREDENTIAL_FILE", "ACCEPTANCE_UPLOAD_EMAIL", "ACCEPTANCE_UPLOAD_SECRET_MARKER", "ACCEPTANCE_UPLOAD_EVIDENCE_FILE", "ACCEPTANCE_BROWSER_CONSOLE_FILE"]));

const browserConsoleMessages: string[] = [];

test.beforeEach(async ({ page }) => {
  page.on("console", (message) => browserConsoleMessages.push(`${message.type()}: ${message.text()}`));
});

test.afterEach(() => {
  writeFileSync(browserConsoleFile, browserConsoleMessages.join("\n"), { mode: 0o600 });
  browserConsoleMessages.length = 0;
});

test("uploads a new account through the real Control and pinned Node", async ({ page }) => {
  const controlRequests: Array<{ method: string; path: string }> = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/account-operations/upload-new") {
      controlRequests.push({ method: request.method(), path: url.pathname });
    }
  });

  await page.goto("/accounts");
  await expect(page.getByTestId("accounts-page")).toBeVisible();

  const nodeSelector = page.getByTestId("accounts-node-selector");
  await expect(nodeSelector).toBeVisible();
  await nodeSelector.click();
  await nodeSelector.press("ArrowDown");
  await nodeSelector.press("Enter");

  const uploadEntry = page.getByTestId("account-upload-new");
  await expect(uploadEntry).toBeVisible();
  await uploadEntry.click();

  const fileChooserPromise = page.waitForEvent("filechooser");
  await page.getByTestId("account-upload-new-file-button").click();
  await (await fileChooserPromise).setFiles(credentialFile);
  await expect(page.getByTestId(`account-import-preview-${uploadEmail.trim().toLowerCase()}`)).toBeVisible();

  const responsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === "POST" && url.pathname === "/api/account-operations/upload-new";
  });
  await page.getByTestId("account-upload-new-submit").click();
  const response = await responsePromise;
  const responseText = await response.text();
  if (![200, 202].includes(response.status())) {
    throw new Error(`upload request failed with status ${response.status()} and public response ${responseText}`);
  }
  const responseBody = JSON.parse(responseText) as { operation?: { command_id?: string; operation_kind?: string; execution_state?: string } };
  const operation = responseBody.operation;
  expect(operation?.command_id).toMatch(/^[0-9a-f-]{36}$/u);
  expect(operation?.operation_kind).toBe("upload_new");
  expect(operation?.execution_state).toBe("remote_applied");

  await expect(page.getByTestId("account-operation-result")).toBeVisible();
  await expect(page.getByTestId("account-upload-new-file-button")).toBeVisible();
  expect(controlRequests).toEqual([{ method: "POST", path: "/api/account-operations/upload-new" }]);

  const pageHTML = await page.content();
  expect(pageHTML).not.toContain(secretMarker);
  const batchEmails = [`batch-enabled-${uploadEmail}`, `batch-disabled-${uploadEmail}`];
  const batchResponses: Array<Promise<{ operation: { command_id: string; execution_state: string; account_key: string } }>> = [];
  page.on("response", (response) => {
    if (response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/upload-new") batchResponses.push(response.json());
  });
  await uploadEntry.click();
  const batchChooser = page.waitForEvent("filechooser");
  await page.getByTestId("account-upload-new-file-button").click();
  await (await batchChooser).setFiles({ name: "synthetic-sub2api-export.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify({
    accounts: batchEmails.map((email, index) => ({ platform: "antigravity", type: "oauth", credentials: { email, refresh_token: secretMarker }, proxy_key: "synthetic", auto_pause_on_expired: true, ...(index === 1 ? { disabled: true } : {}) })),
    proxies: [{ proxy_key: "synthetic", protocol: "socks5h", host: "proxy.example.invalid", port: 1080, username: "synthetic", password: secretMarker }],
  })) });
  for (const email of batchEmails) await expect(page.getByTestId(`account-import-preview-${email}`)).toBeVisible();
  await page.getByTestId("account-upload-new-submit").click();
  for (const email of batchEmails) await expect(page.getByTestId(`account-import-result-${email}`).getByTestId("account-operation-result")).toBeVisible();
  const batch = await Promise.all(batchResponses);
  expect(batch).toHaveLength(2);
  expect(batch.map(({ operation }) => operation.account_key)).toEqual(batchEmails.map((email) => `antigravity:${email}`));
  for (const { operation } of batch) expect(operation.execution_state).toBe("remote_applied");
  expect(controlRequests).toHaveLength(3);
  expect(await page.content()).not.toContain(secretMarker);
  expect(page.url()).not.toContain(secretMarker);
  expect(await page.evaluate(() => JSON.stringify({ local: localStorage, session: sessionStorage, history: history.state }))).not.toContain(secretMarker);
  writeFileSync(evidenceFile, JSON.stringify({ command_id: operation?.command_id, email: uploadEmail, batch: batch.map(({ operation }, index) => ({ command_id: operation.command_id, email: batchEmails[index], disabled: index === 1 })) }), { mode: 0o600 });
});

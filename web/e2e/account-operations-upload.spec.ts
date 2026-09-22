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

  const nodeSelector = page.getByTestId("relay-node-selector");
  await expect(nodeSelector).toBeVisible();
  await nodeSelector.click();
  await nodeSelector.press("ArrowDown");
  await nodeSelector.press("Enter");

  const uploadEntry = page.getByTestId("account-upload-new");
  await expect(uploadEntry).toBeVisible();
  await uploadEntry.click();

  await page.getByTestId("account-upload-new-email").fill(uploadEmail);
  const fileChooserPromise = page.waitForEvent("filechooser");
  await page.getByTestId("account-upload-new-file-button").click();
  await (await fileChooserPromise).setFiles(credentialFile);

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
  writeFileSync(evidenceFile, JSON.stringify({ command_id: operation?.command_id, email: uploadEmail }), { mode: 0o600 });
});

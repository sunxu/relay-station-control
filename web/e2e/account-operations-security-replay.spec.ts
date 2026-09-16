import { expect, test, type Page } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";

const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const disableEmail = process.env.ACCEPTANCE_DISABLE_EMAIL;
const enableEmail = process.env.ACCEPTANCE_ENABLE_EMAIL;
const uploadEmail = process.env.ACCEPTANCE_UPLOAD_EMAIL;
const uploadCredential = process.env.ACCEPTANCE_UPLOAD_CREDENTIAL_FILE;
const evidenceFile = process.env.ACCEPTANCE_SECURITY_REPLAY_EVIDENCE_FILE;
const nodeID = "00000000-0000-4000-8000-000000000047";
const accountBody = (commandID: string, email: string) => JSON.stringify({ command_id: commandID, node_instance_id: nodeID, account_key: `antigravity:${email}` });

if (!storageState || !disableEmail || !enableEmail || !uploadEmail || !uploadCredential || !evidenceFile) {
  throw new Error("Security/replay acceptance environment is incomplete");
}

test.use({ storageState });
test.setTimeout(180_000);

type Evidence = {
  security: Record<string, unknown>;
  duplicate: Record<string, unknown>;
  normalReplay: Record<string, unknown>;
  credentialReplay: Record<string, unknown>;
};

const evidence: Evidence = { security: {}, duplicate: {}, normalReplay: {}, credentialReplay: {} };

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

async function sessionCSRF(page: Page): Promise<string> {
  const body = await page.evaluate(async () => await (await fetch("/api/auth/session", { credentials: "same-origin", cache: "no-store" })).json() as { csrf_token?: string });
  expect(body.csrf_token).toEqual(expect.any(String));
  return String(body.csrf_token);
}

async function rawJSON(page: Page, path: string, body: string, headers: Record<string, string> = {}): Promise<{ status: number; body: Record<string, unknown> }> {
  return await page.evaluate(async ({ path, body, headers }) => {
    const response = await fetch(path, { method: "POST", credentials: "same-origin", cache: "no-store", headers: { "Content-Type": "application/json", ...headers }, body });
    return { status: response.status, body: await response.json() as Record<string, unknown> };
  }, { path, body, headers });
}

async function rawMultipart(page: Page, path: string, requestBody: string, credentialPath: string, csrf: string): Promise<{ status: number; body: Record<string, unknown> }> {
  const credential = readFileSync(credentialPath).toString("base64");
  return await page.evaluate(async ({ path, requestBody, credential, csrf }) => {
    const bytes = Uint8Array.from(atob(credential), (character) => character.charCodeAt(0));
    const form = new FormData();
    form.append("request", requestBody);
    form.append("credential", new Blob([bytes], { type: "application/json" }), "upload-credential.json");
    const response = await fetch(path, { method: "POST", credentials: "same-origin", cache: "no-store", headers: { "X-CSRF-Token": csrf }, body: form });
    return { status: response.status, body: await response.json() as Record<string, unknown> };
  }, { path, requestBody, credential, csrf });
}

test("covers browser security boundary without accepting a mutation", async ({ page, browser, baseURL }) => {
  const body = accountBody(randomUUID(), disableEmail);
  const unauthenticatedContext = await browser.newContext({ baseURL, ignoreHTTPSErrors: true });
  const unauthenticatedPage = await unauthenticatedContext.newPage();
  await unauthenticatedPage.goto("/");
  const unauthenticated = await rawJSON(unauthenticatedPage, "/api/account-operations/disable", body, { "X-CSRF-Token": "invalid" });
  console.log(`SECURITY_STATUS unauthenticated=${unauthenticated.status}`);
  expect(unauthenticated.status).toBeGreaterThanOrEqual(400);
  expect(unauthenticated.status).toBeLessThan(500);
  const unauthorizedContext = await browser.newContext({ baseURL, ignoreHTTPSErrors: true });
  await unauthorizedContext.addCookies([{ name: "__Host-relay_control_session", value: "invalid-session", url: unauthenticatedPage.url(), secure: true, httpOnly: true }]);
  const unauthorizedPage = await unauthorizedContext.newPage();
  await unauthorizedPage.goto("/");
  const unauthorized = await rawJSON(unauthorizedPage, "/api/account-operations/disable", body, { "X-CSRF-Token": "invalid" });
  console.log(`SECURITY_STATUS unauthorized=${unauthorized.status}`);
  expect(unauthorized.status).toBeGreaterThanOrEqual(400);
  expect(unauthorized.status).toBeLessThan(500);
  await page.goto("/");
  const missingCSRF = await rawJSON(page, "/api/account-operations/disable", body);
  console.log(`SECURITY_STATUS missing_csrf=${missingCSRF.status}`);
  expect(missingCSRF.status).toBe(403);
  evidence.security = { unauthenticated: unauthenticated.status, unauthorized: unauthorized.status, missing_csrf: missingCSRF.status };
  await unauthenticatedContext.close();
  await unauthorizedContext.close();
});

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

test("replays an exact terminal normal mutation in the browser context", async ({ page }) => {
  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);
  await openAccount(page, enableEmail);
  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/disable");
  await page.getByTestId("account-disable").click();
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; execution_state?: string } };
  expect(response.status()).toBe(200);
  expect(body.operation).toMatchObject({ execution_state: "remote_applied" });
  await expect(page.getByTestId("account-operation-result")).toContainText("已应用");
  const firstRequest = response.request().postDataJSON() as { node_instance_id: string; account_key: string };
  const replay = await rawJSON(page, "/api/account-operations/disable", JSON.stringify({ ...firstRequest, command_id: body.operation?.command_id }), { "X-CSRF-Token": await sessionCSRF(page) });
  expect(replay.status).toBe(200);
  expect(replay.body).toEqual(body);
  evidence.normalReplay = { command_id: body.operation?.command_id, first_status: response.status(), replay_status: replay.status, result_equal: JSON.stringify(replay.body) === JSON.stringify(body) };
});

test("replays an exact terminal credential mutation in the browser context", async ({ page }) => {
  await page.goto("/topology");
  await expect(page.getByTestId("topology-page")).toBeVisible();
  await selectNode(page);
  await page.getByTestId("account-upload-new").click();
  await page.getByTestId("account-upload-new-email").fill(uploadEmail);
  const fileChooser = page.waitForEvent("filechooser");
  await page.getByTestId("account-upload-new-file-button").click();
  await (await fileChooser).setFiles(uploadCredential);
  const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/account-operations/upload-new");
  await page.getByTestId("account-upload-new-submit").click();
  const response = await responsePromise;
  const body = await response.json() as { operation?: { command_id?: string; execution_state?: string } };
  expect(response.status()).toBe(200);
  expect(body.operation).toMatchObject({ execution_state: "remote_applied" });
  await expect(page.getByTestId("account-operation-result")).toContainText("已应用");
  const replay = await rawMultipart(page, "/api/account-operations/upload-new", JSON.stringify({ command_id: body.operation?.command_id, node_instance_id: nodeID, account_key: `antigravity:${uploadEmail}` }), uploadCredential, await sessionCSRF(page));
  expect(replay.status).toBe(200);
  expect(replay.body).toEqual(body);
  evidence.credentialReplay = { command_id: body.operation?.command_id, first_status: response.status(), replay_status: replay.status, result_equal: JSON.stringify(replay.body) === JSON.stringify(body) };
  expect(await page.content()).not.toContain("PHASE7_E2E_SECRET_");
});

test.afterAll(() => writeFileSync(evidenceFile, JSON.stringify(evidence), { mode: 0o600 }));

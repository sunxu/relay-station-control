import { createHmac } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { expect, test, type BrowserContext, type Page, type APIResponse } from "@playwright/test";

const bootstrapLogin = "e2e.primary";
const secondLogin = "e2e.secondary";

function requiredFile(name: string): string {
  const file = process.env[name];
  if (!file) throw new Error(`${name} must point to an external runtime file`);
  return readFileSync(file, "utf8").trim();
}

function decodeBase32(value: string): Buffer {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const character of value.toUpperCase().replace(/=+$/u, "")) {
    const index = alphabet.indexOf(character);
    if (index < 0) throw new Error("invalid TOTP enrollment secret");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes: number[] = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8) {
    bytes.push(Number.parseInt(bits.slice(offset, offset + 8), 2));
  }
  return Buffer.from(bytes);
}

function totp(uri: string, now = Date.now()): string {
  const parsed = new URL(uri);
  const encodedSecret = parsed.searchParams.get("secret");
  if (!encodedSecret) throw new Error("TOTP enrollment did not contain a secret");
  const period = Number(parsed.searchParams.get("period") ?? "30");
  const counter = Math.floor(now / 1000 / period);
  const buffer = Buffer.alloc(8);
  buffer.writeBigUInt64BE(BigInt(counter));
  const digest = createHmac("sha1", decodeBase32(encodedSecret)).update(buffer).digest();
  const offset = digest[digest.length - 1] & 0x0f;
  const binary = ((digest[offset] & 0x7f) << 24)
    | ((digest[offset + 1] & 0xff) << 16)
    | ((digest[offset + 2] & 0xff) << 8)
    | (digest[offset + 3] & 0xff);
  return String(binary % 1_000_000).padStart(6, "0");
}

async function freshTotp(uri: string, previous: string): Promise<string> {
  const deadline = Date.now() + 35_000;
  while (Date.now() < deadline) {
    const current = totp(uri);
    if (current !== previous) return current;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error("TOTP step did not advance within one period");
}

async function json(response: APIResponse): Promise<Record<string, unknown>> {
  expect(response.headers()["cache-control"]).toBe("no-store");
  return await response.json() as Record<string, unknown>;
}

async function leaveOneTimePage(page: Page): Promise<void> {
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "确认并离开" }).click();
  await expect(page.getByTestId("management-page")).toBeVisible();
}

async function logout(page: Page): Promise<void> {
  const button = page.getByRole("button", { name: /注\s*销/u });
  if (await button.isVisible()) await button.click();
  await expect(page.getByTestId("login-page")).toBeVisible();
}

async function passwordLogin(page: Page, login: string, password: string): Promise<void> {
  await page.getByLabel("登录名").fill(login);
  await page.getByLabel("密码").fill(password);
  await page.locator("form button[type=submit]").click();
  await expect(page.getByText("需要第二步验证")).toBeVisible();
}

async function sameOriginJSON(
  page: Page,
  path: string,
  method: "GET" | "POST",
  body?: Record<string, unknown>,
  csrf?: string,
): Promise<{ status: number; cacheControl: string | null; body: Record<string, unknown> }> {
  return await page.evaluate(async ({ path: target, method: verb, body: payload, csrf: proof }) => {
    const headers: Record<string, string> = {};
    if (payload) headers["Content-Type"] = "application/json";
    if (proof) headers["X-CSRF-Token"] = proof;
    const response = await fetch(target, {
      method: verb,
      credentials: "same-origin",
      cache: "no-store",
      headers,
      body: payload ? JSON.stringify(payload) : undefined,
    });
    return {
      status: response.status,
      cacheControl: response.headers.get("cache-control"),
      body: await response.json() as Record<string, unknown>,
    };
  }, { path, method, body, csrf });
}

test("real administrator lifecycle survives restart and keeps one-time material out of reports", async ({ browser, baseURL }) => {
  const bootstrapSecret = requiredFile("CONTROL_E2E_BOOTSTRAP_SECRET_FILE");
  const primaryPassword = requiredFile("CONTROL_E2E_ADMIN_PASSWORD_FILE");
  const secondPassword = requiredFile("CONTROL_E2E_SECOND_ADMIN_PASSWORD_FILE");
  const container = process.env.CONTROL_E2E_CONTAINER;
  if (!container) throw new Error("CONTROL_E2E_CONTAINER is required for the restart assertion");

  const primaryContext: BrowserContext = await browser.newContext({ baseURL });
  const primary = await primaryContext.newPage();
  await primary.goto("/");
  await expect(primary.getByTestId("bootstrap-page")).toBeVisible();

  await primary.getByTestId("bootstrap-secret").fill(bootstrapSecret);
  await primary.getByLabel("登录名").fill(bootstrapLogin);
  await primary.getByLabel("实名显示名").fill("E2E Primary Operator");
  await primary.getByLabel("密码").fill(primaryPassword);
  const bootstrapStartPromise = primary.waitForResponse((response) => response.url().endsWith("/api/bootstrap/start") && response.request().method() === "POST");
  await primary.getByRole("button", { name: "开始或继续初始化" }).click();
  const bootstrapStart = await json(await bootstrapStartPromise);
  const bootstrapEnrollment = bootstrapStart.totp_enrollment as Record<string, unknown>;
  const primaryTotpURI = String(bootstrapEnrollment.otpauth_uri);
  expect(primaryTotpURI.startsWith("otpauth://totp/")).toBe(true);

  const bootstrapTotp = totp(primaryTotpURI);
  await primary.getByLabel("TOTP 验证码").fill(bootstrapTotp);
  const bootstrapCompletePromise = primary.waitForResponse((response) => response.url().endsWith("/api/bootstrap/complete"));
  await primary.getByRole("button", { name: "确认并永久完成" }).click();
  const bootstrapCompleteResponse = await bootstrapCompletePromise;
  expect(bootstrapCompleteResponse.headers()["cache-control"]).toBe("no-store");
  await expect(primary.getByTestId("one-time-page")).toBeVisible();
  const recoveryCodes = await primary.locator(".secret-list code").allTextContents();
  expect(recoveryCodes.length).toBe(10);
  expect(recoveryCodes.every((code) => code.length >= 8)).toBe(true);
  await leaveOneTimePage(primary);
  console.log("[e2e] bootstrap completed");

  execFileSync("docker", ["restart", container], { stdio: "ignore", timeout: 60_000 });
  await expect.poll(async () => {
    try {
      return (await primary.request.get("/api/healthz")).status();
    } catch {
      return 0;
    }
  }, { timeout: 60_000 }).toBe(200);
  await primary.reload();
  await expect(primary.getByTestId("management-page")).toBeVisible();
  const persistedBootstrap = await primary.request.get("/api/bootstrap/status");
  const persistedBootstrapBody = await json(persistedBootstrap);
  expect(persistedBootstrapBody.status).toBe("completed");
  console.log("[e2e] restart preserved completed bootstrap state");
  await primaryContext.clearCookies();
  await primary.reload();
  await expect(primary.getByTestId("login-page")).toBeVisible();

  await passwordLogin(primary, bootstrapLogin, primaryPassword);
  await primary.getByLabel("TOTP 验证码").fill(await freshTotp(primaryTotpURI, bootstrapTotp));
  await primary.getByRole("button", { name: "验证并登录" }).click();
  await expect(primary.getByTestId("management-page")).toBeVisible();
  console.log("[e2e] TOTP login completed");
  await logout(primary);

  await passwordLogin(primary, bootstrapLogin, primaryPassword);
  await primary.getByText("恢复码", { exact: true }).click();
  await primary.getByRole("textbox", { name: "恢复码" }).fill(recoveryCodes[0]);
  await primary.getByRole("button", { name: "验证并登录" }).click();
  await expect(primary.getByTestId("management-page")).toBeVisible();
  console.log("[e2e] recovery-code login completed");

  const browserAssetRequests: Array<{ method: string; url: string }> = [];
  const externalAssetRequests: string[] = [];
  const assetPaths = new Set([
    "/api/environment",
    "/api/assets/gateway",
    "/api/assets/nodes",
    "/api/assets/drivers",
    "/api/assets/provider-policies/current",
  ]);
  const assetRoute = (url: URL) => assetPaths.has(url.pathname);
  primary.on("request", (request) => {
    const requestedURL = new URL(request.url());
    if (assetPaths.has(requestedURL.pathname)) browserAssetRequests.push({ method: request.method(), url: request.url() });
    if (requestedURL.hostname === "gateway.invalid" || requestedURL.hostname === "node.invalid") externalAssetRequests.push(request.url());
  });
  await primary.route(assetRoute, async (route) => {
    const path = new URL(route.request().url()).pathname;
    const fixtures: Record<string, object> = {
      "/api/environment": { environment_id: "development", environment_type: "production", name: "E2E Phase 1" },
      "/api/assets/gateway": {
        status: "registered",
        gateway: {
          instance_id: "00000000-0000-4000-8000-000000000001",
          display_name: "E2E Gateway",
          management_endpoint: "https://gateway.invalid:8443",
          secret_configured: true,
          reader_secret_ref: "vault://test-only/CANARY-GATEWAY-REF",
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
        },
      },
      "/api/assets/nodes": {
        items: [{
          instance_id: "00000000-0000-4000-8000-000000000101",
          display_name: "E2E Node",
          node_type: "cliproxyapi",
          driver_contract_version: "v1",
          management_endpoint: "https://node.invalid:8317",
          secret_configured: true,
          reader_secret_ref: "vault://test-only/CANARY-NODE-REF",
          capabilities: ["management_health_read"],
          monitoring: { active: true, effective_from: "2026-08-25T00:00:00Z", effective_to: null },
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
        }],
        next_cursor: null,
      },
      "/api/assets/drivers": {
        items: [{
          node_type: "cliproxyapi",
          driver_contract_version: "v1",
          display_name: "E2E Driver",
          lifecycle_status: "active",
          capabilities: ["management_health_read"],
          created_at: "2026-08-25T00:00:00Z",
        }],
      },
      "/api/assets/provider-policies/current": {
        status: "not_configured",
        node_type: "cliproxyapi",
        driver_contract_version: "v1",
      },
    };
    await route.fulfill({
      status: 200,
      headers: { "Cache-Control": "no-store", "Content-Type": "application/json" },
      body: JSON.stringify(fixtures[path]),
    });
  });

  await primary.getByRole("button", { name: "资产注册表" }).click();
  await expect(primary.getByTestId("assets-page")).toBeVisible();
  await expect(primary.getByText("E2E Gateway")).toBeVisible();
  await expect(primary.getByText("E2E Node")).toBeVisible();
  await expect(primary.getByText("尚未配置当前 Provider 策略")).toBeVisible();
  await expect.poll(() => browserAssetRequests.length).toBe(5);
  const controlOrigin = new URL(baseURL!).origin;
  expect(browserAssetRequests.every((request) => request.method === "GET" && new URL(request.url).origin === controlOrigin)).toBe(true);
  expect(await primary.locator('a[href^="https://gateway.invalid"], a[href^="https://node.invalid"]').count()).toBe(0);
  expect(await primary.getByText(/CANARY-(GATEWAY|NODE)-REF/).count()).toBe(0);
  expect(externalAssetRequests).toEqual([]);
  await primary.getByRole("button", { name: "管理员控制台" }).click();
  await expect(primary.getByTestId("management-page")).toBeVisible();
  await primary.unrouteAll({ behavior: "wait" });
  console.log("[e2e] assets page stayed on same-origin read APIs and omitted Secret references");

  let current = await sameOriginJSON(primary, "/api/auth/session", "GET");
  expect(current.status).toBe(200);
  expect(current.cacheControl).toBe("no-store");
  let csrf = String(current.body.csrf_token);
  const reauthenticated = await sameOriginJSON(primary, "/api/auth/reauthenticate", "POST", {
    password: primaryPassword,
    mfa_method: "recovery_code",
    mfa_code: recoveryCodes[1],
  }, csrf);
  expect(reauthenticated.status).toBe(200);
  csrf = String(reauthenticated.body.csrf_token);

  const created = await sameOriginJSON(primary, "/api/admins", "POST", {
    login_name: secondLogin,
    display_name: "E2E Secondary Operator",
    reason: "E2E dual administrator recovery coverage",
  }, csrf);
  expect(created.status).toBe(201);
  expect(created.cacheControl).toBe("no-store");
  const secondID = String((created.body.administrator as Record<string, unknown>).id);
  const activationToken = String(created.body.activation_token);
  expect(secondID.length > 20).toBe(true);
  expect(activationToken.length >= 32).toBe(true);
  console.log("[e2e] second administrator created");

  const secondaryContext = await browser.newContext({ baseURL });
  const secondary = await secondaryContext.newPage();
  await secondary.goto("/");
  await secondary.getByRole("button", { name: "使用激活令牌设置新账号" }).click();
  await secondary.getByTestId("activation-token").fill(activationToken);
  const activationStartPromise = secondary.waitForResponse((response) => response.url().endsWith("/api/admin-activations/complete"));
  await secondary.getByRole("button", { name: "开始激活" }).click();
  const activationStart = await json(await activationStartPromise);
  const secondEnrollment = activationStart.totp_enrollment as Record<string, unknown>;
  const secondTotpURI = String(secondEnrollment.otpauth_uri);
  expect(secondTotpURI.startsWith("otpauth://totp/")).toBe(true);

  const activated = await sameOriginJSON(secondary, "/api/admin-activations/complete", "POST", {
    stage: "complete",
    activation_token: activationToken,
    password: secondPassword,
    totp_code: totp(secondTotpURI),
  });
  expect(activated.status).toBe(200);
  expect(activated.cacheControl).toBe("no-store");
  expect(Array.isArray(activated.body.recovery_codes)).toBe(true);
  expect((activated.body.recovery_codes as unknown[]).length).toBe(10);
  await secondary.reload();
  await expect(secondary.getByTestId("management-page")).toBeVisible();
  console.log("[e2e] second administrator activated");

  current = await sameOriginJSON(primary, "/api/auth/session", "GET");
  expect(current.status).toBe(200);
  csrf = String(current.body.csrf_token);
  const disabled = await sameOriginJSON(primary, `/api/admins/${secondID}/disable`, "POST", {
    reason: "E2E administrator disable and session revocation coverage",
  }, csrf);
  expect(disabled.status).toBe(200);
  expect((disabled.body as Record<string, unknown>).status).toBe("disabled");
  console.log("[e2e] second administrator disabled");

  await secondary.reload();
  await expect(secondary.getByTestId("login-page")).toBeVisible();
  await secondary.getByLabel("登录名").fill(secondLogin);
  await secondary.getByLabel("密码").fill(secondPassword);
  const disabledLoginPromise = secondary.waitForResponse((response) => response.url().endsWith("/api/auth/login"));
  await secondary.locator("form button[type=submit]").click();
  const disabledLogin = await disabledLoginPromise;
  expect(disabledLogin.status()).toBe(401);
  expect(disabledLogin.headers()["cache-control"]).toBe("no-store");
  await expect(secondary.getByText("认证失败，请检查输入后重试。")).toBeVisible();

  await secondaryContext.close();
  await primaryContext.close();
});

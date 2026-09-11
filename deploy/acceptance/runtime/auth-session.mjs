import { createHmac } from "node:crypto";
import { chmodSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { chromium } from "../../../web/node_modules/@playwright/test/index.mjs";

const runtime = process.env.ACCEPTANCE_RUNTIME_DIR;
const baseURL = process.env.ACCEPTANCE_BASE_URL;
const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
if (!runtime || !baseURL || !storageState || !runtime.startsWith("/") || runtime.includes(process.cwd())) throw new Error("explicit repo-external runtime and storage paths are required");
const read = (name) => readFileSync(`${runtime}/${name}`, "utf8").trim();
function decodeBase32(value) { const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"; let bits = ""; for (const c of value.toUpperCase().replace(/=+$/u, "")) { const i = alphabet.indexOf(c); if (i < 0) throw new Error("invalid TOTP secret"); bits += i.toString(2).padStart(5, "0"); } const bytes = []; for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(Number.parseInt(bits.slice(i, i + 8), 2)); return Buffer.from(bytes); }
function totp(uri) { const parsed = new URL(uri); const secret = parsed.searchParams.get("secret"); if (!secret) throw new Error("TOTP enrollment missing secret"); const counter = Math.floor(Date.now() / 1000 / Number(parsed.searchParams.get("period") ?? "30")); const b = Buffer.alloc(8); b.writeBigUInt64BE(BigInt(counter)); const d = createHmac("sha1", decodeBase32(secret)).update(b).digest(); const o = d[d.length - 1] & 15; return String((((d[o] & 127) << 24) | ((d[o + 1] & 255) << 16) | ((d[o + 2] & 255) << 8) | (d[o + 3] & 255)) % 1000000).padStart(6, "0"); }
const launchOptions = process.env.CONTROL_E2E_EXECUTABLE_PATH
  ? { executablePath: process.env.CONTROL_E2E_EXECUTABLE_PATH }
  : { channel: process.env.CONTROL_E2E_BROWSER_CHANNEL ?? "chromium" };
const browser = await chromium.launch(launchOptions);
const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: true });
const page = await context.newPage();
await page.goto("/");
await page.getByTestId("bootstrap-page").waitFor();
await page.getByTestId("bootstrap-secret").fill(read("bootstrap-secret"));
await page.getByLabel("登录名").fill(process.env.ACCEPTANCE_LOGIN ?? "acceptance.operator");
await page.getByLabel("实名显示名").fill("Acceptance Operator");
await page.getByLabel("密码").fill(read("admin-password"));
const start = page.waitForResponse((r) => r.url().endsWith("/api/bootstrap/start") && r.request().method() === "POST");
await page.getByRole("button", { name: "开始或继续初始化" }).click();
const startBody = await (await start).json();
const uri = startBody.totp_enrollment?.otpauth_uri;
if (typeof uri !== "string") throw new Error("TOTP enrollment unavailable");
await page.getByLabel("TOTP 验证码").fill(totp(uri));
await page.getByRole("button", { name: "确认并永久完成" }).click();
await page.getByTestId("one-time-page").waitFor();
await page.getByRole("checkbox").check();
await page.getByRole("button", { name: "确认并离开" }).click();
await page.getByTestId("management-page").waitFor();
await context.storageState({ path: storageState });
chmodSync(storageState, 0o600);
console.log("AUTH_SESSION_READY=YES\nSTORAGE_STATE_SAVED=YES");
await browser.close();

if (process.env.ACCEPTANCE_CONTROL_CONTAINER) {
  execFileSync("docker", ["restart", process.env.ACCEPTANCE_CONTROL_CONTAINER], { stdio: "ignore", timeout: 60_000 });
  const restored = await chromium.launch(launchOptions);
  const restoredContext = await restored.newContext({ baseURL, ignoreHTTPSErrors: true, storageState });
  const restoredPage = await restoredContext.newPage();
  await restoredPage.goto("/");
  await restoredPage.getByTestId("management-page").waitFor();
  const session = await restoredPage.request.get("/api/auth/session");
  if (session.status() !== 200) throw new Error("restored authenticated session failed");
  const sessionBody = await session.json();
  if (sessionBody.state !== "authenticated") throw new Error("restored session is not authenticated");
  console.log("SESSION_RESTORE_AFTER_RESTART=PASS");
  await restored.close();
}

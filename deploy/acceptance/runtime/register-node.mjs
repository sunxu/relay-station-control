import { chromium } from "../../../web/node_modules/@playwright/test/index.mjs";
import { randomUUID } from "node:crypto";

const baseURL = process.env.ACCEPTANCE_BASE_URL;
const storageState = process.env.ACCEPTANCE_STORAGE_STATE;
const instanceID = process.env.ACCEPTANCE_NODE_INSTANCE_ID;
const managementCredential = process.env.ACCEPTANCE_NODE_MANAGEMENT_PASSWORD;
const managementEndpoint = process.env.ACCEPTANCE_NODE_ENDPOINT;
if (![baseURL, storageState, instanceID, managementCredential, managementEndpoint].every(Boolean)) {
  throw new Error("acceptance node registration inputs are incomplete");
}

const launchOptions = process.env.CONTROL_E2E_EXECUTABLE_PATH
  ? { executablePath: process.env.CONTROL_E2E_EXECUTABLE_PATH }
  : { channel: process.env.CONTROL_E2E_BROWSER_CHANNEL ?? "chromium" };
const browser = await chromium.launch(launchOptions);
try {
  const context = await browser.newContext({ baseURL, ignoreHTTPSErrors: true, storageState });
  const session = await context.request.get("/api/auth/session");
  if (session.status() !== 200) throw new Error(`authenticated session unavailable: ${session.status()}`);
  const sessionBody = await session.json();
  const csrf = sessionBody.csrf_token;
  if (typeof csrf !== "string" || csrf.length === 0) throw new Error("authenticated session has no CSRF token");
  const headers = {
    "Content-Type": "application/json",
    "Origin": new URL(baseURL).origin,
    "X-CSRF-Token": csrf,
  };
  const registration = await context.request.post("/api/assets/nodes", {
    headers,
    data: {
      command_id: randomUUID(),
      new_instance_id: instanceID,
      display_name: "Acceptance Node",
      management_endpoint: managementEndpoint,
      node_type: "cliproxyapi",
      driver_contract_version: "cliproxyapi.auth-files.v1",
      capabilities: ["management_health_read", "management_account_inventory_read"],
      management_credential: managementCredential,
    },
  });
  if (registration.status() !== 201) throw new Error(`node registration failed: ${registration.status()}`);
  const monitoring = await context.request.post(`/api/assets/nodes/${instanceID}/monitoring-enable`, {
    headers,
    data: { command_id: randomUUID(), expected_revision: "1" },
  });
  if (monitoring.status() !== 200) throw new Error(`node monitoring enable failed: ${monitoring.status()}`);
  console.log("NODE_REGISTRATION=PASS");
  console.log("NODE_MONITORING_ENABLE=PASS");
} finally {
  await browser.close();
}

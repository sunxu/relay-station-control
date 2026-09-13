import { afterEach, describe, expect, it, vi } from "vitest";
import { generatedAssetApi } from "./asset-api";
import { AssetApiError } from "./asset-types";

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
});

afterEach(() => vi.unstubAllGlobals());

describe("generated asset client adapter", () => {
  it("uses only same-origin read paths and strips fields outside the generated read model", async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      requests.push({ url, init });
      if (url === "/api/environment") return json({ environment_id: "development", environment_type: "production", name: "Phase 1" });
      if (url === "/api/assets/gateway") return json({
        status: "registered",
        gateway: {
          instance_id: "00000000-0000-4000-8000-000000000001",
          display_name: "Gateway",
          management_endpoint: "https://gateway.invalid:8443",
          secret_configured: true,
          reader_secret_ref: "vault://CANARY-GATEWAY-SECRET",
          created_at: "2026-08-25T00:00:00Z",
          updated_at: "2026-08-25T00:00:00Z",
        },
      });
      if (url.startsWith("/api/assets/nodes?")) return json({ items: [], next_cursor: null });
      if (url === "/api/assets/nodes/00000000-0000-4000-8000-000000000101") return json({ asset: {
        instance_id: "00000000-0000-4000-8000-000000000101",
        display_name: "Node",
        node_type: "cliproxyapi",
        driver_contract_version: "v1",
        management_endpoint: "https://node.invalid:8317",
        secret_configured: true,
        capabilities: ["management_health_read"],
		lifecycle_status: "active",
		revision: "1",
		retired_at: null,
		retired_by: null,
		retire_reason: null,
		monitoring: { current: false, monitoring_active: false, effective_from: null, effective_to: null },
        created_at: "2026-08-25T00:00:00Z",
        updated_at: "2026-08-25T00:00:00Z",
      }, predecessor: null, successor: null });
      if (url === "/api/assets/drivers") return json({ items: [] });
      if (url.startsWith("/api/assets/provider-policies/current?")) return json({
        status: "not_configured",
        node_type: "cliproxyapi",
        driver_contract_version: "v1",
      });
      throw new Error(`unexpected URL: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    await generatedAssetApi.environment();
    const gateway = await generatedAssetApi.gateway();
    await generatedAssetApi.nodes({ nodeType: "cliproxyapi", capability: "management_health_read", monitoringActive: false, limit: 50 });
    await generatedAssetApi.node("00000000-0000-4000-8000-000000000101");
    await generatedAssetApi.drivers();
    await generatedAssetApi.currentProviderPolicy({ nodeType: "cliproxyapi", driverContractVersion: "v1" });

    expect(requests).toHaveLength(6);
    expect(requests.every(({ url }) => url.startsWith("/api/"))).toBe(true);
    expect(requests.every(({ init }) => init?.method === "GET" && init.cache === "no-store" && init.credentials === "same-origin")).toBe(true);
    expect(requests[2]?.url).toContain("monitoring_active=false");
    expect(JSON.stringify(gateway)).not.toContain("CANARY-GATEWAY-SECRET");
    expect(JSON.stringify(requests)).not.toContain("gateway.invalid");
    expect(JSON.stringify(requests)).not.toContain("node.invalid");
  });

  it("maps an error envelope to status only", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({
      code: "internal_error",
      message: "postgres://user:password@db/vault/CANARY",
      request_id: "request-canary",
    }, 503)));
    await expect(generatedAssetApi.gateway()).rejects.toEqual(expect.objectContaining({ name: "AssetApiError", status: 503 }));
    await expect(generatedAssetApi.gateway()).rejects.not.toHaveProperty("detail");
    expect(new AssetApiError(503).message).not.toContain("CANARY");
  });
});

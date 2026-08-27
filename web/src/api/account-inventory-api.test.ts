import { beforeEach, describe, expect, it, vi } from "vitest";
import { queryAccountInventory } from "./generated/control";
import { generatedAccountInventoryApi } from "./account-inventory-api";

vi.mock("./generated/control", async (importOriginal) => {
  const original = await importOriginal<typeof import("./generated/control")>();
  return { ...original, queryAccountInventory: vi.fn() };
});

const instanceId = "00000000-0000-4000-8000-000000000101";

beforeEach(() => vi.mocked(queryAccountInventory).mockReset());

describe("account inventory generated client adapter", () => {
  it("sends filters and the encrypted cursor only in the POST body with no-store and CSRF", async () => {
    vi.mocked(queryAccountInventory).mockResolvedValue({
      status: 200,
      headers: new Headers(),
      data: {
        items: [{
          instance_id: instanceId,
          provider: "openai",
          email: "operator@example.invalid",
          basic_status: "reported_active",
          lifecycle: "present",
          consecutive_missing_count: 0,
          first_seen_at: "2026-08-27T00:00:00Z",
          last_seen_at: "2026-08-27T01:00:00Z",
          missing_since: null,
          out_of_scope_since: null,
          last_refresh_at: null,
          next_retry_at: null,
          source_updated_at: null,
          provider_last_complete_at: "2026-08-27T01:00:00Z",
          provider_degraded: false,
          snapshot_freshness: "fresh",
        }],
        next_cursor: "opaque-ciphertext",
      },
    });

    const page = await generatedAccountInventoryApi.query("csrf-proof", {
      instanceId,
      provider: "openai",
      lifecycle: "present",
      basicStatus: "reported_active",
      email: "operator@example.invalid",
      cursor: "opaque-ciphertext",
      limit: 25,
    });

    expect(queryAccountInventory).toHaveBeenCalledWith({
      instance_id: instanceId,
      provider: "openai",
      lifecycle: "present",
      basic_status: "reported_active",
      email: "operator@example.invalid",
      cursor: "opaque-ciphertext",
      limit: 25,
    }, expect.objectContaining({
      cache: "no-store",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": "csrf-proof" },
    }));
    expect(page.items[0]).toMatchObject({ email: "operator@example.invalid", snapshotFreshness: "fresh" });
    expect(page.nextCursor).toBe("opaque-ciphertext");
  });

  it("maps a structured failure without exposing the request body", async () => {
    vi.mocked(queryAccountInventory).mockResolvedValue({
      status: 409,
      headers: new Headers(),
      data: { code: "account_inventory_unsupported", message: "fixed", request_id: "request-fixed" },
    } as unknown as Awaited<ReturnType<typeof queryAccountInventory>>);

    await expect(generatedAccountInventoryApi.query("csrf-proof", { instanceId, email: "canary@example.invalid" }))
      .rejects.toMatchObject({ status: 409, detail: { code: "account_inventory_unsupported", message: "fixed", request_id: "request-fixed" } });
  });
});

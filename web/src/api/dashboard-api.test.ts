import { beforeEach, describe, expect, it, vi } from "vitest";

const generated = vi.hoisted(() => ({
  getHealthz: vi.fn(),
  listGatewayAssets: vi.fn(),
  listNodeAssets: vi.fn(),
  getAccountInventoryPollCapacity: vi.fn(),
}));

vi.mock("./generated/control", () => generated);

import { generatedDashboardApi } from "./dashboard-api";

describe("dashboard API adapter", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps health and authoritative asset counts without using page length", async () => {
    generated.getHealthz.mockResolvedValue({ status: 200, data: { status: "ok", version: "0.9.3" } });
    generated.listGatewayAssets.mockResolvedValue({ status: 200, data: { items: [{ instance_id: "only-page-item" }], gateway_counts: { active: 30, retired: 7, total: 37 } } });
    generated.listNodeAssets.mockResolvedValue({ status: 200, data: { items: [{ instance_id: "only-page-item" }], node_counts: { active: 15, retired: 4, total: 19 } } });
    generated.getAccountInventoryPollCapacity.mockResolvedValue({ status: 200, data: { status: "ready", enabled: true, eligible_node_count: 4, effective_capacity: 12, evaluated_slot: "slot-1", evaluated_at: "2026-09-21T00:00:00Z" } });

    await expect(generatedDashboardApi.health()).resolves.toEqual({ status: "ok", version: "0.9.3" });
    await expect(generatedDashboardApi.gatewayCounts()).resolves.toEqual({ active: 30, retired: 7, total: 37 });
    await expect(generatedDashboardApi.nodeCounts()).resolves.toEqual({ active: 15, retired: 4, total: 19 });
    await expect(generatedDashboardApi.pollCapacity()).resolves.toMatchObject({ status: "ready", eligibleNodeCount: 4, effectiveCapacity: 12 });
    expect(generated.listGatewayAssets).toHaveBeenCalledWith({ lifecycle: "all", limit: 1 }, expect.objectContaining({ cache: "no-store", credentials: "same-origin" }));
    expect(generated.listNodeAssets).toHaveBeenCalledWith({ lifecycle: "all", limit: 1 }, expect.anything());
  });

  it("preserves unauthorized status for the existing session boundary", async () => {
    generated.getHealthz.mockResolvedValue({ status: 401, data: {} });
    await expect(generatedDashboardApi.health()).rejects.toMatchObject({ status: 401 });
  });
});

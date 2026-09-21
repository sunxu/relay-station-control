import {
  getAccountInventoryPollCapacity,
  getHealthz,
  listGatewayAssets,
  listNodeAssets,
} from "./generated/control";
import type {
  AccountInventoryPollCapacityStatus,
  HealthResponseStatus,
} from "./generated/control";

export type DashboardCounts = { active: number; retired: number; total: number };
export type DashboardHealth = { status: HealthResponseStatus; version: string };
export type DashboardPollCapacity = {
  status: AccountInventoryPollCapacityStatus;
  enabled: boolean;
  eligibleNodeCount: number;
  effectiveCapacity: number;
  evaluatedSlot: string;
  evaluatedAt: string;
};

export class DashboardApiError extends Error {
  readonly status: number;

  constructor(status: number) {
    super("Dashboard read failed");
    this.name = "DashboardApiError";
    this.status = status;
  }
}

const requestOptions: RequestInit = { cache: "no-store", credentials: "same-origin" };

function unwrap<T>(response: { data: T | unknown; status: number }): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  throw new DashboardApiError(response.status);
}

export const generatedDashboardApi = {
  async health(): Promise<DashboardHealth> {
    const value = unwrap<{ status: HealthResponseStatus; version: string }>(await getHealthz(requestOptions));
    return { status: value.status, version: value.version };
  },

  async gatewayCounts(): Promise<DashboardCounts> {
    const value = unwrap<{ gateway_counts: DashboardCounts }>(await listGatewayAssets({ lifecycle: "all", limit: 1 }, requestOptions));
    return value.gateway_counts;
  },

  async nodeCounts(): Promise<DashboardCounts> {
    const value = unwrap<{ node_counts: DashboardCounts }>(await listNodeAssets({ lifecycle: "all", limit: 1 }, requestOptions));
    return value.node_counts;
  },

  async pollCapacity(): Promise<DashboardPollCapacity> {
    const value = unwrap<{
      status: AccountInventoryPollCapacityStatus;
      enabled: boolean;
      eligible_node_count: number;
      effective_capacity: number;
      evaluated_slot: string;
      evaluated_at: string;
    }>(await getAccountInventoryPollCapacity(requestOptions));
    return {
      status: value.status,
      enabled: value.enabled,
      eligibleNodeCount: value.eligible_node_count,
      effectiveCapacity: value.effective_capacity,
      evaluatedSlot: value.evaluated_slot,
      evaluatedAt: value.evaluated_at,
    };
  },
};

export type DashboardApi = typeof generatedDashboardApi;

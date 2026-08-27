import type { ErrorResponse } from "./generated/control";

export const accountInventoryLifecycles = ["present", "suspected_missing", "missing", "out_of_scope"] as const;
export type AccountInventoryLifecycle = typeof accountInventoryLifecycles[number];

export const accountInventoryBasicStatuses = ["reported_active", "disabled", "unavailable", "error", "unknown"] as const;
export type AccountInventoryBasicStatus = typeof accountInventoryBasicStatuses[number];

export type AccountInventorySnapshotFreshness = "fresh" | "stale" | "out_of_scope";

export interface AccountInventoryFilters {
  instanceId: string;
  provider?: string;
  lifecycle?: AccountInventoryLifecycle;
  basicStatus?: AccountInventoryBasicStatus;
  email?: string;
  cursor?: string;
  limit?: number;
}

export interface AccountInventoryItem {
  instanceId: string;
  provider: string;
  email: string;
  basicStatus: AccountInventoryBasicStatus;
  lifecycle: AccountInventoryLifecycle;
  consecutiveMissingCount: number;
  firstSeenAt: string;
  lastSeenAt: string;
  missingSince: string | null;
  outOfScopeSince: string | null;
  lastRefreshAt: string | null;
  nextRetryAt: string | null;
  sourceUpdatedAt: string | null;
  providerLastCompleteAt: string;
  providerDegraded: boolean;
  snapshotFreshness: AccountInventorySnapshotFreshness;
}

export interface AccountInventoryPage {
  items: AccountInventoryItem[];
  nextCursor: string | null;
}

export interface AccountInventoryApi {
  query(csrfToken: string, filters: AccountInventoryFilters): Promise<AccountInventoryPage>;
}

export class AccountInventoryApiError extends Error {
  readonly status: number;
  readonly detail: ErrorResponse;

  constructor(status: number, detail: ErrorResponse) {
    super("account inventory request failed");
    this.name = "AccountInventoryApiError";
    this.status = status;
    this.detail = detail;
  }
}

import { queryAccountInventory } from "./generated/control";
import type {
  AccountInventoryItem as GeneratedAccountInventoryItem,
  AccountInventoryQueryResponse,
  ErrorResponse,
} from "./generated/control";
import { AccountInventoryApiError } from "./account-inventory-types";
import type { AccountInventoryApi, AccountInventoryItem } from "./account-inventory-types";

type GeneratedResponse<T> = { data: T | unknown; status: number };

function isErrorResponse(value: unknown): value is ErrorResponse {
  return typeof value === "object" && value !== null && "code" in value && "request_id" in value;
}

function unwrap<T>(response: GeneratedResponse<T>): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  const fallback: ErrorResponse = { code: "internal_error", message: "请求未完成", request_id: "client-unknown" };
  throw new AccountInventoryApiError(response.status, isErrorResponse(response.data) ? response.data : fallback);
}

function mapItem(value: GeneratedAccountInventoryItem): AccountInventoryItem {
  return {
    instanceId: value.instance_id,
    provider: value.provider,
    email: value.email,
    basicStatus: value.basic_status,
    lifecycle: value.lifecycle,
    consecutiveMissingCount: value.consecutive_missing_count,
    firstSeenAt: value.first_seen_at,
    lastSeenAt: value.last_seen_at,
    missingSince: value.missing_since ?? null,
    outOfScopeSince: value.out_of_scope_since ?? null,
    lastRefreshAt: value.last_refresh_at ?? null,
    nextRetryAt: value.next_retry_at ?? null,
    sourceUpdatedAt: value.source_updated_at ?? null,
    providerLastCompleteAt: value.provider_last_complete_at,
    providerDegraded: value.provider_degraded,
    snapshotFreshness: value.snapshot_freshness,
  };
}

export const generatedAccountInventoryApi: AccountInventoryApi = {
  async query(csrfToken, filters) {
    const response = unwrap<AccountInventoryQueryResponse>(await queryAccountInventory({
      instance_id: filters.instanceId,
      provider: filters.provider,
      lifecycle: filters.lifecycle,
      basic_status: filters.basicStatus,
      email: filters.email,
      cursor: filters.cursor,
      limit: filters.limit,
    }, {
      cache: "no-store",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": csrfToken },
    }));
    return { items: response.items.map(mapItem), nextCursor: response.next_cursor ?? null };
  },
};

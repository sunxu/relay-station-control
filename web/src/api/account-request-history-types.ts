import type { NodeAccountRequestHistoryItem, NodeAccountRequestHistoryResponse } from "./generated/control";

export type AccountRequestHistoryItem = NodeAccountRequestHistoryItem;
export type AccountRequestHistoryResponse = NodeAccountRequestHistoryResponse;

export interface AccountRequestHistoryApi {
  requestHistory(instanceId: string, accountKey: string, cursor?: string, signal?: AbortSignal): Promise<AccountRequestHistoryResponse>;
}

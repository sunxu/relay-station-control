import type { AccountAvailability as GeneratedAvailability, NodeAccountAvailabilityOccurrenceItem, NodeAccountAvailabilityOccurrenceResponse } from "./generated/control";

export type AccountAvailability = NonNullable<GeneratedAvailability>;
export type AccountAvailabilityState = AccountAvailability["state"];
export type AccountAvailabilityOccurrence = NodeAccountAvailabilityOccurrenceItem;
export type AccountAvailabilityOccurrenceReason = AccountAvailabilityOccurrence["reason"];
export type AccountAvailabilityOccurrenceStatus = AccountAvailabilityOccurrence["status"];
export type AccountAvailabilityOccurrenceResponse = NodeAccountAvailabilityOccurrenceResponse;

export interface AccountAvailabilityApi {
  accountAvailabilityOccurrences(
    instanceId: string,
    accountKey: string,
    status: AccountAvailabilityOccurrenceStatus | undefined,
    cursor?: string,
    signal?: AbortSignal,
  ): Promise<AccountAvailabilityOccurrenceResponse>;
}

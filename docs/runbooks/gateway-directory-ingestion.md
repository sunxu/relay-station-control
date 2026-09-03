# Gateway Directory Ingestion Runbook

## Cadence and freshness

- Scheduler uses a 180s slot cadence per Gateway.
- Freshness is driven by `last_success_received_at`.
- `fresh` means the last successful observation is within 540s.
- `stale` means the last successful observation is older than 540s.
- `unknown` means no successful observation exists yet.

## Secret configuration

- Reuse `gateway_instances.reader_secret_ref`.
- The secret must resolve to the Directory reader token.
- Do not store the token in logs, metrics, or response payloads.

## Retry and failure behavior

- Retryable failures can move a run to `retry_wait` while the claim window is still open.
- Non-retryable failures end the run as `failed`.
- Lease loss is a normal outcome during contention or recovery.
- `source_time_invalid` is terminal and does not refresh freshness.

## Restart recovery

- Reconciler resumes expired `running`, `pending`, and `retry_wait` runs from durable state.
- Expired `running` runs may be returned to `retry_wait` only while the retry window is still open.
- Reconciler clears stale fencing tokens before the next claim.
- Do not replay an old in-memory response after restart; the next claim must fetch again.

## Common errors

- `lost_lease`: another worker won the fence or the lease expired.
- `source_time_invalid`: the source timestamp is outside the frozen tolerance window.
- `secret_unavailable`: the reader secret reference could not be resolved.
- `contract_invalid`: the response shape, field order, or URL contract is invalid.

## Rollout and rollback

- Roll out the migration and runtime together.
- Verify scheduler, claim, finalize, and reconciler behavior after deploy.
- Roll back by stopping the runtime first, then reverting the migration only if no live runs depend on the new schema.

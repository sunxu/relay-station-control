# Gateway Directory Ingestion Runbook

## Cadence and freshness

- Scheduler uses a 180s slot cadence per Gateway.
- Freshness is driven by `last_success_received_at`.
- `fresh` means the last successful observation is within 540s.
- `stale` means the last successful observation is older than 540s.
- `unknown` means no successful observation exists yet.

## Secret configuration

- The current Stage 0 path uses the Gateway asset's protected `directory_credential` state.
- `secret_configured` means only that the sealed credential column is non-NULL; it does not prove K2 availability or ciphertext usability.
- The bounded Directory resolver opens the credential with the external K2 only for one authenticated fetch. It must not use `reader_secret_ref`, `SecretResolver`, or `FileSecretResolver` fallback.
- Missing, wrong, structurally invalid, or mismatched K2 fails closed for credential-dependent fetches while credential-independent Control features remain available. K2 changes require a process restart; there is no hot reload.
- Do not store plaintext credential, sealed value, K2, commitment, or raw token in logs, metrics, audit, response payloads, or evidence.

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
- `secret_unavailable`: the protected Directory credential could not be read or opened with the current K2.
- `contract_invalid`: the response shape, field order, or URL contract is invalid.

## Rollout and rollback

- Roll out the Migration 00051 protected credential state and runtime together.
- Verify scheduler, claim, finalize, and reconciler behavior after deploy.
- A local environment containing non-NULL legacy `reader_secret_ref` must be rebuilt and assets re-registered; do not import or dual-read the legacy reference. Production rollback is stop-forward only and must not rewrite the shipped migration.

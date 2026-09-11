# Recovery Harness Hardening follow-up

Status: `DEFERRED — separate Recovery Harness Hardening follow-up`

This follow-up is intentionally outside `harden-runtime-acceptance-harness`. It must add a self-contained runtime orchestration using a real Control process, isolated PostgreSQL, and a controlled executor; focused unit/integration tests must not be presented as runtime orchestration evidence.

Required scenarios:

1. `pending restart`: create a pending durable job, stop before claim, restart the same candidate, and verify completion with stable identity and payload/hash.
2. `retry_wait restart`: observe persisted retry/backoff state, stop before availability, restart, and verify attempt budget and identity preservation.
3. `running lease SIGKILL -> expiry -> reconciler takeover`: terminate the worker while a valid lease is running, wait for lease expiry, restart/reconcile, and verify a new fence, stable job identity/payload/hash, and no stale-fence commit.

The follow-up must use controlled transport only, clean up its containers/volumes/secrets, and record only safe IDs, states, counts, timestamps, and hashes.

# runtime-recovery-harness Specification

## Purpose
TBD - created by archiving change harden-runtime-recovery-harness. Update Purpose after archive.

## Requirements

### Requirement: Real process pending restart recovery

The harness MUST create a durable notification job through the approved production lifecycle, start the frozen candidate Control process, and prove that the same job reaches success without changing its operation ID, idempotency key, payload bytes/hash, or attempt budget.

#### Scenario: Pending job survives process start

- **WHEN** a pending job exists at attempt zero before Control starts
- **THEN** the production Worker processes it and the durable identity remains unchanged

### Requirement: Real process retry-wait recovery

The harness MUST persist a controlled retryable or unknown failure, stop and restart the same candidate, and prove that `retry_wait`, `available_at`, attempt budget, and durable identity survive the restart.

#### Scenario: Retry wait survives restart

- **WHEN** a job is in retry_wait after attempt one and Control stops before availability
- **THEN** the restarted Worker continues the same job without resetting attempt or backoff

### Requirement: Running lease crash takeover

The harness MUST SIGKILL the real Control process while a controlled request holds a running lease, wait for real lease expiry, and prove that the Reconciler acquires a different valid fence. A stale fence MUST be rejected and the job identity, payload hash, and attempt budget MUST remain stable.

#### Scenario: Expired running lease is recovered

- **WHEN** the process holding a running lease is killed and the lease expires
- **THEN** the restarted Reconciler/Worker recovers according to the persisted policy and an old fence cannot commit

### Requirement: Safe controlled execution

The harness MUST use only a controlled HTTPS endpoint, MUST never load real DingTalk configuration, MUST not persist secrets or raw sensitive responses, and MUST remove containers, volumes, and runtime artifacts after each scenario.

#### Scenario: Controlled cleanup

- **WHEN** a recovery scenario completes or fails
- **THEN** real DingTalk sends remain zero and all scoped runtime resources are removed

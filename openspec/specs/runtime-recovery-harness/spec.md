# runtime-recovery-harness Specification

## Purpose
提供基于真实 Control production main、隔离 PostgreSQL 与受控 HTTPS endpoint 的 durable-job crash/restart acceptance harness，覆盖 pending restart、retry_wait restart 和 running-lease SIGKILL 后的 lease-expiry/Reconciler takeover，且不改变 production behavior、不发送真实 DingTalk。

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

### Requirement: Recovery SHALL treat PostgreSQL and K2 as one recovery set

Backup、restore 与 host migration MUST 将 PostgreSQL protected state 与正确 K2 一起管理；不得从每资产 legacy Secret 文件重建。K2 变更仅在进程重启后生效，不支持 hot reload。

#### Scenario: database and correct K2 restore credential usability
- **WHEN** operator 恢复数据库及其匹配 K2 并重启 Control
- **THEN** Node 与 Gateway credential-dependent paths 均恢复，sealed state 与 K2 commitment 保持一致

#### Scenario: wrong or missing K2 fails closed
- **WHEN** operator 恢复数据库但提供错误或缺失 K2
- **THEN** credential-dependent paths 不可用、credential-independent paths 仍可用，且 sealed state/commitment 不被改写

### Requirement: Local transition SHALL use fresh database and re-registration

Stage 0 local/dev fixture transition MUST 使用 fresh local DB、正确 K2 provisioning 与 Node/Gateway re-register；不得建设 local legacy importer、backfill、dual-read 或 dual-write。

#### Scenario: legacy local database contains references
- **WHEN** local Migration 00051 guard 发现 non-null legacy `reader_secret_ref`
- **THEN** transition 停止并要求重建本地数据库与重新注册资产，不尝试自动导入

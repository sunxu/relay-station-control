# runtime-acceptance-harness Specification

## Purpose
提供统一、可复用且安全的 runtime acceptance tooling，包括 repo-external runtime orchestration、isolated Buildx candidate-image 校验、authenticated session/restart restore 与 production lifecycle fixture；该 capability 不改变 Control production behavior，durable-job recovery orchestration 由独立 capability 负责。

## Requirements

### Requirement: Isolated image build

The harness MUST use an explicit temporary `BUILDX_CONFIG` and MUST verify the requested image revision label and platform without modifying `~/.docker/buildx`.

#### Scenario: Candidate image verification

- **WHEN** an operator supplies a candidate SHA and image tag
- **THEN** the builder produces an image with matching `org.opencontainers.image.revision` and reports only safe image identity fields

### Requirement: External authenticated session

The harness MUST require a repo-external runtime directory, write storage-state with mode `0600`, and MUST NOT persist TOTP URI, passwords, bootstrap secrets, cookies or CSRF tokens in evidence.

#### Scenario: Session bootstrap

- **WHEN** the acceptance composition exposes the real bootstrap API
- **THEN** the auth runner performs bootstrap and TOTP in one browser context and saves storage-state only under the explicit external runtime directory

### Requirement: Production lifecycle fixture

The fixture MUST use production Availability `Reconcile()` and Duplicate `Evaluate()` paths. It MUST NOT directly enqueue notification jobs or implement a second hashing/idempotency algorithm.

#### Scenario: Four lifecycle transitions

- **WHEN** the fixture runs against an isolated database
- **THEN** it can observe Availability ACTIVE/RESOLVED and Duplicate ACTIVE/RESOLVED durable-job transitions without changing production code

### Requirement: Recovery scope boundary

The harness MUST explicitly defer self-contained pending, retry-wait, running-lease crash recovery, and old-binary runtime orchestration to a separate follow-up. Focused durable-job tests and Phase 5 deployment-readiness evidence remain the sources for production recovery behavior; this change MUST NOT claim those scenarios as runtime-harness PASS.

#### Scenario: Deferred recovery work

- **WHEN** an operator uses this harness change
- **THEN** it provides environment, auth, and lifecycle infrastructure while reporting recovery orchestration as `DEFERRED — separate Recovery Harness Hardening follow-up`

### Requirement: Stage 0 acceptance SHALL prove immutable class-4 provenance and compatibility obligations

Acceptance harness MUST 将 exact source SHA、class-4 signed manifest v1、immutable Control artifact、Migration 00051、floor 4、running artifact 与 K2 identity commitment 的 ownership/match/unchanged 状态对齐，并覆盖 Compatibility Addendum O01–O05。Commitment 检查必须在内部完成，evidence 只输出 PASS/FAIL 或 `present`、`match`、`unchanged` 布尔值；不得打印、记录、snapshot 或导出 commitment value。Class 0..3 manifest structure MUST 保持有效，signed class-3 artifact 在 floor-4/Migration-51 DB 上 MUST 在 Control 启动前被拒绝。

#### Scenario: reject and restore preserve credential state
- **WHEN** harness 先执行 O04 class-3 reject，再以 exact class-4 current candidate 验证同一数据库
- **THEN** reject 不修改 Node/Gateway sealed state 或 K2 commitment，class-4 candidate 可读取并使用原有合法 state 而不因 gate admission 静默重写；evidence 仅报告 `unchanged=true/false` 或 PASS/FAIL

### Requirement: Stage 0 acceptance SHALL remain representative and secret-safe

Runtime acceptance MUST 证明 Node authenticated read/account mutation、Gateway Directory read、credential set/clear/Replace/Retire、wrong/missing K2 fail-closed 与 representative UI credential flows；并 MUST 扫描 API/DOM/log/audit/metrics/trace/test evidence/acceptance artifact，确保 plaintext credential、raw K2、sealed credential blob、K2 identity commitment value、credential-bearing header 与 raw native body 不泄漏。Browser MUST 不承担 crypto、ACL、migration、race 或 compatgate matrix 的 owning proof。

#### Scenario: representative runtime acceptance completes
- **WHEN** exact class-4 candidate 在 production-like stack 上运行最小 Stage 0 acceptance set
- **THEN** changed cross-layer paths 通过，Secret scan 为零泄漏，Control 仍在数据面之外且 Gateway/Node artifact 不变

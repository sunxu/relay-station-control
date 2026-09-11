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

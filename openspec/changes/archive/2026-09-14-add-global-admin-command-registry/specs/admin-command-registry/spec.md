## Purpose

定义所有管理员命令共用的不可变全局 `command_id` 预留、actor-first 冲突边界、历史回填、receipt 完整性与兼容性阻断契约。

## ADDED Requirements

### Requirement: Administrator commands SHALL share one durable global command namespace

Control SHALL reserve every accepted administrator `command_id` in one immutable `admin_command_registry` before any command-domain writer can complete. The registry MUST cover existing Gateway asset, Relay Node asset, Node Monitoring and Phase 7 account command domains. `command_id` MUST be globally unique across those domains; a per-table UNIQUE constraint MUST NOT be treated as global identity.

#### Scenario: Same UUID exists in another command domain
- **WHEN** an asset command ID is already registered and an account operation presents the same UUID, or vice versa
- **THEN** Control resolves the existing global reservation and MUST NOT create a second command identity or perform remote/domain mutation

### Requirement: Global command lookup SHALL preserve actor-first conflict priority

After authentication, active session, `super_admin` and CSRF validation, every command writer MUST acquire the shared UUID-derived transaction advisory serialization and lookup the global reservation before target lookup, Secret parsing/fingerprinting, lifecycle/revision validation or remote calls. A different actor MUST receive `command_conflict` before all later validation. The same actor with a different command domain/kind MUST also receive `command_conflict`.

#### Scenario: Different actor supplies malformed later input
- **WHEN** actor B reuses actor A's registered command ID while also supplying an invalid endpoint, Secret or target
- **THEN** Control returns `command_conflict` without evaluating or exposing the later validation result and performs zero mutation

#### Scenario: Same actor changes command kind
- **WHEN** the original reservation is for a Node lifecycle command and the same actor reuses the ID for a monitoring/account command
- **THEN** Control returns `command_conflict` and performs zero mutation

### Requirement: Registry SHALL backfill and protect historical completed receipts

The forward migration MUST backfill every existing `asset_admin_command_receipts` row into the global registry before enforcement and MUST fail atomically on missing actors, unsupported metadata, divergent duplicates or count mismatch. After backfill, every asset receipt command ID MUST reference the registry, and registry rows MUST reject UPDATE, DELETE and TRUNCATE.

#### Scenario: Historical receipt backfill is incomplete
- **WHEN** migration cannot represent every existing receipt exactly
- **THEN** migration fails and leaves the previous schema authoritative; it MUST NOT silently drop, rewrite or synthesize history

### Requirement: Registry enforcement SHALL fail closed against unsupported writers

Existing asset and monitoring command writers MUST reserve through the registry in the same transaction as mutation/audit/receipt. A receipt without a matching reservation MUST be rejected so a pre-registry writer cannot silently commit a domain transition on the forward schema. The release compatibility floor MUST advance before the registry schema is opened for production writes; unsupported rollback binaries MUST not start.

#### Scenario: Old writer reaches forward schema
- **WHEN** an unsupported pre-registry writer attempts a new asset command after registry enforcement
- **THEN** the command cannot commit a receipt or domain mutation, and the supported compatibility wrapper also rejects starting that artifact

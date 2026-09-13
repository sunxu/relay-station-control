## Context

Phase 6 shared admin commands use global-looking UUIDs, advisory serialization, actor-first receipt lookup and immutable completed receipts, but the durable uniqueness boundary is the `asset_admin_command_receipts` table. Phase 7 needs nonterminal remote operation state in a separate table. The frozen architecture therefore requires a true global registry used by both domains.

## Goals / Non-Goals

### Goals

- one durable `command_id` namespace for all administrator commands;
- actor mismatch detected before intent/Secret/target validation;
- existing asset/monitoring exact replay remains unchanged;
- historical receipts are fully represented before enforcement;
- old writers cannot silently bypass registry after forward migration;
- additive/forward-only rollout with deterministic rollback barrier.

### Non-Goals

- account remote execution state;
- account credential mutation/upload;
- generic job/workflow orchestration;
- changes to K1 bytes/domain/version;
- changes to Gateway/Node native data-plane responsibilities.

## Decisions

### 1. Exact planned registry schema

The forward migration SHALL create `public.admin_command_registry` with the following logical schema (exact SQL spelling may only vary where PostgreSQL syntax requires it):

```text
command_id uuid PRIMARY KEY
actor_admin_id uuid NOT NULL FK control_admin_users(admin_id) ON UPDATE RESTRICT ON DELETE RESTRICT
command_domain text NOT NULL CHECK IN ('asset_admin','account_admin')
command_kind text NOT NULL, 1..96 bytes, fixed safe token grammar
intent_encoding_version smallint NOT NULL CHECK > 0
canonical_intent_hash bytea NOT NULL CHECK octet_length=32
secret_fingerprint_key_version smallint NULL CHECK > 0 when present
reserved_at timestamptz NOT NULL, finite
```

The registry stores no command result/body and no mutable execution state. `command_domain` distinguishes existing completed asset/monitoring commands from Phase 7 account commands; `command_kind` remains the finer operation discriminator.

The table SHALL reject UPDATE, DELETE and TRUNCATE through ownership/ACL plus immutable trigger protection. Runtime roles receive no direct DML; writes happen only through reviewed controlled functions/transactions.

### 2. Global advisory serialization and lookup order

Every administrator command writer SHALL derive the existing full-UUID advisory lock key using the shared implementation, acquire the transaction advisory lock, then perform registry lookup.

Order after HTTP authentication/session/super_admin/CSRF:

```text
command advisory lock
-> registry lookup by command_id
-> actor comparison
-> domain/kind comparison
-> only then intent encoding / Secret fingerprint / target/current-state validation
```

If actor differs: `409 command_conflict` before any later validation. If actor matches but domain/kind differs: the same conflict. Same actor/domain/kind proceeds to canonical-intent comparison and the existing domain-specific replay/current-operation path.

### 3. Historical backfill

The migration SHALL, before adding enforcement, insert one registry row for every existing `asset_admin_command_receipts` row using its immutable actor, `command_kind`, encoding/hash/key-version and committed/reserved timestamp. Historical rows map to `command_domain='asset_admin'`.

Backfill MUST prove:

- count parity;
- no duplicate command IDs with divergent data;
- hashes exactly 32 bytes;
- supported historical encoding/key versions;
- every receipt has an existing actor.

Any inconsistency aborts the whole migration; it MUST NOT repair, overwrite or choose one duplicate.

### 4. Registry↔receipt relationship

After backfill, `asset_admin_command_receipts.command_id` SHALL reference `admin_command_registry.command_id`. A receipt insert without a reservation fails and rolls back the enclosing domain transaction.

For existing asset/Node/Monitoring mutations, registry reservation and final receipt remain in the same PostgreSQL transaction as the domain mutation/audit. Therefore a receipt/FK failure rolls back the entire command.

Registry row metadata MUST equal the receipt's actor/kind/encoding/hash/key-version. A deferred/controlled integrity check or receipt insert function SHALL fail closed on mismatch; direct runtime insert paths remain unavailable.

### 5. Existing writer migration

Gateway register/edit/retire/replace, Node register/edit/retire/replace and Node Monitoring Enable/Disable SHALL call the shared registry reservation/lookup path. Health/Connection Test are not command-ID operations and do not reserve IDs.

Existing same-command replay remains exact persisted receipt replay; no existing success body/status is changed.

### 6. Phase 7 future writer contract

`account_admin_operations` (planned in the dependent change) will reserve `command_domain='account_admin'` in the same registry. A nonterminal account operation MAY exist without a completed asset-style terminal receipt, but it can never reuse an ID reserved by an asset command.

### 7. Direct-DML and role boundary

Migration owner owns the table/functions. Runtime application roles have only EXECUTE on controlled functions and bounded SELECT needed for actor-first lookup. PUBLIC privileges are revoked. Immutable registry rows cannot be pruned while any supported command/receipt depends on them.

### 8. Compatibility barrier and rollout

Change A SHALL raise the compatibility floor because an old binary does not reserve global command IDs. Numeric class/floor is intentionally assigned only after implementation artifacts and signed release metadata exist.

Required rollout order:

```text
stop old Control
install gate/trust-root/wrapper that understands new floor
forward migration: create/backfill/enforce registry and advance floor
start only compatible new Control artifact
```

The DB-level receipt FK is an additional fail-closed defense: an unsupported old writer that somehow reaches the schema cannot commit a new receipt/domain mutation without a registry reservation.

Rollback after floor advancement uses the normal compatibility wrapper and MUST NOT start a pre-registry binary. Production does not down-migrate or delete registry evidence.

## Concurrency Invariants

- Two requests using the same command ID serialize on one advisory lock.
- Different actors cannot learn target/Secret validation results for an existing ID.
- Cross-domain reuse is always conflict.
- Backfill/enforcement occurs before runtime writers are allowed.
- Registry/receipt writes for existing commands are atomic with the domain transaction.

## Failure Recovery

- Crash before reservation commit: no registry row; retry may proceed.
- Existing asset command crash before transaction commit: registry/domain/audit/receipt all rollback.
- Existing asset command commit response lost: registry+receipt persist; retry returns exact receipt.
- Registry/receipt mismatch: service fails closed; no mutation is attempted to repair history.

## Validation / Acceptance

PostgreSQL 18 acceptance SHALL cover backfill parity, immutable guards, actor-first ordering, same-ID cross-domain races, existing receipt replay, monitoring commands, direct-DML denial, old-writer fail-closed behavior and compatibility rollback through the mandatory wrapper.

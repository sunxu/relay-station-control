## Why

Phase 7 requires explicit Antigravity account Disable, Enable, Remove, Upload New and Replace Existing while keeping Control outside the request data plane and CLIProxyAPI authoritative for credential/runtime semantics. The previous Stage 7N Relay-specific Node mutation protocol was implemented and reviewed, but the current Native-First architecture candidate supersedes it as a Stage 7B dependency to avoid a parallel Node protocol and maintenance surface.

This change now plans a bounded adapter over upstream CLIProxyAPI `v7.3.2` at exact tag commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`. Phase 7 Relay-specific Node mutation protocol count is zero.

## What Changes

- Add minimal durable `account_admin_operations` execution/recovery truth with durable same-`(node_instance_id,account_key)` serialization.
- Expose explicit single-account Control APIs for Disable, Enable, Remove, Upload New, Replace Existing and operation read; add separate high-risk lifecycle-block and same-account-block overrides.
- Restrict the Node adapter to native `GET /auth-files`, `PATCH /auth-files/status`, single-name `DELETE /auth-files`, and raw-JSON `POST /auth-files?name=`. No arbitrary management passthrough.
- Gate every fresh snapshot on exact `X-CPA-VERSION` and pinned `X-CPA-COMMIT`, classify manager-backed file eligibility using transient source/runtime evidence, then project to the minimal safe target fields.
- Use fresh exactly-one provider/email target resolution. `name` and `auth_index` remain ephemeral native request evidence, not durable/public identity.
- Accept upstream native last-writer-wins: Upload New is best-effort create; Replace Existing is best-effort replace. No CAS, target incarnation or Node postcondition proof.
- Bound Control credential ingress to 1 MiB, validate minimal Antigravity identity, canonicalize metadata aliases, and reject the complete reviewed v7.3.2 runtime/routing/management denylist without taking ownership of provider credential schema.
- Preserve global command actor-first identity, immutable terminal replay, no automatic redispatch after ambiguous outcome, Node-first lifecycle locking, normal Inventory independent business observation, audit and Secret boundaries.
- Treat timeout, connection/response loss and ambiguous native 5xx as `outcome_unknown`; block lifecycle until explicit high-risk override or stable terminal classification.
- Decide Disable/Enable noop from the fresh pre-dispatch snapshot and send zero PATCH; any sent successful PATCH is applied.
- Give the lifecycle override and same-account override separate global command identities and immutable receipts while keeping their blocker scopes orthogonal; both are manual high-risk override actions, not a workflow.
- Preserve exact account canonical intent v1 and upload HMAC equality without restoring any Node-side proof/fencing protocol.

## Capabilities

### New Capabilities

- `account-admin-operation`: Control command/API state, native safe adapter, durable serialization, conservative outcome recovery, manual high-risk lifecycle and same-account overrides, audit, metrics and UI behavior.

### Modified Capabilities

- `asset-admin-command`: account commands use the archived global registry and a separate immutable account receipt without changing asset K1 or asset receipts.
- `relay-node-asset-lifecycle`: Node Retire/Replace inspects durable dispatched/outcome-unknown account-operation blockers and may proceed only after the reviewed explicit override.
- `account-inventory-poll-run`: normal Inventory remains an independent business-observation surface; account execution does not request, schedule or reconcile a Phase 7 verification run.
- `account-inventory-snapshot`: Inventory remains an independent business observation surface and never changes account execution truth.

## Dependencies

1. Ops Native-First Crash Recovery Corrective Round 12 revision candidate and transition plan in `../ops/docs/phase-5-7/`.
2. Archived `add-global-admin-command-registry`: migration `37`, compatibility class/floor `3 / 3`.
3. CLIProxyAPI upstream release `v7.3.2`, exact tag commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`.
4. Existing Phase 6 Node lifecycle/monitoring, account Inventory and HTTP-only management contracts.

Historical Stage 7N final revision `72c435b1b1b85b341a734e3860081c7782d9cbd2` and image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4` remain preserved evidence only: `HISTORICAL / SUPERSEDED CANDIDATE / NOT CURRENT IMPLEMENTATION DEPENDENCY / NOT CURRENT DEPLOYMENT BASELINE`.

## Impact

Future implementation is expected to add additive Control schema/state, OpenAPI routes/generated clients, a bounded native CLIProxyAPI adapter, admin UI, audit/metrics and acceptance harnesses. It does not modify Gateway or define Node product changes. A separate transition gate governs any future ordinary revert/alignment of historical Stage 7N code; this planning task runs no revert.

## Non-Goals

No Relay-specific Node account protocol, OAuth/Re-auth, automatic repair/move/remove, Credential Vault, workflow engine, provider framework, batch/all operation, Gateway mutation, new Inventory subsystem, off-grid polling, internal HTTPS/TLS, scheduler replacement or data-plane participation.

## Planning status

Native-First Crash Recovery Corrective Round 12 follows the fixed review baseline (Ops `ba9547818b7d48de370b3e6f9a2d92bd612a920e`, Control `7b5b1bc7bb29f176d6f6fa7e0067828a183040cf`, Node `72c435b1b1b85b341a734e3860081c7782d9cbd2`, Gateway `b2512a314`). The previous independent crash-recovery re-review recorded P0=0, P1=2, P2=1 / CHANGES REQUIRED. Round 12 resolutions are incorporated as P0=0, P1=0 candidate, P2=0 candidate. Architecture status = `READY FOR INDEPENDENT CRASH-RECOVERY RE-REVIEW`; Gate 1 is `NOT CLOSED`; ADR remains `PROPOSED`, runtime artifact identity is `NOT YET FROZEN`, Node revert is `NOT RUN`, and Stage 7B implementation is `NOT STARTED`.

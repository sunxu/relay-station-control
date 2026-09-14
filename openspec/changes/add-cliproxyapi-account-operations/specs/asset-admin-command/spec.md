## ADDED Requirements

### Requirement: Phase 7 account commands SHALL preserve shared global identity without expanding asset K1

Phase 7 account commands MUST use the same global command reservation, advisory serialization and actor-first namespace introduced by `add-global-admin-command-registry`. Existing `CONTROL_ASSET_INTENT_KEY_FILE` K1 bytes, domains, versions and asset command encodings MUST remain unchanged. Upload Secret equality MUST use the separately reviewed Phase 7 keyed-fingerprint contract.

Change B MUST use a separate immutable `account_admin_command_receipts` relation and MUST NOT extend the asset-only receipt domain. A stable terminal `remote_applied`, `remote_noop` or `failed` account mutation MUST atomically create exact original POST status/body replay evidence. `prepared`, `dispatched` and `outcome_unknown` MUST NOT create a terminal receipt. The separate relation MUST also receipt independent `account.lifecycle_override` commands while linking each to its target operation; an override receipt MUST NOT reuse or rewrite the target mutation receipt. Verification and target-operation override fields MUST NOT rewrite immutable replay evidence.

#### Scenario: Upload command tries to use asset K1 fallback
- **WHEN** the Phase 7 upload fingerprint key is unavailable but asset K1 is valid
- **THEN** upload fails closed and asset K1 is neither read as fallback nor semantically expanded

#### Scenario: Outcome unknown same-command retry
- **WHEN** the same actor and intent retry while execution is `outcome_unknown`
- **THEN** Control returns the current operation projection with 202, creates no receipt and sends zero native mutation

#### Scenario: Verification changes after terminal POST
- **WHEN** a terminal POST receipt exists and later Inventory changes verification state
- **THEN** exact POST replay returns the original persisted status/body while GET returns the current mutable verification projection

#### Scenario: Ambiguous native server error
- **WHEN** a native request may have reached mutation and returns an ambiguous 500 or loses its response
- **THEN** execution becomes `outcome_unknown` and Control creates no terminal receipt or inferred execution result

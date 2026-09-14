## ADDED Requirements

### Requirement: Phase 7 account commands SHALL preserve shared global identity without expanding asset K1

After `add-global-admin-command-registry`, Phase 7 account commands MUST use the same global command reservation/advisory/actor-first namespace. Existing `CONTROL_ASSET_INTENT_KEY_FILE` K1 bytes, domains, versions and existing asset command canonical encodings MUST remain unchanged. Account upload Secret equality MUST use the separately reviewed Phase 7 keyed-fingerprint contract. Change B MUST use a separate immutable `account_admin_command_receipts` relation and MUST NOT extend the asset-only receipt domain. Terminal `remote_applied|remote_noop|remote_partial|failed` transitions MUST atomically create exact original POST replay evidence; `prepared|dispatched|outcome_unknown` MUST NOT create it. Nonterminal retries return `202` with the current operation projection and MUST NOT redispatch solely due to retry.

#### Scenario: Upload command tries to use asset K1 fallback
- **WHEN** Phase 7 upload key is unavailable but asset K1 is valid
- **THEN** upload fails closed and existing asset K1 is neither read as fallback nor semantically expanded

#### Scenario: Nonterminal same-command retry
- **WHEN** the same actor/intent retries a Phase 7 POST while its remote operation is already dispatched and nonterminal
- **THEN** Control returns the defined current-operation projection with zero additional remote dispatch; once terminal replay evidence exists, later exact replay follows that immutable evidence

#### Scenario: Outcome unknown remains recoverable
- **WHEN** a dispatched request loses its response and execution is `outcome_unknown`
- **THEN** Control creates no terminal receipt, returns the current projection with `202` on same-command retry, and uses recovery without redispatch

#### Scenario: Verification changes after terminal POST
- **WHEN** a terminal POST receipt exists and later Inventory changes verification state
- **THEN** exact POST replay returns the original persisted status/body while GET returns the current mutable verification projection

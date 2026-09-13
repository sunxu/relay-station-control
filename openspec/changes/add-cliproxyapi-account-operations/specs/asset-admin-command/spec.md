## ADDED Requirements

### Requirement: Phase 7 account commands SHALL preserve shared global identity without expanding asset K1

After `add-global-admin-command-registry`, Phase 7 account commands MUST use the same global command reservation/advisory/actor-first namespace. Existing `CONTROL_ASSET_INTENT_KEY_FILE` K1 bytes, domains, versions and existing asset command canonical encodings MUST remain unchanged. Account upload Secret equality MUST use the separately reviewed Phase 7 keyed-fingerprint contract. A terminal account POST replay MAY materialize immutable exact response evidence only after the account-operation execution contract permits terminalization; nonterminal retries use the current account-operation projection and MUST NOT redispatch solely due to retry.

#### Scenario: Upload command tries to use asset K1 fallback
- **WHEN** Phase 7 upload key is unavailable but asset K1 is valid
- **THEN** upload fails closed and existing asset K1 is neither read as fallback nor semantically expanded

#### Scenario: Nonterminal same-command retry
- **WHEN** the same actor/intent retries a Phase 7 POST while its remote operation is already dispatched and nonterminal
- **THEN** Control returns the defined current-operation projection with zero additional remote dispatch; once terminal replay evidence exists, later exact replay follows that immutable evidence

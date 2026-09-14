## ADDED Requirements

### Requirement: Inventory SHALL verify account-operation business convergence without becoming execution proof

Phase 7 verification MUST consume only accepted fresh, complete, eligible normal Inventory evidence. Disable requires the same account_key with disabled=true; Enable requires disabled=false; Remove requires account absence only from provider-complete evidence; Upload New requires the expected identity to appear; Replace Existing requires the expected identity to remain present. Inventory MUST NOT be treated as exact credential-byte, compare-and-swap, native execution or remote-quiescence proof. Stale, incomplete, disk-fallback or duplicate evidence MUST NOT fabricate success.

#### Scenario: Replace identity remains present after response loss
- **WHEN** fresh Inventory still contains the expected account after an ambiguous Replace response
- **THEN** business convergence may be observed but execution remains `outcome_unknown`

#### Scenario: Remove sees incomplete Provider evidence
- **WHEN** the account is absent from incomplete, disk-fallback or stale evidence
- **THEN** absence does not prove removal and verification remains pending or inconclusive according to its independent deadline

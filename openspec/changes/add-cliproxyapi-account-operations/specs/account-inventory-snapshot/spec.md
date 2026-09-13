## ADDED Requirements

### Requirement: Inventory SHALL verify account-operation business convergence without becoming credential proof

Phase 7 verification MUST consume only accepted fresh/complete/eligible normal Inventory evidence. Disable/Enable require the same account_key with desired disabled projection; Remove requires account absence only from provider-complete evidence. Upload New/Replace require exactly one expected account_key plus independent Node remote postcondition proof. Snapshot/account identity presence MUST NOT by itself prove which credential bytes were committed, and stale/incomplete/disk-fallback/duplicate evidence MUST NOT fabricate success.

#### Scenario: Replace identity is unchanged
- **WHEN** Inventory still shows the expected account_key after Replace but remote postcondition proof for this dispatch is missing
- **THEN** Control does not mark verification `verified`; Inventory remains business-convergence evidence only

#### Scenario: Remove sees incomplete Provider evidence
- **WHEN** the account is absent from an incomplete/disk-fallback/stale observation
- **THEN** absence does not prove removal and verification remains pending/inconclusive according to deadline/evidence

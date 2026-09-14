## ADDED Requirements

### Requirement: Node Retire and Replace SHALL honor durable account-operation blockers

Retire/Replace MUST acquire the existing Node lifecycle row first and then inspect same-Node account operations using the shared Node-first lock order. Any `dispatched` or unresolved `outcome_unknown` operation MUST return `account_operation_in_progress` with zero lifecycle mutation unless that exact operation has a valid durable lifecycle override. No database lock may be held across native HTTP.

A lifecycle override MUST persist `lifecycle_override_at`, `lifecycle_override_by` and reason `process_restarted|node_stopped|risk_accepted`. It releases only the lifecycle block: it MUST NOT change execution or verification state, manufacture success/failure evidence, create receipt eligibility or authorize redispatch. `risk_accepted` explicitly records that the administrator waives the strict guarantee that a previously dispatched request can never mutate the old Node after lifecycle proceeds.

#### Scenario: Account dispatch commits first
- **WHEN** an account operation commits `prepared -> dispatched` before Retire or Replace locks the Node
- **THEN** lifecycle observes the durable blocker and rejects unless a valid override exists

#### Scenario: Retire commits first
- **WHEN** Retire locks and commits the Node before account dispatch authorization
- **THEN** later dispatch sees the terminal lifecycle and sends zero native request

#### Scenario: Control restarts with unresolved operation
- **WHEN** Control restarts while an operation is `dispatched` or `outcome_unknown`
- **THEN** the durable blocker remains effective without relying on an in-memory mutex

#### Scenario: Risk is explicitly accepted
- **WHEN** a super_admin submits the typed high-risk confirmation and `risk_accepted` override
- **THEN** lifecycle may proceed while the operation remains unresolved and no automatic redispatch occurs

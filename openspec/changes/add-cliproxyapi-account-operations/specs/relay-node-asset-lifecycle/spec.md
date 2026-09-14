## ADDED Requirements

### Requirement: Node Retire and Replace SHALL wait for proven account-operation remote quiescence

Retire/Replace MUST, after acquiring the existing Node-first lifecycle lock, inspect same-Node Phase 7 account operations. If any operation is `dispatched` and remote mutation may still begin/continue, lifecycle mutation MUST return `account_operation_in_progress` with zero retirement/replacement. Client timeout, `dispatch_deadline`, `remote_mutation_deadline`, `quiescence_deadline`, or elapsed operational bounds alone MUST NOT release the fence. Release requires a terminal Node response with `quiescent=true`, a successful shared-gate recovery resolve that durably fences the operation's exact `dispatch_token` as Node `fence_dispatch_token_v1` before read-back, or proven termination/restart of the exact Node process instance. No DB lock is held across HTTP.

#### Scenario: Retire races a still-running Node handler
- **WHEN** Control-side request timed out but the Node handler may still mutate credential state
- **THEN** Retire is blocked until remote quiescence is proven; it cannot retire first and allow the credential mutation to land afterward

#### Scenario: Control restarts with dispatched operation
- **WHEN** Control restarts before remote quiescence proof
- **THEN** lifecycle reads the durable account-operation fence and remains blocked; once lifecycle eventually commits, the old operation can never redispatch or target the replacement Node

#### Scenario: Quiescence deadline expires without Node proof
- **WHEN** the conservative `quiescence_deadline` passes but no terminal response, successful durable token-fencing resolve, or proven process restart exists
- **THEN** Retire/Replace remains blocked because elapsed time alone cannot prove the remote mutation stopped

### Requirement: Node mutation capability SHALL be explicitly declared and runtime-verified

Change B MUST add `management_account_mutation_v1` to the approved CLIProxyAPI Driver contract capability set without changing the existing immutable per-Node capability ownership rule. A Node may dispatch account mutation only when its durable capability declaration contains both `management_account_inventory_read` and `management_account_mutation_v1` and fresh authenticated runtime discovery confirms the exact frozen v1 contract. Migration MUST NOT silently grant the new capability to existing Node identities; an existing Node that lacks it requires the normal explicit lifecycle path, including Replace where immutable capability ownership requires a new identity.

#### Scenario: Existing Node lacks mutation capability
- **WHEN** an active legacy Node has Inventory-read capability but no durable `management_account_mutation_v1` declaration
- **THEN** Control returns `unsupported_node_contract` with zero remote mutation and does not infer support from image or commit metadata

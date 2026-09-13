## ADDED Requirements

### Requirement: Node Retire and Replace SHALL wait for proven account-operation remote quiescence

Retire/Replace MUST, after acquiring the existing Node-first lifecycle lock, inspect same-Node Phase 7 account operations. If any operation is `dispatched` and remote mutation may still begin/continue, lifecycle mutation MUST return `account_operation_in_progress` with zero retirement/replacement. `dispatch_deadline` or client timeout alone MUST NOT release the fence. Release requires terminal handler/quiescence evidence, safe recovery proof, or expiry of a conservative quiescence bound only for a pinned Node artifact whose bounded synchronous mutation contract has passed acceptance. No DB lock is held across HTTP.

#### Scenario: Retire races a still-running Node handler
- **WHEN** Control-side request timed out but the Node handler may still mutate credential state
- **THEN** Retire is blocked until remote quiescence is proven; it cannot retire first and allow the credential mutation to land afterward

#### Scenario: Control restarts with dispatched operation
- **WHEN** Control restarts before remote quiescence proof
- **THEN** lifecycle reads the durable account-operation fence and remains blocked; once lifecycle eventually commits, the old operation can never redispatch or target the replacement Node

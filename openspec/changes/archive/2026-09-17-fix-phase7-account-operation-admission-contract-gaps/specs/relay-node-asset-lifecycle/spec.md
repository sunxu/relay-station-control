## MODIFIED Requirements

### Requirement: Node lifecycle mutation SHALL expose the bounded account-operation blocker

Node Retire and Replace MUST preserve Node-first blocker inspection. When a same-Node dispatched or `outcome_unknown` account operation blocks lifecycle mutation without lifecycle override, the API MUST return HTTP `409` with error code `account_operation_in_progress`.

#### Scenario: Retire is blocked by unresolved account operation
- **WHEN** Retire is attempted while the Node has a blocking account operation
- **THEN** lifecycle remains unchanged and the response is `409 account_operation_in_progress`

#### Scenario: Replace is blocked by unresolved account operation
- **WHEN** Replace is attempted while the Node has a blocking account operation
- **THEN** lifecycle remains unchanged and the response is `409 account_operation_in_progress`

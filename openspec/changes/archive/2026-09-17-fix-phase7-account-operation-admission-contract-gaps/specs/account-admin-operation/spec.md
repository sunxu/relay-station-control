## MODIFIED Requirements

### Requirement: Account operation admission SHALL use current durable Node eligibility

Accepted account dispatch and no-op terminalization MUST acquire the Node-first durable boundary and re-check active lifecycle, current non-cancelled monitoring, `management_account_inventory_read`, matching active Provider policy, and same-account serialization before committing a prepared transition. Native HTTP MUST occur only after the transaction commits.

#### Scenario: stale eligibility is rejected
- **WHEN** a resolver snapshot is positive but any durable eligibility condition is false at admission
- **THEN** the operation becomes `failed` with the corresponding bounded error code, writes its normal receipt/audit atomically, and performs zero native mutation

#### Scenario: concurrent prepared no-op converges
- **WHEN** two callers admit the same prepared no-op command concurrently
- **THEN** one caller commits `remote_noop` and its immutable receipt, and the other returns the committed terminal operation/receipt without a state mismatch or native mutation

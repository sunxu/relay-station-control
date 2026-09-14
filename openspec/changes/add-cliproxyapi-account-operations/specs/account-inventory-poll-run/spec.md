## ADDED Requirements

### Requirement: Phase 7 verification SHALL only request existing fixed-slot Inventory scheduling

Account operations MAY wake/request the existing Inventory scheduler after execution becomes verification-eligible. The scheduler MUST retain PostgreSQL UTC 300-second aligned `scheduled_at`, uniqueness `(instance_id,scheduled_at)`, existing start grace, lease/fencing, Provider policy pinning and lifecycle/monitoring fences. If the current aligned slot is not materialized and remains inside normal start grace, the existing scheduler MAY create it; otherwise verification waits for the next normal aligned slot. Existing slot in any state MUST NOT be duplicated. Phase 7 MUST NOT create off-grid runs, special parsers/finalize paths or direct current-Inventory patches.

#### Scenario: Wake inside an existing slot
- **WHEN** an operation requests verification and the current Node+slot run already exists
- **THEN** no second run is created and normal run/future-slot behavior remains authoritative

### Requirement: Account mutation dispatch SHALL require current Inventory monitoring eligibility

Before native mutation dispatch, Control MUST use the existing Node-first lock graph to require active Node lifecycle, current non-cancelled monitoring activation, `management_account_inventory_read` capability and a matching active Provider policy. Monitoring or policy ineligibility MUST return `node_monitoring_ineligible` with zero native mutation. Control MUST NOT require a Relay-specific Node mutation capability or contract-discovery endpoint, automatically enable monitoring or bypass policy. Deployment compatibility evidence SHALL pin CLIProxyAPI upstream v7.3.2 commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`.

#### Scenario: Monitoring Disable commits first
- **WHEN** Monitoring Disable wins the Node lock before account dispatch authorization
- **THEN** account dispatch re-reads monitoring ineligible and sends zero native request

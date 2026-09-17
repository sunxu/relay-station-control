## ADDED Requirements

### Requirement: Account operations SHALL NOT own Inventory verification workflow

Normal Inventory SHALL continue on its existing independent cadence. Account operations MUST NOT create a Phase 7 verification state, wake or schedule a special Inventory run, create a durable verification job, or write Inventory observations back into `account_admin_operations`. Inventory observations are separate business evidence and MUST NOT terminalize or rewrite account execution truth.

#### Scenario: Inventory converges after an ambiguous native outcome
- **WHEN** normal Inventory later observes the requested account state after an operation is `outcome_unknown`
- **THEN** the observation remains separate and the operation remains `outcome_unknown` without redispatch or durable verification mutation

#### Scenario: Account dispatch does not create an off-grid run
- **WHEN** an account operation is prepared or dispatched
- **THEN** no Phase 7-specific Inventory run or scheduler request is created

### Requirement: Account mutation dispatch SHALL require current Inventory monitoring eligibility

Before native mutation dispatch, Control MUST use the existing Node-first lock graph to require active Node lifecycle, current non-cancelled monitoring activation, `management_account_inventory_read` capability and a matching active Provider policy. Monitoring ineligibility MUST return `node_monitoring_ineligible`; a missing or inactive matching Provider policy MUST return `unsupported_provider`. For an accepted `prepared` operation either condition terminalizes `prepared -> failed` with the applicable stable error and receipt, with zero native mutation. Control MUST NOT require a Relay-specific Node mutation capability or contract-discovery endpoint, automatically enable monitoring or bypass policy. Deployment compatibility evidence SHALL pin CLIProxyAPI upstream v7.3.2 commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`.

#### Scenario: Monitoring Disable commits first
- **WHEN** Monitoring Disable wins the Node lock before account dispatch authorization
- **THEN** account dispatch re-reads monitoring ineligible and sends zero native request

# Deployment Profile Specification

## Requirement: Local and Production SHALL share Control initialization

Local and Production SHALL use:

/usr/local/bin/relay-control-init

as the deployment initialization owner.

## Requirement: Driver catalog SHALL be deployment-owned

Driver catalog and capabilities SHALL be initialized by deployment initialization.

Administrators SHALL NOT manually register Drivers.

## Requirement: Product provisioning SHALL remain manual

Deployment SHALL NOT automatically:
- create administrators;
- enroll Gateway;
- enroll Node;
- enable monitoring.

## Requirement: Standard profile SHALL enable observation workers

Standard profiles SHALL enable:
- Inventory Poll
- Gateway Directory
- Request Quality
- Node usage statistics

## Requirement: Lifecycle SHALL not expose a separate current switch

The current deployment profile SHALL NOT require:

CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED

## Requirement: Operational overrides SHALL remain available

Operators SHALL be able to explicitly disable operational workers.

## Requirement: Deployment readiness SHALL not require product provisioning

Deployment readiness does not require administrator or asset enrollment.

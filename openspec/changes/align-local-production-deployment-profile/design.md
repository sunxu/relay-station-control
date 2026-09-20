# Design: Align Local and Production Deployment Profile

## Initialization Model

Local and Production:

control-secret-init
    |
    v
relay-control-init
    |
    v
Control

relay-control-init owns deployment initialization.

## Product Provisioning Boundary

Manual administrator actions remain:
- administrator bootstrap
- Gateway enrollment
- Node enrollment
- credential configuration
- monitoring activation

## Standard Operational Profile

Deployment defaults:

CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=true
CONTROL_GATEWAY_DIRECTORY_ENABLED=true
CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED=true

DIRECTORY_ENABLED=true

usage-statistics-enabled=true

Application safe defaults may remain disabled.

## Lifecycle

CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED is not part of the current normal deployment surface.

Lifecycle follows Inventory Poll semantics.

## Readiness

Deployment readiness does not require:
- administrator bootstrap
- Gateway enrollment
- Node enrollment
- monitoring activation
- inventory data availability

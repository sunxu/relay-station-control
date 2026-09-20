# Runtime Acceptance Plan: Align Local and Production Deployment Profile

## Status

PLANNING

## Purpose

This document defines the runtime acceptance plan for:

`align-local-production-deployment-profile`

The purpose is to verify that Local and Production deployment profiles:

-   share the same deployment initialization model;
-   preserve administrator-owned product boundaries;
-   enable standard operational capabilities correctly;
-   converge correctly after manual product provisioning.

This document defines validation strategy only.

It does not claim implementation completion or acceptance success.

------------------------------------------------------------------------

# 1. Acceptance Principles

## 1.1 Deployment readiness and product readiness are separate

Deployment readiness verifies:

-   deployment infrastructure;
-   deployment initialization;
-   service health.

Product readiness requires later administrator actions:

-   administrator bootstrap;
-   Gateway enrollment;
-   Node enrollment;
-   credential configuration;
-   monitoring activation.

Deployment MUST NOT automatically create product state.

## 1.2 Acceptance environment isolation

Runtime acceptance MUST use an isolated environment:

-   independent Compose project;
-   independent Docker volumes;
-   independent runtime directory;
-   independent ports;
-   independent secrets.

Acceptance MUST NOT use:

-   existing developer runtime;
-   existing production data;
-   existing shared volumes;
-   existing credentials.

------------------------------------------------------------------------

# 2. Stage 1 --- Fresh Deployment

Verify:

-   control-secret-init completed;
-   relay-control-init completed;
-   migrations completed;
-   environment initialized;
-   Driver catalog exists;
-   Driver capabilities exist;
-   initial Provider Policy exists;
-   Control/Gateway/Node/PostgreSQL/Redis healthy.

Driver contract:

    node_type:
    cliproxyapi

    contract:
    cliproxyapi.auth-files.v1

    capabilities:
    management_account_inventory_read
    management_health_read

------------------------------------------------------------------------

# 3. Stage 2 --- Repeat Deployment

Run deployment again without deleting persistent state.

Verify:

-   secrets preserved;
-   environment preserved;
-   Driver catalog not duplicated;
-   Driver capabilities not duplicated;
-   usable Provider Policy not overwritten;
-   services remain healthy.

------------------------------------------------------------------------

# 4. Stage 3 --- Deployment Ready Without Product Assets

Before manual provisioning:

Expected:

-   administrator not created;
-   Gateway not enrolled;
-   Node not enrolled;
-   monitoring not enabled.

Deployment should still reach:

    DEPLOYMENT_READY

Deployment MUST NOT:

-   automatically create Gateway;
-   automatically create Node;
-   automatically enable monitoring;
-   automatically import credentials.

------------------------------------------------------------------------

# 5. Stage 4 --- Manual Administrator Provisioning

Manual actions remain:

-   first administrator bootstrap;
-   Gateway enrollment;
-   Gateway credential configuration;
-   Node enrollment;
-   Node credential configuration;
-   monitoring activation.

------------------------------------------------------------------------

# 6. Stage 5 --- Worker Convergence

After product state exists, workers should converge without deployment
configuration changes or Control restart.

## Inventory Poll

Prerequisites:

-   Node enrolled;
-   Node active;
-   monitoring enabled;
-   required capabilities available;
-   Provider Policy usable.

Verify:

-   poll starts;
-   poll completes;
-   account_inventory becomes available.

## Gateway Directory

Prerequisites:

-   Gateway Directory source enabled;
-   Gateway enrolled;
-   Directory credential configured.

Verify:

-   Control Directory consumer runs;
-   observations appear;
-   freshness advances.

## Request Quality

Prerequisites:

-   Node usage statistics enabled;
-   eligible monitored Node;
-   Control Request Quality enabled.

Verify:

-   usage observations collected;
-   single destructive usage queue consumer remains enforced.

------------------------------------------------------------------------

# 7. Operational Override Validation

Verify these controls remain available:

    CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED

    CONTROL_GATEWAY_DIRECTORY_ENABLED

    CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED

    DIRECTORY_ENABLED

For each:

-   true enables capability;
-   false disables capability.

Disabling a worker MUST NOT mutate administrator-owned product state.

------------------------------------------------------------------------

# 8. Security Validation

Verify:

-   no credentials committed in Compose;
-   no secrets printed in logs;
-   no direct SQL business asset provisioning;
-   no automatic credential replacement.

Directory token:

    DIRECTORY_CURRENT_TOKEN

remains a deployment-owned secret input.

It does not automatically enroll Gateway into Control.

------------------------------------------------------------------------

# 9. Regression Validation

Verify unchanged boundaries:

-   first administrator bootstrap remains manual;
-   Gateway enrollment remains manual;
-   Node enrollment remains manual;
-   monitoring activation remains manual;
-   deploy-v0.9.1 remains immutable.

No validation step may require:

-   deployment tag change;
-   database migration;
-   Control runtime behavior change;
-   Gateway source change;
-   Node source change.

------------------------------------------------------------------------

# 10. Acceptance Result

Final result:

    DEPLOYMENT_PROFILE_RUNTIME_ACCEPTANCE =
    PASS / FAIL

Required evidence:

-   fresh deployment PASS;
-   repeat deployment PASS;
-   manual provisioning boundary PASS;
-   worker convergence PASS;
-   override behavior PASS;
-   security validation PASS.

Only after evidence is collected may:

    align-local-production-deployment-profile

be archived.

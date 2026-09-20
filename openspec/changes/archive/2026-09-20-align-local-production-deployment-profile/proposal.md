# Proposal: Align Local and Production Deployment Profile

## Why

Local Control initialization and the standard operational profile must be
validated as one deployment contract while administrator and asset provisioning
remain explicit manual operations.

## Status
ACTIVE

## Summary
Align Relay Station Local and Production deployment profiles so both use the same deployment initialization model and standard operational profile.

This change does not automate administrator-owned product provisioning.

## Goals
- Local deployment uses the same relay-control-init path as Production.
- Remove Local host-owned migration/environment initialization.
- Enable standard observation workers by default.
- Preserve manual product ownership boundaries.

## Ownership

Deployment-owned:
- migrations
- environment initialization
- Driver catalog
- Driver capabilities
- initial Provider Policy

Administrator-owned:
- first administrator bootstrap
- Gateway enrollment
- Node enrollment
- credential entry
- monitoring activation

## Non Goals
- No automatic administrator creation.
- No automatic Gateway or Node enrollment.
- No automatic monitoring enable.
- No Control runtime semantic changes.
- No Gateway or Node source changes.
- No database migration.
- No deployment release creation.

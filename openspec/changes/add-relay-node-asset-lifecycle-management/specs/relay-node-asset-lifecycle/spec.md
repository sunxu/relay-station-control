## ADDED Requirements

### Requirement: Node lifecycle planning depends on shared foundation

Relay Node lifecycle implementation MUST reuse the approved shared command receipt, revision, lifecycle reason and compatibility contracts from `add-gateway-asset-lifecycle-management`. This skeleton authorizes no implementation.

#### Scenario: Dependency not approved

- **WHEN** the shared Gateway foundation has not passed its review
- **THEN** Node lifecycle planning remains deferred and no Node migration or runtime change is applied

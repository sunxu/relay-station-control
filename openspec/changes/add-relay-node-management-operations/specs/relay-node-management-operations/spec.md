## ADDED Requirements

### Requirement: Node operations planning boundary

Node management operations MUST be planned only after the Node lifecycle change is approved and MUST reuse the shared command foundation. The product operations scope SHALL be limited to fixed `GET /healthz` observation and immediate monitoring actions; this skeleton authorizes no implementation.

#### Scenario: Operations dependency not approved

- **WHEN** the Node lifecycle foundation has not passed its review
- **THEN** operations planning remains deferred and no API, UI or runtime change is applied

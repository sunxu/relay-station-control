## ADDED Requirements

### Requirement: Durable selector policy

E2E tests MUST use stable test IDs, stable business identity attributes, or stable accessibility roles/names. They MUST NOT use presentation classes, positional selectors, XPath/DOM hierarchy, or incidental copy as durable identity.

#### Scenario: Selector inventory precedes implementation

- **WHEN** a selector hardening change is proposed
- **THEN** the affected selectors are classified before UI or E2E edits, and the minimal required UI attributes are listed for review.

### Requirement: Product behavior neutrality

Selector hardening MUST NOT change UI behavior, API behavior, business semantics, or production data contracts.

#### Scenario: Stable identity attributes

- **WHEN** a reviewed selector is added
- **THEN** it identifies the same existing element or business entity without changing the rendered behavior or interaction outcome.

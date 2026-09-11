# e2e-selector-policy Specification

## Purpose
定义 E2E 稳定 selector 契约，通过稳定 test ID、业务实体 identity attribute 和稳定 accessibility contract，避免测试依赖 presentation class、DOM 层级、位置或 incidental copy，同时保持 UI、API 和业务行为不变。

## Requirements

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

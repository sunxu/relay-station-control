# CI validation specification

## ADDED Requirements

### Requirement: correctness evidence MUST remain owned

任何旧 correctness job 被删除或迁移前，replacement owner MUST 已定义，且
replacement proof MUST 实际运行并 PASS。不得通过删除失败 coverage 获得绿色。

#### Scenario: migrated evidence has a passing replacement

- **WHEN** a correctness check moves to another job or workflow
- **THEN** the new owner runs the same contract and records a passing proof before the old owner is removed

### Requirement: stale acceptance inventory MUST fail actionably

Required-test inventory MUST 引用当前真实 owning test。测试重命名或迁移后，
inventory MUST 同步更新；stale test name MUST 被分类为 validation maintenance
failure，而不是 product regression。

#### Scenario: owning test is renamed

- **WHEN** the old exact test name no longer exists
- **THEN** the inventory identifies the replacement or reports a coverage gap with the current owner boundary

### Requirement: acceptance failures MUST expose actionable classification

Acceptance failure MUST 暴露 `phase`、`reason` 以及 `checkpoint` 或
`failed_test`。输出 MUST sanitized，不得包含 secret、token、credential 或
raw sensitive response。

#### Scenario: snapshot preparation fails

- **WHEN** a snapshot preparation boundary fails
- **THEN** the earliest failed checkpoint is printed with a fixed sanitized reason

### Requirement: correctness and capacity MUST be separated

Correctness 与 capacity/performance MUST 由不同 gate owner 表达。hosted runner
性能波动 MUST NOT 被归类为普通 correctness regression。

#### Scenario: capacity is slow on a hosted runner

- **WHEN** a scale scenario exceeds its bounded performance contract
- **THEN** the capacity owner reports scale and bounded metrics without weakening the main correctness gate

### Requirement: upstream compatibility MUST be separately owned

Pinned official CLIProxyAPI validation MUST 属于 upstream compatibility evidence，
不得描述为 current production Node artifact acceptance。

#### Scenario: pinned upstream validation runs

- **WHEN** the compatibility workflow is dispatched or triggered by a relevant change
- **THEN** it reports the pinned upstream identity and compatibility result separately from production artifact evidence

### Requirement: deployment publication MUST be tag driven

正式 deployment image publication MUST 由符合
`deploy-v0.<PHASE>.<REVISION>` 的 immutable tag 驱动。manual dispatch MUST NOT
隐式发布 `:main` 或其他 mutable deployment tag。

#### Scenario: a non-release ref requests publication

- **WHEN** a workflow is run from a branch or an invalid tag
- **THEN** no deployment tag is published and the workflow fails closed or does not run

### Requirement: change classification MUST fail closed

Change classification MUST 覆盖 docs、web、Go、database、acceptance/workflow
类别；不确定影响 MUST 扩大验证。`CI required` MUST fail on unexpected skip,
failure 或 cancelled state，并接受符合 contract 的合法 skip。

#### Scenario: a shared acceptance helper changes

- **WHEN** a shared acceptance infrastructure path changes
- **THEN** classifier requires the owning correctness acceptance set rather than skipping it

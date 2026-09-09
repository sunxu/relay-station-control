# antigravity-account-availability Specification

## MODIFIED Requirements

### Requirement: Recovery SHALL require newer independent evidence

对于 `TOKEN_INVALID`、`ACCOUNT_BLOCKED`、`FORBIDDEN` 的 confirmed ACTIVE occurrence，任意数量的 fresh/complete `file_active` Inventory observation MUST NOT resolve occurrence。runtime-only `file_error`、`file_unavailable`、`auth_reason=other` MUST NOT independently advance ACTIVE occurrence 的 `last_failure_at`。唯一新增允许的 recovery evidence 是真实成功请求；恢复 MUST require `success = true` AND `success.occurred_at > occurrence.last_failure_at`。其它既有非 recovery-evidence guards 保持不变，包括 conflict guards、confirmation、ACTIVE request-failure persistence、request_id/event_hash、FORBIDDEN confirmation、recurrence、concurrency、SERIALIZABLE 与 request ingestion。

#### Scenario: File-active observations do not recover faults
- **WHEN** confirmed TOKEN_INVALID、ACCOUNT_BLOCKED 或 FORBIDDEN 后出现任意数量 fresh complete qualified `file_active` observation，且没有合格成功请求
- **THEN** occurrence 保持 ACTIVE，不得变为 RESOLVED

#### Scenario: Success after failure
- **WHEN** 既有非 recovery guards 满足，且 success=true 严格晚于 occurrence.last_failure_at
- **THEN** 对应 occurrence ACTIVE→RESOLVED

#### Scenario: Equal timestamp success is insufficient
- **WHEN** success.occurred_at 等于 occurrence.last_failure_at
- **THEN** occurrence 保持 ACTIVE

#### Scenario: No traffic recovery
- **WHEN** 故障后没有成功请求，仅有连续或重复 fresh complete `file_active` observation
- **THEN** occurrence 保持 ACTIVE，不执行无流量恢复

#### Scenario: Recurrence and late evidence
- **WHEN** 已 RESOLVED 后收到迟到或重复失败
- **THEN** 既有 recurrence、late-evidence 与 occurrence identity 行为保持不变；本 change 不重新定义其 confirmation

#### Scenario: Runtime-only evidence does not advance failure watermark
- **WHEN** ACTIVE occurrence 后仅出现 file_error、file_unavailable 或 auth_reason=other
- **THEN** last_failure_at 不变，runtime-only evidence 不阻止满足既有 guards 的成功恢复

## ADDED Requirements

### Requirement: Current projection SHALL remain separate from occurrence lifecycle

ACTIVE TOKEN_INVALID 或 ACCOUNT_BLOCKED 在 Inventory fresh+complete、Provider health qualified、runtime=file_active 且无成功恢复时，current MUST 分别为 TOKEN_INVALID 或 ACCOUNT_BLOCKED；不得 AVAILABLE 或仅因 file_active 降为 UNKNOWN。ACTIVE FORBIDDEN 且 runtime active 时 current MUST 为 UNKNOWN/pending_confirmation。任一 ACTIVE fault 遇 stale/incomplete 时 current MUST 为 UNKNOWN，stale/incomplete 本身不 resolve occurrence 且不作为 recovery evidence；DISABLED 时 current MUST 为 DISABLED，DISABLED 本身不 resolve occurrence 且不作为 recovery evidence。若独立存在满足既有 guards 的 qualified success，occurrence lifecycle 仍按 success recovery rule 处理。无 ACTIVE fault 时既有 AVAILABLE baseline 保持不变。

#### Scenario: Active TOKEN_INVALID with fresh file-active
- **WHEN** ACTIVE TOKEN_INVALID、Inventory fresh+complete、Provider health qualified、runtime=file_active 且无成功恢复
- **THEN** current 为 TOKEN_INVALID，occurrence 保持 ACTIVE

#### Scenario: Active ACCOUNT_BLOCKED with fresh file-active
- **WHEN** ACTIVE ACCOUNT_BLOCKED、Inventory fresh+complete、Provider health qualified、runtime=file_active 且无成功恢复
- **THEN** current 为 ACCOUNT_BLOCKED，occurrence 保持 ACTIVE

#### Scenario: Active FORBIDDEN with active runtime
- **WHEN** ACTIVE FORBIDDEN 且 runtime active
- **THEN** current 为 UNKNOWN/pending_confirmation，occurrence 保持 ACTIVE

#### Scenario: Active fault with stale or disabled inventory
- **WHEN** ACTIVE fault 的 Inventory stale/incomplete，或合格观察为 DISABLED
- **THEN** stale/incomplete 时 current 为 UNKNOWN，DISABLED 时 current 为 DISABLED；occurrence lifecycle 不变且不作为恢复证据

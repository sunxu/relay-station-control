## MODIFIED Requirements

### Requirement: Monitoring cancellation SHALL be durable and cancellation-aware

Monitoring activation history MUST retain `effective_from/effective_to` and schedule metadata.
Future rows MAY be cancelled exactly once with non-NULL `cancelled_at`, `cancelled_by` and
`cancel_reason`; all three are NULL together or non-NULL together. This change's first version
fixes `cancel_reason IN ('node_retired','node_replaced','administrator_disable')`, with `cancelled_by` a
`control_admin_users.admin_id` FK (`ON UPDATE RESTRICT ON DELETE RESTRICT`); the existing current
close `end_reason` taxonomy (`deployment_disable|scheduled_disable|reconciliation`, its own
text `end_actor`) is a separate column family and MUST NOT be merged into `cancel_reason`,
because those existing reasons use system/deployment actor semantics incompatible with an admin
UUID FK. Stage 3 administrator Disable MUST use `administrator_disable` with the same cancellation columns; it MUST NOT create a second cancellation persistence. Current intervals MUST close with
`effective_to=boundary` and the existing `end_reason/end_actor/end_recorded_at` metadata (the
existing `end_reason` allowlist additively gains `node_retired|node_replaced|administrator_disable`; `end_actor`
remains the baseline text column, and Node lifecycle writes the authenticated canonical
`actor_admin_id` UUID string into it without restructuring the actor schema), not cancellation
metadata. Cancelled future rows MUST never become current, eligible or expected slots.
Stage 3 administrator Disable（包括already-disabled no-op）MUST以其immutable command receipt建立
per-Node race fence，使该Disable commit前形成但尚未提交activation的scheduled intent conflict；
Disable commit后形成的新intent不受该旧boundary永久阻止，因此这不是durable disabled latch。
同一Node的Disable receipts MUST在Node lock serialization下分配严格递增的`committed_at`，latest
MUST只按该字段决定；`command_id`不得承担时间顺序。Node Disable receipts MUST NOT UPDATE、
DELETE或TRUNCATE，支持operational scheduling的部署MUST NOT prune latest fence receipt。

#### Scenario: Future cancellation
- **WHEN** a future activation is cancelled for `node_retired` or `node_replaced`
- **THEN** the row remains, original schedule fields remain unchanged, cancellation metadata is
  durable and its generated `active_range` is exactly `'empty'::tstzrange`

#### Scenario: Cancellation immutability
- **WHEN** a caller uncancels, changes cancellation metadata, cancels a current/past row, or
  deletes/truncates activation history
- **THEN** the database rejects the operation

#### Scenario: Administrator Disable cancellation
- **WHEN** Stage 3的Disable在同一DB boundary取消所有future activation
- **THEN** cancel_reason=administrator_disable、cancelled_by=admin UUID、cancelled_at=boundary，原schedule字段和existing lifecycle cancellation语义保留，active_range永久为空

#### Scenario: Administrator Disable no-op race fence
- **WHEN** Stage 3的Disable没有发现current或future activation并返回already_disabled
- **THEN** 不改变activation或`node_generation`，但新的immutable Disable receipt仍提交race fence，使该Disable之前形成而尚未提交的scheduled intent conflict；该fence不阻止Disable commit后新形成的intent

## Design

### Transaction boundary

Admission 使用短 PostgreSQL transaction：先锁 `relay_node_assets` Node row，再锁 command/operation identity，读取 operation。若 operation 已不是 `prepared`，提交并返回当前状态；不得将新的 eligibility failure 覆盖已推进状态。prepared 时在同一 transaction 内重新检查 lifecycle、当前未取消 monitoring、Node capability、当前 Provider policy 和 same-account blocker，成功后仅提交 `prepared -> dispatched` 或 `prepared -> remote_noop`。事务提交后才允许 native HTTP。

### Failure and replay

确定性 eligibility failure 在 accepted operation 上原子写入 `failed`、audit 和 immutable receipt；不发 native 请求。并发 no-op 只有一个调用能 terminalize；其它调用在锁后观察已提交 terminal state 并返回它。`dispatched` / `outcome_unknown` 仍不重新解析 Node、不做 snapshot、不 admission、不 redispatch。

### Policy and lifecycle

请求 Provider 从 canonical account key 得出。policy 必须与 Node 的 type/contract 相符，且 provider 在当前 active policy 中；out-of-scope 或缺失映射为 `unsupported_provider`。monitoring 缺失或不在当前未取消区间映射为 `node_monitoring_ineligible`，缺少 capability 映射为 `node_management_unavailable`，非 active lifecycle 映射为 `node_retired`。Retire/Replace 保持 Node-first blocker 检查，API 对该 blocker 返回 `account_operation_in_progress`。

### Security and boundaries

不新增工作流、租约、分布式锁或状态；复用现有 advisory/same-account serialization 与 activation graph。任何数据库锁都在 native HTTP 前释放。历史 migrations 00039/00040/00042/00049 不修改；必要 SQL 使用新的 forward-only migration。Browser 不承担并发和 exact native at-most-once 证明。

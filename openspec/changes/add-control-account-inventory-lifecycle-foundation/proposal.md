## Why

阶段 2 已能把完整 runtime Provider 观察原子提升为不可变快照和当前来源，但 Control 尚不能回答“账号当前是否存在、是否连续缺失或是否已退出监控范围”。现在需要先把已提升快照转换为受数据库保护的账号生命周期真相，后续页面、告警和历史汇总才能消费稳定状态，而无需重新请求 Node 或解释原始响应。

## What Changes

- 新增 `account_inventory` 当前账号生命周期模型，以 `(instance_id, account_key)` 唯一保存标准化身份、最近完整基础状态、首次/末次出现时间、连续缺失次数、`present|suspected_missing|missing|out_of_scope`、对应起始时间和可空来源 poll 指针。
- 扩展现有 fenced finalize：只有所属 Provider 的完整 runtime 快照实际 `promotion_applied=true` 时才推进生命周期；本轮出现的账号写为 `present` 并清零缺失，未出现的既有 active 账号第一次进入 `suspected_missing`、连续第二次进入 `missing`。生命周期、snapshot items、Provider 当前指针、promotion 标记和 poll finalized 必须同事务提交或回滚。
- 明确 transport/contract 失败、disk fallback、Provider 不完整、`policy_changed`、`stale_poll`、abandoned 槽和 Gateway/Compose 状态均不得增加或清零缺失次数；旧槽、旧 fencing 和提交未知恢复不得倒退或重复推进状态。
- 扩展受审计 Provider 策略切换事务：Provider 从 active 移入已登记 out-of-scope 集时，原子把 Provider 状态和现有账号转为 `out_of_scope`；重新加入 active 后，只有下一次完整且已提升的 runtime 快照中实际出现的账号恢复 `present`，未出现旧账号继续保持 `out_of_scope`。
- Migration 不从历史快照猜测当前生命周期，也不把已有 Provider 指针回填为账号状态；部署后的下一次合格 promotion 建立初始 `present` 基线，之后才允许累计 missing。
- 增加内部 Store 读取边界、封闭生命周期指标/日志维度和脱敏验收证据，供后续 API/UI 与告警 change 使用；本 change 不新增产品 OpenAPI 或 React 页面。
- 新增 lifecycle Runbook，覆盖首次基线、连续缺失、恢复、策略移出/重新加入、数据库故障、应用回滚和 forward Migration 保留。
- 不新增 Node、Gateway 或模型数据面请求，不实现账号删除/保留清理、HMAC `account_id`、10 分钟/1 小时趋势、告警路由、日级覆盖率、摘要、历史压缩或跨 Node 重复检测。

## Capabilities

### New Capabilities

- `account-inventory-lifecycle`: 定义当前账号状态、连续完整快照驱动的 missing 转换、Provider out-of-scope 迁移、恢复、单调性、敏感身份保护和内部读取边界。

### Modified Capabilities

- `account-inventory-snapshot`: 将完整 Provider promotion 的原子边界扩展到账号生命周期，并允许受保护的 `account_inventory` 列保存当前身份和来源；移除“snapshot foundation 永不推进生命周期”的旧边界。
- `account-inventory-poll-run`: 扩展 fenced finalize 的全有或全无结果，使合格 promotion 同时推进当前账号生命周期，而失败、降级和执行恢复仍不得产生额外 Node 请求或部分状态。

## Impact

- **阶段与结果**：进入阶段 3 的账号生命周期 foundation；运维人员尚无新页面，但数据库能够可靠区分当前出现、首次缺失、连续缺失和退出监控范围，为下一 change 的账号查询与告警提供唯一真相源。
- **仓库**：只修改 `control`。`ops` 系统设计 v1.0 第 9.4、9.6、12.1、21、23、24.1 节和 ADR-0001 是输入真相源；不修改 `ops`、Gateway 或 Node 产品代码。
- **Migration/sqlc**：新增一个 additive forward Goose Migration，创建受保护生命周期表并扩展 Provider 状态、策略切换和 fenced finalize；更新 sqlc 查询与 Store adapter。普通应用回滚保留 schema 和已形成状态，生产不执行 destructive down；受保护 down 只允许新表为空且没有后续依赖的全新环境。
- **OpenAPI/生成客户端/UI**：本 change 不修改 `api/openapi.yaml`，不生成新客户端，不新增账号页面、导出或人工状态操作。后续只读查询 change 必须另行定义认证、分页、筛选和 email 展示边界。
- **指标/日志/审计**：只允许封闭 lifecycle/transition reason 与受控 instance/provider 维度；email、account key、poll/policy ID、版本/提交、endpoint、Secret 和原始错误不得进入普通日志或标签。Provider 范围变更继续使用实名管理员、原因和不可变审计；本 change 不新增告警发送链路。
- **兼容性与数据面**：现有 API/UI、Driver HTTP 契约和调度请求数不变。Control/PostgreSQL/lifecycle 处理停止只暂停状态推进，不影响 Gateway 或 Relay Node 已有流量。
- **安全与回滚**：标准化 email/account key 只允许进入受保护 snapshot/duplicate/lifecycle 列；运行时角色只能通过有界 finalize 和内部只读函数访问。回滚旧应用后停止新生命周期推进并保留已提交状态，禁止用旧二进制删除或重算状态。

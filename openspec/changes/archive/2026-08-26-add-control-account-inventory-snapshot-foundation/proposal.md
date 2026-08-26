## Why

Control 已能按 PostgreSQL UTC 五分钟槽安全调用现有 CLIProxyAPI 只读 Driver，并持久化 transport、contract、inventory mode 与 Provider 完整性聚合证据；但 poll finalize 会主动丢弃内存中的账号 DTO，也没有账号快照表、节点内重复证据或 Provider 当前快照指针。因此，完整的 runtime 观察仍无法成为可审计、可分页且不依赖 Node 在线的数据库快照，策略并发切换时也没有独立于 `provider_snapshot_complete` 的 promotion 结果。

本 change 建立账号快照与 Provider 级原子 promotion 基础：按固定规则标准化 provider/email 并生成 `account_key`，在 finalize 前预分组重复账号，在同一 fenced PostgreSQL 事务中锁定当前策略 binding、保存聚合 poll 证据、节点内重复证据、完整 Provider 的 snapshot items、Provider 当前指针和 promotion 结果。它只形成不可变快照与当前来源指针，不推进账号 missing/out-of-scope 生命周期。

## What Changes

- 新增 `account_inventory_snapshot_items`、`account_inventory_poll_duplicates` 与 `account_inventory_provider_states`，使用 poll run、Node、Provider 和 `account_key` 的显式唯一键与外键保存完整 runtime 快照、节点内重复聚合及各 Provider 当前来源。
- 扩展 poll run/provider result：增加封闭 `promotion_applied`、`promotion_skipped_reason`，并把 provider snapshot complete 与实际 promotion 明确分离。
- 在 Control 内存中按确定性规则标准化 provider/email，生成 `account_key = normalized_provider + ":" + normalized_email`；不进行 plus-address、域名别名、Unicode 等价或启发式账号合并。
- 在网络响应离开 Driver 后、数据库事务开始前按 `(provider, account_key)` 分组。节点内重复只保存 `account_key` 与 `occurrence_count` 聚合证据；重复所属 Provider 标记不完整，不任意选择记录或依靠唯一约束吞掉冲突。
- 扩展 fenced finalize：先写 poll/provider/duplicate 证据，再锁定当前 `(node_type, driver_contract_version)` Provider policy binding 并与 poll pinned policy 比较。策略已变化时只保存证据并记录 `policy_changed`，不得用新策略重解释或提升旧响应。
- 策略未变化时，各 active Provider 独立 promotion：只为 `provider_snapshot_complete=true` 且 `inventory_mode=runtime` 的 Provider 写入去重 snapshot items、更新其当前指针并设置 `promotion_applied=true`；其他 Provider 保留旧指针。
- 对 transport/contract 失败、disk fallback、缺 provider/email、节点内重复、unsupported/out-of-scope、空但完整 Provider、数据库故障、lease/fencing 丢失和策略切换增加事务、恢复和敏感数据负向验收。
- 不修改 Gateway/Node，不增加任何管理请求，不实现账号生命周期、连续缺失、out-of-scope 状态迁移、历史压缩、覆盖率、告警、产品 OpenAPI 或 React 页面。

## Capabilities

### New Capabilities

- `account-inventory-snapshot`: 定义账号标准化、`account_key`、节点内重复预分组、不可变 snapshot items、Provider 当前指针及策略一致的原子 promotion 边界。

### Modified Capabilities

- `account-inventory-poll-run`: 将现有聚合 finalize 扩展为策略 binding 受锁、Provider 独立且与 snapshot/pointer/promotion 同事务的 finalize；保留原 UTC 槽、lease/fencing、无历史补采和 Node 失败 finalized 语义。

## Impact

- **阶段与结果**：继续阶段 2；完整 runtime Provider 观察可以形成 PostgreSQL 快照和稳定当前来源，后续生命周期 change 可只消费已提升快照，不重新请求 Node 或解析原始响应。
- **仓库**：只修改 `control`。`ops` 系统设计 v1.0 第 9.6、12、13.4、20.3、21.1、23、24.2 节和 ADR-0001 是输入真相源，不修改 `ops`、Gateway 或 Node 产品代码。
- **Migration/sqlc**：新增一个 additive Goose Migration，扩展现有 poll 表并创建三张快照表、受控 finalize 函数、索引、约束和运行时最小权限；新增 sqlc 写入/读取与 PostgreSQL 集成测试。
- **事务与并发**：所有 promotion 写入必须继续受 poll lease/fencing 保护；策略 binding 行锁、版本比较、snapshot items、Provider 指针和 promotion 标记在同一事务中提交或回滚。
- **数据模型**：`account_key` 是标准化 provider/email 的环境数据库内业务键，包含账号身份信息，不得进入非受控日志、指标或错误。未来 Prometheus `account_id` 才使用环境独立 Secret 的 HMAC-SHA256，两者不得混用。
- **安全**：允许只在快照表保存标准化 provider/email、`account_key` 和 Driver 字段白名单；继续禁止 endpoint/IP、Secret/Management Key、header/body、原始错误、无法识别记录内容、未知 JSON 字段和响应原文落库。
- **兼容性**：现有 API/UI 不变；当前 poll 调度、请求次数、超时、容量和真实 Node 10 秒冷却边界不变。完整 Provider 可独立推进，异常 Provider 不阻止同 Node 其他完整 Provider。
- **指标/日志**：只新增 Provider promotion 成功/跳过的封闭指标或日志维度；禁止 email、`account_key`、poll ID、policy ID 和原始错误成为标签或普通日志字段。
- **回滚**：应用回滚停止新 promotion 并保留已形成的快照与当前指针；普通环境只允许保留 forward Migration。只有新表为空、没有 promotion 标记且无后续依赖的全新环境才允许受保护 down。
- **数据面隔离**：复用现有每槽一次固定只读 GET，不增加 Probe、Gateway、模型数据面、管理写请求或任意目标访问。Control/PostgreSQL 停止只暂停采集与 promotion，不影响数据面。

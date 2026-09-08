# 本地源码调研与阻塞证据

日期：2026-09-08。最初调研状态为BLOCKED；后续Architecture Review允许unresolved，已解除，进入正式实现。原源码发现保留如下。

## CPA Manager Plus（只读）

根目录 `/Users/keedle/workspace/CPA-Manager-Plus`，本地HEAD `1ae656c8`（v1.12.10 promotion），工作树干净。不查询GitHub、不把远端版本当依据。

| 文件（相对此根） | 实际逻辑 |
|---|---|
| apps/manager-server/internal/httpqueue/client.go:46-136 | Pop：GET usage-queue?count=N，Bearer，context，30s client timeout，数组内object/string，404/405/501 unsupported |
| apps/manager-server/internal/httpqueue/client_test.go:12-74 | HTTP queue测试参考 |
| apps/manager-server/internal/usage/event.go:504-640 | NormalizeRaw对象解析、别名、时间、latency、failed、optional snapshots与hash |
| apps/manager-server/internal/usage/event.go:790-817 | failed优先，再success/ok、status/error；nested fail和顶层字段 |
| apps/manager-server/internal/usage/event.go:944-961 | 内容hash包含request_id、timestamp、endpoint、model、auth_index、source hash、token、failed、latency；不是request_id唯一性证明 |
| apps/manager-server/internal/usage/event.go:993-1007 | FailSummaryFromBody；本轮不移植额外敏感信息治理 |
| apps/manager-server/internal/collector/collector.go:348-453 | HTTP轮询、normalize、写入；其SQLite、dead letter和后续业务不整体复制 |
| apps/manager-server/internal/collector/collector.go:510-575 | 入库前enrichAccountSnapshots，不能误认raw queue原生提供全部snapshot |
| apps/manager-server/internal/collector/auth_snapshot.go:129-185 | auth-files lookup；重复auth_index标ambiguous；account/label/file展示fallback不等于Relay canonical email |
| apps/manager-server/internal/usage/response_headers.go:997-1017 | 部分auth/rate-limit识别；没有可直接复用的完整五类classifier |
| LICENSE:1-16 | MIT，Copyright (c) 2026 Seakee；本轮未复制生产源码 |

## Control（只读现有实现）

| 文件 | 结论 |
|---|---|
| internal/drivers/cliproxyapi/transport.go:163-226 | 现有health/auth-files固定路径管理transport，可增加小型queue operation，不必新框架 |
| internal/drivers/cliproxyapi/driver.go:120及后续 | inventory管理认证/SecretResolver调用模式 |
| internal/drivers/cliproxyapi/parser.go:315 | inventory解析provider/email，丢弃auth_index而不是持久化lookup |
| internal/drivers/types.go:132 | AccountObservation无auth_index |
| internal/inventorypoll/projection.go:113-138 | trim/lowercase provider/email，account_key=provider+":"+email，不自行做RFC/alias推断 |
| migrations/00007_account_inventory_lifecycle_foundation.sql:75 | account_inventory无auth_index |
| cmd/control/main.go:389-410 | worker/reconciler/inventory runtime共享shutdownContext |
| internal/history/retention.go | inventory lineage30天retention，不可改造成请求事件7天truth |
| ../ops/docs/adr/0001-control-technology-stack.md:12,203,207 | 单实例部署；多实例是未来评审项，不是本版增加scheduler理由 |

## Node 源码交叉证明（零修改）

- `../node-cliproxyapi/internal/api/server_management.go:84` 注册GET usage-queue。
- `../node-cliproxyapi/internal/api/handlers/management/usage.go:24-43` 先PopOldest再返回，无ACK/requeue。
- `../node-cliproxyapi/internal/redisqueue/plugin.go:91-92,147-148` 序列化source/auth_index。
- `../node-cliproxyapi/internal/runtime/executor/helps/usage_helpers.go:412-454` source可为OAuth邮箱、Vertex project或API key，不可普遍视作email。
- `../node-cliproxyapi/sdk/cliproxy/auth/types.go:555-581` OAuth AccountInfo使用metadata email；仅此路径可直接证明email来源。
- Node工作树已有`M AGENTS.md`，本轮没有修改此文件或其它Node文件。

## Architecture Review 变更

原调研正确证明Control不保存auth_index，且CPA完整Event含enrichment，不等同queue raw格式。最新Architecture Review不再要求每个事件归属账号：允许account_key=NULL，保留Node/Provider结果。该决定解除原blocker，不需要历史身份系统。

实现仅在当前auth-files显式provider/email唯一且无冲突时用auth_index解析；直接事件identity可独立解析；其它保留unresolved。CPA的label/file fallback不搬入canonical identity。AccountQuality排除NULL，NodeProviderQuality保留并返回unresolved_request_count。

## 实施验证

最终命令、fixture、结果和100k查询证据集中记录在planning-validation.md。生产改动仅Control；无CPA/Node修改，无push/deploy/archive，无真实本地Node queue消费。

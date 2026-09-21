# Phase 10 Dashboard Truth-Source Matrix

> 状态：Stage 0 FROZEN CANDIDATE。Dashboard 是只读 presentation；本表决定第一阶段哪些信息可以作为 Authoritative Summary，哪些只能作为 Navigation Summary。

| Candidate summary | Source API | Classification | Aggregation semantics | Window / denominator | Pagination completeness | Stage 2 decision |
|---|---|---|---|---|---|---|
| Control online / version | `GET /api/healthz` | Authoritative | 当前 Control 进程 health response | current observation / N/A | complete | 允许状态展示；不推导历史 uptime |
| Gateway asset counts | `GET /api/assets/gateways` -> `gateway_counts` | Authoritative | Store 返回 active / retired / total counts，独立于当前 page items | current registry / all Gateway assets | counts complete | 允许显示 registered/active/retired counts；不等于 Gateway health |
| Relay Node asset counts | `GET /api/assets/nodes` -> `node_counts` | Authoritative | Store 返回 active / retired / total counts，独立于当前 page items | current registry / all Node assets | counts complete | 允许显示 Node counts；不等于 monitoring health |
| Inventory poll capacity | `GET /api/account-inventory/poll-capacity` | Authoritative diagnostic | 当前槽位/配置的容量诊断 | endpoint-defined current slot / endpoint fields | complete response | 允许显示 ready/capacity-exceeded/disabled 与容量字段；不得称为“最近采集成功率” |
| Accounts / Inventory total | `POST /api/account-inventory/query` | Navigation-only | 单 Node、带 filters 的分页 query | request-scoped | incomplete: items + cursor, no global total | 不显示全局账号数；只提供 Accounts 入口/当前查询上下文 |
| Account quality success rate | per-node/account quality APIs | Navigation-only | 已有窗口只对指定 Node/account/provider query 有意义 | 15m / 1h 等 endpoint-defined | query-scoped | 不生成 Global Success Rate，不跨 Node 前端平均 |
| Durable Jobs count/trend | `GET /api/jobs` | Navigation-only | 分页 durable job list | filters / page | incomplete: items + next_cursor, no total | 不显示全局 job count/trend；可显示“查看持久任务” |
| Problems count/trend | `POST /api/problem-accounts/query` | Navigation-only | filters + cursor 的 confirmed problems page | request-scoped | incomplete: items + next_cursor, no total | 不显示“当前问题总数/趋势”；可显示 Problems 入口 |
| Gateway health | `GET /api/assets/gateways/{id}/health` | Explicit observation, not Dashboard aggregate | per-instance explicit probe | point-in-time | one target | Dashboard mount MUST NOT 自动 probe；可链接到 Gateway 管理/Monitoring |
| Node health | `GET /api/assets/nodes/{id}/health` | Explicit observation, not Dashboard aggregate | per-instance explicit probe | point-in-time | one target | Dashboard mount MUST NOT 自动 probe；可链接到 Relay Nodes/Monitoring |
| Directory freshness / binding | existing per-node topology/binding read APIs | Navigation-only | domain-specific current derived context | per Node/binding | not a global aggregate | 仅显示导航/selected context；不得包装成全局 Gateway routing health |
| Request Quality | existing per-node/provider/account query APIs | Navigation-only | domain query with explicit windows | endpoint-defined | query-scoped | 不构造 global rate/P95/trend |
| Global recent account operations | no list API | Unsupported | none | none | none | MUST NOT display; backend contract required |
| Last-week comparison | no approved aggregate API | Unsupported | none | none | none | MUST NOT display |
| Global alerts trend | no approved aggregate API | Unsupported | none | none | none | MUST NOT display |
| Global operation trend | no approved aggregate API | Unsupported | none | none | none | MUST NOT display |

## Rules

1. `items.length` MUST NOT 充当全局 count。
2. `next_cursor == null` 只证明该 query 的当前结果页链结束，不自动证明跨 Node / 全领域 aggregate。
3. Authoritative Summary MUST 使用 API 自身定义的完整 count/diagnostic，不在前端重新解释 denominator。
4. 显式 health/connection test 有网络成本和独立 audit/operation ownership；Dashboard mount 不触发。
5. 新增任何全局 rate/trend/comparison 先建立后端 aggregation contract 并更新 OpenSpec。

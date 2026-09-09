# Local source investigation

本轮只读本地源码，无GitHub检索，无远程Node/Google调用，无真实auth文件或Token读取。仅修改本change规划文档。

| 位置 | 已验证事实 | 设计影响 |
| --- | --- | --- |
| `internal/drivers/cliproxyapi/parser.go:157,323-435,501-536` | AccountObservation保存安全基础状态/计数/时间；未知字段包括status_message被丢弃；source=file属于runtime而非disk fallback；disabled优先，unavailable或未来retry其次 | 新增独立安全枚举，不能修改现有base status/eligibility；file模式证明需传递到持久安全投影 |
| `internal/drivers/cliproxyapi/usage_queue.go:86-134` | CurrentIdentities只返回auth_index/provider/email | 不能把身份lookup误认为完整runtime availability；不用它新增第二轮可用性采集 |
| `internal/requestquality/normalize.go:67-103,191-210` | 401/403/token_revoked/token_invalidated/account_deactivated压为auth；失败原文仅在normalize中；event hash沿用CPA | normalization边界细分安全枚举，不能从已存auth逆推401/403；不改hash |
| `internal/requestquality/types.go:12-24`、`internal/store/account_request_quality.go:19-31` | Event及DB wire无HTTP status/认证子原因 | nullable auth_failure_reason是必要additive字段，不新存raw或HTTP body |
| `migrations/00020_account_request_quality_events.sql:3-55` | node+hash PK、五类failure CHECK、insert-ignore、7天retention | 保留所有现有真相与清理；新版本writer兼容旧NULL字段 |
| `internal/inventorypoll/types.go:206-219` | SnapshotCandidate持久白名单不包含source/disabled/unavailable或认证子原因 | 必须显式规划安全projection贯穿driver/DTO/snapshot/current，不能只改Topology接口假装现存字段足够 |
| `internal/inventorypoll/reconciler.go:58`、`internal/inventorypoll/worker.go:191`、`cmd/control/cross_node_duplicate_ownership.go:12-38` | 既有startup/periodic/post-finalize lifecycle callback与有界timeout | 复用调用生命周期但隔离availability失败，不改变duplicate算法/lease |
| 本地 `~/workspace/CPA-Manager-Plus/apps/manager-server/internal/usage/event.go:19-85` | fail_status_code/fail_summary/request_id/event_hash/auth_index输入形态 | 仅参考normalize，不移植完整Event存储 |
| 同库 `internal/service/credentialpolicy/policy.go:37-113` | 存在account_deactivated、token_revoked/token_invalidated/invalid_grant及普通forbidden分支，但还带delete/reauth/auto-disable决策且使用宽泛Contains | 只参考安全标识符，不复制action/宽泛匹配，不证明所有Google文案均可识别 |

当前本地读取没有证明全部Antigravity版本都会返回结构化blocked code。设计只承认用户指定且fixture确认的精确白名单，未知文案归other，不根据CPA自动策略推断Google账号已封禁。明确错误枚举的合成fixture与生产原文零泄露测试是实施验收门槛。

已有 Inventory provider state 的 health_degraded 与 duplicate ownership eligibility 独立。本availability可将degraded视为UNKNOWN，但不能反向改变duplicate eligibility。已有HTTP usage queue不增加consumer，no-ACK损失窗口继续写入runbook。

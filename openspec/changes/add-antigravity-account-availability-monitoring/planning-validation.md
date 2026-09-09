# Planning validation

## Current phase

实现阶段已完成；本轮进行Implementation Final Review。已写入additive migration、只读API/UI与生命周期接线；尚未commit、push、deploy或archive。原A1已由用户决策关闭，UNKNOWN永远不告警，other/runtime_unavailable不创建availability occurrence。

## Acceptance matrix（实施验收结果）

| ID | Fixture / assertion | Verification |
| --- | --- | --- |
| A1 | provider=antigravity + runtime source=file；memory/unknown/其他Provider不误纳入 | Driver + PG/UI |
| A2 | 两个不同非空request_id的401/invalid_grant → TOKEN_INVALID Critical；一个失败（可无ID）+fresh完整runtime error可确认 | normalize/evaluator PG |
| A3 | 明确blocked白名单按原去抖 → ACCOUNT_BLOCKED Critical；普通403仅与fresh runtime error/unavailable交叉确认FORBIDDEN Warning | 白名单positive/negative |
| A4 | 单次401或任意数量普通403+active只pending UNKNOWN；同request_id/无ID多hash/replay不能凑独立请求证据 | PG |
| A5 | stale/最新degraded/不完整/Node管理401/采集失败 → UNKNOWN，不新告警 | PG/API |
| A6 | fresh disabled → DISABLED，无告警；不自动resolve已有ACTIVE | PG/UI |
| A7 | success严格新于最后故障→RESOLVED；同timestamp或更新冲突不恢复 | PG |
| A8 | 无流量两次相邻5分钟槽完整active才恢复；同source100次不凑数；中间失败/停机跳槽打断 | PG/race |
| A9 | A/B同account_key完全隔离；unresolved/event-only不造账号 | PG/API |
| A10 | 并发/restart/retry/未知commit最多一个ACTIVE；失败不推进checkpoint | 真实PG/race |
| A11 | 15m滑窗/7天retention/current poll外键NULL不虚假恢复或重复计数 | PG |
| A12 | 恢复水位以前旧event不重开；后续新确认故障有新UUID | PG |
| A13 | success/no-requests原taxonomy和Quality分类不变；auth子原因NULL legacy不回填 | normalize/PG兼容 |
| A14 | Token/raw/status_message canary不进入DTO/SQL参数/DB/API/log；只在边界丢弃原文 | parser/serializer/integration |
| A15 | runtime EXECUTE成功/direct SELECT拒绝/PUBLIC无EXECUTE；测试Down/Up保留旧数据与函数 | PG ACL/migration |
| A16 | 401/403授权、400cursor/limit、404Node、503DB与Unknown/Empty分开 | API/UI |
| A17 | Availability/Reason/Since六态，系统时区；Node切换取消，不加mutation | frontend |
| A18 | 100账号/10000events batch有界、>100账号不会永久饥饿，无N+1 | PG性能/调用计数 |
| A19 | lifecycle回调超时/cancel不影响Inventory、Binding、Duplicate、Gateway数据面 | wiring/race/regression |
| A20 | other/runtime unavailable持续15分钟以上/100次reconcile/restart仍UNKNOWN且零告警；旧ACTIVE遇UNKNOWN不重发、不升级、不虚假resolve | PG/race/UI |
| A21 | 同request_id的多个不同event_hash + active → 只一份证据，不确认 | evaluator/真实PG |
| A22 | 同reason两个不同非空request_id → 两份请求证据，仅token/blocked可用 | evaluator/真实PG |
| A23 | 无request_id的两个不同event_hash + active → 不确认 | evaluator/真实PG |
| A24 | 无request_id的一个401/invalid_grant + fresh runtime error → TOKEN_INVALID | evaluator/真实PG |
| A25 | 两个不同request_id普通403 + runtime active → UNKNOWN/pending_confirmation，零FORBIDDEN occurrence | PG/API/UI |
| A26 | 普通403 + fresh完整runtime unavailable → FORBIDDEN Warning | PG/API/UI |
| A27 | 明确blocked白名单满足原去抖 → ACCOUNT_BLOCKED Critical；普通403不能替代code | normalize/PG |
| A28 | 已确认request_id在恢复/restart/retention后换hash重放 → 不算新的请求证据 | PG/race |
| A29 | 普通403仍进入既有auth Quality/Incidents；不套用availability门槛改变原计数 | compatibility PG/API |
| A30 | 旧FORBIDDEN ACTIVE后runtime active但未满足恢复 → 当前UNKNOWN，历史保留不重发；不能绕过当前runtime门槛 | PG/UI |
| A31 | 两个runtime source带token/blocked但无request failure → 不确认，不能用runtime-only绕过P1-1 | PG |

## Planning commands

- `openspec validate add-antigravity-account-availability-monitoring --type change --strict --no-interactive`：PASS。
- `openspec validate --all --strict`：18/18 PASS。
- `git diff --check`：PASS；新文档另检查行尾空白。
- `go test -race ./internal/authfailure ./internal/drivers/cliproxyapi ./internal/requestquality ./internal/api ./cmd/control`：PASS。
- `go test -race ./internal/store -run 'Test(AccountAvailability|AntigravityAvailability)' -count=1 -v`：真实PostgreSQL PASS，availability专项及metadata finalize PASS；100账号/10002事件reconcile 384ms，100账号batch 7.1ms，occurrence分页单次查询约0.8–2.7ms。
- `npm test -- --run`：22 files / 133 tests PASS；`npm run typecheck`：PASS；`npm run build`：PASS。
- `make test build`：PASS（Go模块缓存使用 `/Volumes/DevRAM/go-mod`，其余缓存按AGENTS配置）。
- `openspec validate add-antigravity-account-availability-monitoring --type change --strict --no-interactive`：PASS；`openspec validate --all --strict`：18/18 PASS；`git diff --check`：PASS。
- `git status --short`：仅当前change的实现、测试、migration、generated/API/UI与文档修改；未修改CLIProxy仓库。

## Architecture Review

原A1冲突已解决。用户明确：“保持 UNKNOWN 永远不告警，第一版不做 other/runtime_unavailable 告警。”已同步proposal/design/spec/tasks及A20验收，明确三种occurrence reason与固定严重度，不保留UNKNOWN告警例外。

原独立Architecture Agent与主Agent复核除A1外未发现实质blocker。本轮主Agent逐项复核：UNKNOWN长期/重复/重启不创建、重发或升级告警；other不能进入故障确认计数；仅明确三reason有occurrence；历史ACTIVE与当前UNKNOWN分开，不篡改已确认历史或伪造恢复。正常化中的other安全枚举保留，但不是告警类型。runtime不可用仍可为明确认证失败提供旁证，不可单独产生故障。

## Review outcome

前轮Architecture PASS仅是历史结论；本轮按用户P1要求重新评审。P1-1已将独立请求条件改为同Node/account内至少两个不同非空request_id（仅token/blocked请求路径），同ID多个hash永远只一份、无ID不能只凭hash确认；无ID仍可通过request+runtime交叉确认。P1-2已将FORBIDDEN限定为普通403+fresh runtime error/unavailable，重复403+active保持UNKNOWN；blocked白名单原确认门槛不放宽。

本轮独立Architecture复审与主Agent复核：PASS，P1-1/P1-2均关闭，无剩余blocker。A21–A31已纳入实现与真实PG/解析测试证据。本轮已完成实现与验证，未提交、未部署、未归档。


## P1 re-review evidence

- 首轮复审发现旧runtime-only确认路径仍绕过P1-1；已删除，改为不同request_id请求路径（仅token/blocked）或request failure + 同Node/account fresh完整runtime交叉路径。最终独立复审PASS，没有沿用旧Architecture结论冒充本轮结果。
- 普通403的安全子分类仍为forbidden，原auth taxonomy/Quality/Incidents不变；仅availability要求runtime旁证。future retry折叠出的unavailable和management HTTP故障不能充当账号runtime旁证。
- 同ID永久只一份；确认摘要保留已用request_id，重启/恢复/retention后换hash不再充当新请求，无新增全量历史身份/去重系统。
- 首次runtime健康但尚待第二份恢复证据的UNKNOWN展示不重置健康source计数；旧FORBIDDEN ACTIVE不能使当前active runtime仍被展示为FORBIDDEN。既有恢复阈值和历史保留不变。
- 同步范围：proposal、design、availability spec、request-quality delta、tasks、acceptance matrix；新增A21–A31覆盖用户七项必需场景及重放/兼容/旧ACTIVE/runtime-only负例。
- 最终change strict PASS、all strict 18/18 PASS、git diff --check PASS；实现任务2.1–5.6全部完成。

## Implementation Final Review

- Architecture Contract：PASS；实现保持 Antigravity file-only、六态、UNKNOWN永不告警、三种occurrence reason与request_id交叉确认规则。
- Implementation：PASS；source projection、nullable安全字段、受控migration、checkpoint/occurrence evaluator、生命周期接线、只读API/UI与ACL均已实现并通过对应测试。
- 兼容性边界：CLIProxy零修改；usage queue、collector、event主schema、retention、failure taxonomy及Inventory/Binding/Duplicate truth未改变；无Prometheus/Grafana、quota、inspection或mutation。
- Blockers：无。非阻塞事项：真实测试需使用可写模块缓存目录，已在验证命令中显式设置 `/Volumes/DevRAM/go-mod`；不影响产品运行时契约。
- Final Review 结论：PASS，change可进入提交/后续发布审批；本轮不commit、push、deploy或archive。

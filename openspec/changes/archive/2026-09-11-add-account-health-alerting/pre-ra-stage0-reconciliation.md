# Phase 5 PRE-RA Stage 0 reconciliation

日期：2026-09-11。Control baseline：`eab99a93bd2e16c83835b64da3566295ca8f8fee`；Ops baseline：`dd3041f916dc16578f21790e71e49c3f751decda`。开始时两仓 clean；未 fetch/pull/reset/rebase。本文记录 implementation reconciliation，不是正式 Runtime Acceptance。

## Task 6.1

用户已正式 Review PASS（P0/P1/P2=0/0/0）。外部 `/Volumes/DevRAM/phase5-6.1-db-acceptance.md` 原样纳入 [Task 6.1 evidence](./task-6.1-pre-ra-db-acceptance.md)，`cmp` PASS，byte-identical。本轮未重新执行 6.1。

原 evidence 的 PostgreSQL 18.6、00027→00032、clean/upgrade/legacy/v1/ACL、52/52 probes、68/68 PostgreSQL test results 与 zero unexpected skips 原样保留，不改写为本轮执行。6.1 checkbox CLOSED。

## Task 1.4

Control 修改：`AGENTS.md`、`openspec/config.yaml`、`api/openapi.yaml` 的 `NormalizedAccountEmail` description、`openspec/specs/{account-inventory-readonly-query,account-inventory-lifecycle,account-inventory-snapshot,account-inventory-poll-run,cliproxyapi-readonly-driver}/spec.md`，以及本 change `design.md` 的 reconciliation 状态。

OpenAPI 经核验确有 `never ... audit detail` 旧描述，故只修该描述；paths、operation IDs、schema shape、generated clients 不变，不是 Task B 引起的 API 语义变更。Canonical specs 中的全局 sensitive/only-position 表述改为当前普通业务邮箱策略；各 endpoint 原有最小日志/审计白名单、URL/cursor/storage/验收 artifact 限制保留，不新增产品日志/审计行为。

Ops 仅修改 `docs/RELAY_STATION_SYSTEM_DESIGN_CN.md`、`docs/RELAY_STATION_EXECUTIVE_OVERVIEW_CN.md`。系统设计明确 supersede ADR-0002 的旧 masked/HMAC UI 和邮箱专属 reauthentication 文字，ADR 历史原文不动。`PHASE5_ACCOUNT_HEALTH_ALERTING_CN.md` 与 active account-health spec 已符合邮箱策略，verify-only。

邮箱为普通业务身份，批准的 protected DB/authenticated API/UI/audit/controlled business logs/durable payload/DingTalk body 可使用完整值，不 mask、不为业务展示 HMAC、不新增邮箱专属权限。Prometheus/Alertmanager labels 不使用 raw email/account_key；确需指标身份时使用环境隔离的不可逆 HMAC account_id。该 label 约束不限制 DingTalk body。Token、credentials、Management Key、webhook URL/query、signing secret、password、credential headers、raw upstream response/body 的保护不放宽。1.4 CLOSED。

## Task 1.5

`openspec/specs/antigravity-account-availability/spec.md` 的 `Active runtime after previously confirmed forbidden` 已统一为 success-only recovery：任意数量 file_active observations 不得 resolve；需要 `success=true` 且 `occurred_at > last_failure_at`，并满足既有 recovery guards。

README 区分 generic empty registry 与当前 Control composition：`jobs.NewProductionRegistry()` 仍为空；`cmd/control/dingtalk.go` 的 `newProductionJobRegistry` 显式组合 reviewed DingTalk Definition/Executor，`cmd/control/main.go` 使用该 composition。没有宣称 generic constructor 默认包含 DingTalk。Active durable-job delta 经核验已正确描述两个独立 default-off persisted policies、unknown⇒replay_safe、direct 不隐含 replay_safe、snapshot/mismatch fail-closed，未重写。Design 标记 canonical reconciliation 完成；archive、旧 foundation acceptance evidence、旧 two-observation 历史不变。1.5 CLOSED。

## Task B: exactly three test files

- `tools/openapi_contract_test.go`：仅在 exhaustive expected map 增加 `POST /api/problem-accounts/query` / `queryProblemAccounts`，保留 operation count 和逐项 assertion。
- `cmd/control/gateway_directory_main_deployment_test.go`：HTTPS-success fixture 改为 HTTP，使用 source.URL，去除 obsolete certificate/localhost assumptions，测试名为 `TestGatewayDirectoryMainDeploymentHTTP`。
- `cmd/control/gateway_directory_process_test.go`：只保留 HTTP recovery variant；其余变动为去掉未使用 fmt 和格式缩进。

保留真实 main、disabled 零请求/零 run、快照写入、9007199254740993 精度、healthz/metrics、bounded SIGTERM、same-slot/restart、snapshot preservation 和 secret/identity-negative assertions。`git diff -w` 核对未删除这些检查。Gateway production HTTP-only、DingTalk direct HTTPS、worker/retry/state machine 全部未改；无 migration/Go production/React/generated 变更。

## Validation executed

使用现有 DevRAM Go/cache/temp 环境与仓库工具；Task B 数据库为专用 PostgreSQL 18.6 disposable `phase5-stage0-postgres` 容器、tmpfs、loopback 端口，owner/runtime URL 仅在进程环境中。未连接既有业务/开发数据库。现有 helper 每测试创建独立数据库并迁移、清理。

| Command / check | Actual result |
|---|---|
| `cd tools && go test ./... -count=1` | PASS，最终 map 45/45；uncached（修改后再次运行 PASS） |
| `go test ./internal/drivers/gatewaydirectory -run '^(TestManagementHTTPOnly\|TestManagementHTTPSRejectedBeforeRequest)$' -count=1` | PASS，HTTP success 与 HTTPS-before-request rejection |
| `go test ./cmd/control -run '^(TestGatewayDirectoryMainDeploymentHTTP\|TestGatewayDirectoryRuntimeProcessRecovery)$' -count=1 -v` | PASS，2 top-level + 1 HTTP subtest；真实 DB 执行，0 skip |
| `go test ./cmd/control/... -count=1 -json` | package PASS；82 pass results（含 subtests），0 fail，1 expected opt-in skip，见下文 |
| `go test ./internal/drivers/gatewaydirectory/... -count=1` | PASS |
| `openspec validate add-account-health-alerting --strict` | PASS |
| `openspec validate --all --strict` | 20/20 PASS |
| local Markdown file references in changed/imported docs | PASS，无 missing target |
| Control / Ops `git diff --check` | PASS |
| `make test build` optional 6.2 preflight | NOT RUN；不将 focused PASS 冒充 full build / 6.2 acceptance |

唯一 skip 是 `TestLocalDirectoryToolingNaturalRecovery`，明确要求 opt-in `RELAY_DIRECTORY_TOOLING_E2E=1`，本轮未启用该额外本地 tooling E2E；不是缺 DB URL 的 skip，不计入 PASS。要求的 main deployment、process recovery、broader same-slot competition 均实际 PASS。Unexpected skips = 0。Task B 当前三个 stale-test blockers RESOLVED；不改写历史 evidence 曾报告 make PASS 的原始记录。

## Scope / self-review

Current truth-source rg 检查区分现行冲突与历史 archive/evidence。旧 ADR 邮箱策略有明确 current supersession；Secret 与 metrics cardinality 不弱化。Task B 三文件之外仅为批准的 canonical docs、Task 6.1 import、tasks 和本 evidence。OpenAPI 唯一 description 修正属于 1.4；无 generated Go/TS、historical migration、Gateway 或 CLIProxyAPI repository 修改。

Checklist 实际计数 44/50：本轮仅关闭 1.4、1.5、6.1；6.2、6.3、6.4、6.5、6.6、6.7 均 OPEN。OpenSpec apply-change skill 用于按现有任务与契约核对，不把 optional preflight 当 task closure。

Self-review P0/P1/P2 = 0/0/0，不冒充本轮正式外部 Review。READY FOR FORMAL REVIEW。

Runtime Acceptance：NOT STARTED。RA-GO：NO（6.2 与 browser/current RA environment readiness 尚未正式 gate）。Real DingTalk messages：0。Commit：NONE。Push：NONE。

# Phase 5 Slice B implementation validation

本轮范围：Token Health DB read projection 与现有 Account Quality API 的 additive 字段。

Detailed Requirements: FROZEN；Architecture Review: PASS；Implementation: IN PROGRESS；Runtime Acceptance: NOT STARTED。

## Implementation baseline

用户已正式确认 Slice A Implementation Review PASS（P0/P1/P2 均为 0）。检查工作树与 staged scope 后，按授权提交 Slice A：`2eee43d00efc04f2141282c49c3f51fb9486e4dc`，`feat(phase5): implement durable job execution policies`。未 push。

| 仓库 | Slice B 开始时 committed HEAD |
| --- | --- |
| Control | `2eee43d00efc04f2141282c49c3f51fb9486e4dc` |
| Ops | `dd3041f916dc16578f21790e71e49c3f751decda` |
| Gateway | `6b045698e6e5e62e35dbd103abf20c1407f8a0bb` |
| CLIProxyAPI | `273d624c70f6eb8bdd7b049df396c306acd3f8d0` |

Slice A 历史见 [Slice A validation](./slice-a-validation.md)；架构批准历史见 [planning-validation](./planning-validation.md)。上述 SHA 是已提交前置基线，不是本 Slice B 工作树的自引用 SHA。

## 实施边界

- 新增 `00029_account_token_health_projection.sql`，仅增加两个受控 read functions 与 ACL；不修改已提交的 00028，也不新增表、列、索引或 Token 持久状态。
- `control_query_account_token_health_v1` 复用 Phase 4 `control_account_availability_source_v1.gate_reason`，只使用 Inventory `last_refresh_at`、既有资格事实与 ACTIVE `token_invalid` occurrence；固定 PostgreSQL `statement_timestamp()` 与 3599 秒边界，不使用 Go/UI 时间算法。
- `expected_valid_until` 是 Expected，而非真实 expiration；refresh 非空时即返回 +3599 秒，包括 INVALID、future、expired；null refresh 返回 null。
- 实施基线已经存在 Quality v1/v2/v3，故采用下一版本 `control_query_node_account_quality_v4`。v4 materialize 原 v3 有界页面，一次 batch Token projection 后关联；不重写旧 Quality 算法，不增加应用层逐账号查询。旧版本签名/行为保留。
- 沿用现有 Quality GET/POST endpoint、认证、cursor、audit 与错误语义，按已冻结要求增加 DTO 字段。非 Antigravity 不推断 Token 状态。OpenAPI 枚举显式命名，避免生成器改名已有 Availability 常量。
- Go/TypeScript generated clients 通过 `make generate` 生成，不手工修改；现有 raw-SQL store adapter 直接映射 DB 返回值，sqlc 生成物无额外差异。

没有 Problems、occurrence notification integration、DingTalk HTTP/config、UI 页面、凭据读取、Token refresh/probe、Phase 6/7 或数据面改动。Ops/Gateway/CLIProxyAPI 保持不变。

## Validation

使用工作区要求的 DevRAM/cache 环境。PostgreSQL 检查只使用本地 PG18 测试容器 55432 上由既有 helper 创建并清理的隔离数据库，不修改运行中的部署数据库 55434。

| 检查 | 结果 |
| --- | --- |
| 新 Token projection PostgreSQL focused suite | PASS，10 项；与 7 项既有 Quality 回归合并运行共 17 项，30.478s |
| Quality / Availability HTTP contracts（含三态透传、expected null、credential-negative） | PASS，2.043s |
| `TestUnifiedAccountPOSTContracts` | PASS，1.384s |
| 现有 Quality composition/lifecycle/acceptance/audit/unified read/performance | 7 项 PASS；初次混合 8 项 run 中另一个历史 fixture 失败，见下文 |
| `make generate` | PASS |
| `make test build`（默认不设置 DB URL） | PASS；前端 22 files / 133 tests，typecheck/build PASS，不替代 PG 验证 |
| OpenSpec current change / all-repo strict | PASS，20/20 |
| Markdown local references / fenced blocks；`git diff --check` | PASS |

新 PG suite 覆盖：精确 DB now/3598.9/3599/future/null、ACTIVE invalid 在不合格/新 refresh/null 下仍优先、RESOLVED 后恢复正常投影、ACCOUNT_BLOCKED/FORBIDDEN 不误判、stale/transport failure/缺少 monitoring/missing/out_of_scope/unsupported mode/identity incomplete/disk fallback/contract invalid、expected 字段保留、28→29 upgrade 的旧函数定义与全列 catalog 不变、clean install、两函数 owner/search_path/runtime/PUBLIC ACL、非 Antigravity null、101 账号 page+1 与后续页面。精确边界在同一个 DO statement 中设置 refresh 并调用实际 projection 断言；后续 runtime read 仅核对稳定诊断时间，不将自然跨 TTL 的时间变化误判为失败。

101 账号 v4 `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`：101 rows，1 outer Function Scan loop，11.240ms，零 temp blocks；结合函数结构检查确认单次 materialized batch Token 调用。旧 Quality 内部统计算法保持，不据此声称重写/消除了它的既有内部逐项统计。现有 100 账号/10000 events unified 首页面 27.296ms。未发现需要新增索引的证据。

最初新测试 fixture 因未登记 Quality capability、未填 lifecycle shape、slot 未对齐以及 invalid 创建顺序错误失败；仅修正本次新增 fixture 后重跑通过，未放宽生产约束。两名 `gpt-5.6-luna` 子 Agent 分别负责 SQL/只读复核与新 DB 测试草稿，主 Agent 完成 API/store、修正 fixture 并独立执行最终合并验证。

全 store PostgreSQL suite 仍为 **NOT GREEN / PRE-EXISTING**，本轮不重跑全套或修无关基线。选定 Quality 回归中的 `TestNodeAccountQualityLifecycleFilterAndACLPostgres` 仍错误假定最新 migration 的 down 会移除 v2：当前 29 down 后报 `v2 not removed`；隔离导出 committed Slice A HEAD `2eee43d` 复核同一测试，在 28 forward-only down 处失败（2.449s）。这是既有 rollback fixture 假设，不是 Token projection 的新产品失败，未修改或弱化该 fixture。

构建期间 Go stat-cache 写入系统 module cache 的警告不影响命令 exit 0；测试和生成使用指定 DevRAM 环境。未执行部署、Phase 5 全套 Runtime Acceptance、真实 DingTalk 或后续 slices。

## Task accounting / stop point

本轮新增勾选 1.2、2.1、2.2，总计 13/50。1.3/2.5 只完成 Quality 部分，仍保持 open；Problems、DingTalk、通知事务、UI 和全 Phase Runtime Acceptance 均不勾选。

Slice B readiness for Implementation Review: READY。本轮自检 P0/P1/P2 = 0；不代替正式 Implementation Review。Architecture Review: PASS；Implementation: IN PROGRESS；Runtime Acceptance: NOT STARTED。

Slice B 保持未提交、未 push；到此停止，等待 Implementation Review，不进入 Slice C。

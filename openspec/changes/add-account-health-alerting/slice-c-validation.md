# Phase 5 Slice C implementation validation

范围仅为 Problems DB/read-model、filters/keyset/ACL 与 `POST /api/problem-accounts/query`。Detailed Requirements: FROZEN；Architecture Review: PASS；Implementation: IN PROGRESS；Runtime Acceptance: NOT STARTED。

## Committed implementation baseline

用户已正式确认 Slice B Implementation Review PASS（P0/P1/P2 均为 0）。核对 working tree、staged 文件与 diff 后，按授权提交已审 Slice B：`dc510cee957ae290c05f5f2e6adabcf61bb8ee8d`，`feat(phase5): add account token health projection`。未 push。

| 仓库 | Slice C implementation baseline |
| --- | --- |
| Control | `dc510cee957ae290c05f5f2e6adabcf61bb8ee8d` |
| Ops | `dd3041f916dc16578f21790e71e49c3f751decda` |
| Gateway | `6b045698e6e5e62e35dbd103abf20c1407f8a0bb` |
| CLIProxyAPI | `273d624c70f6eb8bdd7b049df396c306acd3f8d0` |

这是前置 committed baseline，不是本 Slice C 工作树的自引用 SHA。历史见 [Slice B validation](./slice-b-validation.md) 和 [Architecture Review evidence](./planning-validation.md)。

## Implementation and domain boundaries

- `00030_problem_accounts_query.sql` 仅新增 `control_query_problem_accounts_v1` 与 ACL；SECURITY DEFINER、owner `relay_control_migrator`、`search_path=pg_catalog`、PUBLIC 无 EXECUTE、runtime 有 EXECUTE。不新增表、列、索引或底表写权限；00028/00029 不变。29→30 测试比较全部已有列和函数定义，无变化。
- 查询由三类 ACTIVE Availability occurrences 与 ACTIVE duplicate occurrence/current `cross_node_duplicate_occurrence_nodes` 驱动。按 `(instance_id, account_key)` 聚合；只在有界页面确定后 LEFT JOIN diagnostics，缺少 Inventory 或 asset diagnostics 也保留 confirmed issue。
- Duplicate 直接消费现有 durable membership，不重新实现 Evaluate。真实 domain 测试 A/B/C→A absence_confirmed 后 B/C 仍 ACTIVE→B absence_confirmed 后原 occurrence RESOLVED；逐项保留 stale/unavailable/unverifiable/degraded/incomplete 下的成员。没有 notification integration，读取及 membership-only 测试未产生 jobs。
- Availability ACTIVE issue 不因 UNKNOWN/DISABLED/stale/missing/out_of_scope 或 Inventory 行缺失而消失。多个 Availability issues 与 duplicate 可共存于一行，issues 顺序固定。RESOLVED occurrence 不再投影为 issue。
- Antigravity 的规范化 `account_key=antigravity:<email>` 使同一 Node/email 对应唯一 account identity。四项排序为 Critical 优先、oldest active ASC、email ASC（C collation）、instance_id ASC；同 occurrence 三个 Node 的相同 severity/time/email 分页测试覆盖最终 tie-break，无静默增加排序键。
- DB 执行 issue aggregation、五种 filters、排序和 keyset，返回 limit+1；API 默认 25/最大 100、16KiB body、5s query budget。Go 只映射有界结果，不聚合全量 occurrences，不逐账号发起查询。复用现有 raw-SQL store adapter 惯例；`make generate` 生成 Go/TS OpenAPI clients，sqlc 生成物无额外差异。
- Cursor 复用现有 Keyring/DomainAssetCursorDigest 的签名模式，以 Problems 域隔离，绑定 actor、规范化 filters 和固定排序；严格验证签名、JSON、长度与 key version。时间先转 UTC，避免 DB connection 时区影响 continuation。
- API 沿用 session、super_admin、CSRF、no-store 与固定安全错误。认证/CSRF 拒绝复用既有审计动作；不冒用 node-specific `account_inventory.view` 作为全局 Problems action，不新建邮箱专用审计或权限。认证拒绝在可信 Session 返回之前发生时，既有 audit 按 request_id 关联，actor 可为空。
- Token state 复用 Slice B DB projection；Problems 的 TOKEN_INVALID issue 仍来自 occurrence，而非 token_state。Request Quality 最后成功/失败为当前保留 evidence 的可选诊断；不新建生命周期。完整 email 保留，凭据字段不返回、不读取，无新增 metrics。

当前 asset schema 尚无 Relay Node Retire 状态；本轮未引入 Phase 6 Retire 实现。测试证明缺失 asset/Inventory diagnostics 不隐藏确认事实，SQL 没有 active-Node 过滤；不将此宣称为真实 Retire 操作的运行验收。

## Validation

使用工作区 DevRAM/cache 环境。PG 测试显式配置本地 PG18 的 55432 URL，使用已有 helper 的隔离数据库；不修改部署数据库 55434。

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/store -run '^TestProblemAccount' -count=1`，显式 PG URL | PASS，9 项 PG tests + 3 项 cursor tests，15.849s；含 mixed row 在 duplicate 退出/恢复后保留 Availability issue |
| Problems、Quality、Availability、Quality incidents、unified POST HTTP contracts，显式 PG URL | PASS，5 项，5.305s；含 200/400/401/403/503、no-store、CSRF/authorization audit、cursor/filter bounds、Secret-negative |
| Token/Quality focused + Availability confirmation/recovery/concurrency + Duplicate lifecycle/query | PASS，46.649s；保留 v1/v2/v3/v4 与 Token DB-time/3599 秒回归 |
| `make generate`；`make test build`（默认不设置 PG URL） | PASS；前端 22 files / 133 tests、typecheck、build PASS，不替代显式 PG suite |
| OpenSpec current/all strict | PASS，20/20 |
| Markdown local references/fences；`git diff --check` | PASS |

性能样本为 1000 Inventory accounts、1000 ACTIVE Availability occurrences、1000 duplicate occurrences/memberships、2000 Request Quality events。实际函数 EXPLAIN ANALYZE：15.119ms、26 rows、1 outer Function Scan loop、零 temp blocks。另从实际函数定义提取 RETURN QUERY 主体，仅替换调用参数/局部常量执行 EXPLAIN：11.314ms、estimated rows 7 / actual rows 26，无 temp spill 或重复全表 occurrence/membership scan。已有索引的 membership nested-loop probes 是正常 set-based join；诊断子函数内部仍以 Function Scan 表示，未宣称重写其内部算法。无证据需要新增索引。

新 fixture 的 provider-state shape、HTTP fake cursor/CSRF/参数化 SQL、审计关联以及性能断言均按真实既有 contract 修正，最终由主 Agent 显式设置 PG URL 重跑。性能断言区分已有索引查找和重复全表扫描，不把正常索引查找误判为应用 N+1。

两名 `gpt-5.6-luna` 子 Agent 分别协助 SQL/API fixture、cursor/DB fixture 与只读复核；主 Agent 负责跨层设计、production 集成与最终验收。HTTP 子 Agent 最初未设置 PG URL 的结果仅能证明编译，不能证明数据库测试执行，已排除出验收证据；上表采用主 Agent 显式 PG URL 的实际执行结果。独立只读复核未发现 P1/P2。

发现一个由新增 00030 直接触发的新 fixture signature：`TestNodeAccountQualityV4AddsTokenProjectionWithoutChangingV1V2V3Postgres` 对最新版本 down 一次，却断言 00029 的 v4 已删除。仅将该测试固定到其所验证的 `up-to 29`，保留所有 down/兼容断言，重跑通过；未修改历史 migration。

Full-store PostgreSQL suite 仍为 **NOT GREEN / PRE-EXISTING**，本轮未重跑全套，未处理 Gateway TLS、旧 history/rollback/query-plan/CI/Vitest 等无关基线。默认 `make test` 成功不表示 full PostgreSQL suite 或 Runtime Acceptance PASS。

## Task accounting and stop

按 OpenSpec apply-change 流程仅新增勾选 1.3、2.3、2.4、2.5。1.4、安全文档统一、通知事务、DingTalk、Problems UI 与全 Phase Runtime Acceptance 保持 open；3.6a 的完整通知验收未勾选。

Slice C 保持未提交、未 push，等待 Implementation Review。没有 DingTalk HTTP、notification enqueue、Problems UI、Problem 表/生命周期、repair action、凭据读取或 Phase 6/7 实施。Ops/Gateway/CLIProxyAPI 工作树和 HEAD 均未改变。完成本 Slice 后停止，不进入 Slice D。

Slice C readiness for Implementation Review: READY。自检 P0/P1/P2 = 0，不代替正式 Implementation Review；Implementation: IN PROGRESS；Runtime Acceptance: NOT STARTED。

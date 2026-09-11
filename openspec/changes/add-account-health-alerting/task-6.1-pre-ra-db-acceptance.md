# Phase 5 Task 6.1 PRE-RA Database Acceptance

日期：2026-09-11。Baseline：`eab99a93bd2e16c83835b64da3566295ca8f8fee`。
这只是 PRE-RA database acceptance，不是 P5-RA-001..086；Runtime Acceptance NOT STARTED。未 fetch/pull/rebase；未改 production/migration/OpenAPI/generated/tasks；无真实 DingTalk 请求。

## Environment / migration lineage

PostgreSQL 18.6，aarch64 Alpine；临时独立容器 `phase5-pre61-postgres`，tmpfs 数据目录，仅 loopback 端口。使用仓库 `deploy/postgres/init/001-runtime-role.sql`，Goose 来自 `tools/go.mod` 的 `go tool goose`。未连接任何既有开发/业务数据库。连接只通过命令环境提供；本文不保存完整连接字符串或凭据。

PRE_PHASE5_LAST_MIGRATION=00027
CURRENT_LATEST_MIGRATION=00032

PHASE5_MIGRATIONS:
- 00028_durable_job_execution_policies.sql
- 00029_account_token_health_projection.sql
- 00030_problem_accounts_query.sql
- 00031_dingtalk_alert_delivery.sql
- 00032_transactional_notification_snapshots.sql

## Paths and legacy preservation

Clean：全新 `pre61_clean` → Goose `up`，00001..00032 全部成功，无跳过、schema repair 或手工 migration intervention。
Upgrade：全新 `pre61_upgrade` → Goose `up-to 27` → 合法 legacy fixture COMMIT → Goose `up`（00028..00032）→ version 32。

Legacy fixture 由 owner 创建合法资产/policy，调用既有 poll claim + `control_finalize_account_inventory_poll_run_with_lifecycle_v2` 生成真实 Inventory/provider promotion；随后保存 Availability checkpoint/ACTIVE occurrence 和通过 `control_enqueue_async_job` 创建的普通 durable job/event/outbox。Fixture 仅 synthetic `.invalid` identity，不包含 credential。包含 environment、Relay Node、gateway asset（Directory 所依赖的 identity）、Inventory current state、Availability、ordinary job；没有伪造不存在的 domain。该 path 不声称创建完整 Gateway Directory snapshot 业务流程。

升级前后上述所有 legacy persisted rows（含 IDs、semantic fields、timestamps）逐对象 JSON 相等；async_jobs 比较去掉两个新增 default-off policy 列，另以 owner 检查二者均 false。既有表 OID 全部保留，证明没有 drop/recreate。clean 同样建合法 fixture 用于实际查询证明。

clean_install=PASS；forward_upgrade=PASS；legacy_data_preserved=PASS。

## Phase 5 function inventory / actual catalog matrix

下表来自两数据库 pg_proc/pg_roles/aclexplode/has_function_privilege 查询，非仅 SQL 文本扫描。clean 与 upgrade 每项完全一致。PUBLIC 为 effective default ACL 展开后的权限。00028 五个 CREATE OR REPLACE 继承00004 owner/ACL；已实际验证继承结果。

| Full signature | Migration | owner clean/upgrade | SECURITY DEFINER clean/upgrade | search_path clean/upgrade | PUBLIC EXECUTE clean/upgrade | runtime EXECUTE clean/upgrade |
|---|---|---|---|---|---|---|
| `public.control_claim_expired_async_job(text,uuid)` | 00028 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_enqueue_async_job(uuid,text,text,integer,uuid,jsonb,bytea,smallint,uuid,text,boolean)` | 00028 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_guard_async_job_mutation()` | 00028 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | False / same |
| `public.control_notification_display_snapshot_v1(text,uuid[])` | 00032 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_query_account_token_health_v1(uuid,text[])` | 00029 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_query_node_account_quality_v4(uuid,text,text,text,text,text,text,interval,integer)` | 00029 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_query_problem_accounts_v1(text,uuid,text,text,text,text,timestamp with time zone,text,uuid,integer)` | 00030 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_reconcile_account_availability_v2(uuid,text)` | 00032 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_request_async_job_cancel(uuid,text)` | 00028 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |
| `public.control_transition_async_job_fenced(uuid,text,uuid,text,text,integer,text,text,text,text,boolean)` | 00028 | relay_control_migrator / same | true / true | pg_catalog / same | revoked / revoked | True / same |

00031 没有 SQL function 增量，仅注册 catalog。10/10 owner、SECURITY DEFINER、fixed search_path、PUBLIC revoke PASS；9 个 runtime-callable function 全部实际 ALLOW；trigger helper 明确 DENY。

## v1 / additive compatibility matrix

所有 pre-Phase5 control function signatures 在两条最终路径均存在，没有 Phase5 retirement。下列 required API-facing read/reconcile contracts 的 definition hash 与 return shape 在00027→00032完全不变，并实际调用成功（查询空结果仍为合法执行，不等于权限静态检查）。Quality v2/v3 同时保留；v4 和Token/Problems/transition-returning v2是 additive。

| Signature | Pre27 | Post clean / upgrade callable | Definition / result shape |
|---|---|---|---|
| `public.control_account_availability_source_v1(uuid,text[])` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_account_availability_occurrences_v1(uuid,text,text,timestamp with time zone,uuid,integer)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_account_availability_v1(uuid,text[])` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_account_inventory_provider_states_v1(uuid)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_account_request_history_v1(uuid,text,timestamp with time zone,text,integer)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_account_request_quality_v1(uuid,text,text,interval)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_gateway_directory_target_v1(uuid,uuid,uuid)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_node_account_quality_v2(uuid,text,text,text,text,interval,integer)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer)` | PRESENT | PASS / PASS | UNCHANGED |
| `public.control_reconcile_account_availability_v1(uuid,text)` | PRESENT | PASS / PASS | UNCHANGED |

`control_account_availability_source_v1` 是 owner-only internal helper，使用 owner 验证兼容，不误报为 runtime allow。00026 已将 `_v1_legacy` 两个 finalize 函数私有化并撤销 PUBLIC/runtime EXECUTE；非 Phase5 regression。其替代为已冻结生命周期 finalize contract；未执行 retired legacy helper。

## Actual allow / deny calls (both paths)

runtime login=`relay_control_app_dev`，member of `relay_control_runtime`；实际查询确认 NOSUPERUSER/NOCREATEDB/NOCREATEROLE/NOBYPASSRLS。没有临时授权或 SET ROLE owner。owner-only helper兼容检查单独标注，不计作runtime授权。

每个调用在独立事务内执行并 ROLLBACK，避免 acceptance probe 改写 legacy evidence。enqueue 使用同一个已提交 ordinary logical job/key/payload；fenced transition 先正常 claim 获取有效 fence，再执行 failed transition；expired claim 无候选返回合法空结果，其行为由既有 focused tests补充。无 Executor、无外部HTTP。

| Function / operation | Role | Expected | clean | upgrade |
|---|---|---|---|---|
| `control_query_account_token_health_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_node_account_quality_v4` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_problem_accounts_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_notification_display_snapshot_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_reconcile_account_availability_v2` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_claim_expired_async_job` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_request_async_job_cancel` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_node_account_quality_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_node_account_quality_v2` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_node_account_quality_v3` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_account_availability_source_v1` | relay_control_migrator | ALLOW | PASS | PASS |
| `control_query_account_availability_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_account_availability_occurrences_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_reconcile_account_availability_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_current_account_inventory_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_account_inventory_provider_states_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_account_request_history_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_account_request_quality_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_query_gateway_directory_target_v1` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_enqueue_async_job` | relay_control_app_dev | ALLOW | PASS | PASS |
| `control_transition_async_job_fenced` | relay_control_app_dev | ALLOW | PASS | PASS |
| `owner-only trigger function` | relay_control_app_dev | DENY 42501 | PASS | PASS |
| `owner-only payload helper` | relay_control_app_dev | DENY 42501 | PASS | PASS |
| `registrar-only control operation` | relay_control_app_dev | DENY 42501 | PASS | PASS |
| `owner-only catalog write` | relay_control_app_dev | DENY 42501 | PASS | PASS |
| `PUBLIC unauthorized Problems query` | pre61_unauthorized | DENY 42501 | PASS | PASS |

52/52 probes PASS（含 owner helper兼容调用2项）；所有 DENY 为真实 SQLSTATE 42501。unauthorized role 无能力 membership，Problems query 证实 PUBLIC 不可执行。registrar-only控制函数、owner-only payload helper/trigger、catalog UPDATE均拒绝runtime。额外直接读取async_jobs两个policy列也被runtime拒绝，owner读取旧job false/false；没有放宽列权限。

## DingTalk production catalog

| Field | Clean | Upgrade |
|---|---|---|
| job_kind | dingtalk_alert_delivery | same |
| payload_schema_version | 1 | 1 |
| default_timeout_seconds | 10 | 10 |
| lease_seconds | 30 | 30 |
| heartbeat_interval_seconds | 5 | 5 |
| default_max_attempts | 5 | 5 |
| default_max_verification_attempts | 1 | 1 |
| replay_safe | true | true |
| allow_unknown_effect_replay | true | true |
| allow_direct_success | true | true |
| rollback_allowed | false | false |
| lifecycle_status | active | active |

与当前 production Definition 的一致性另由 TestDingTalkCatalogCleanInstallAndForwardUpgrade 验证。旧 pre61.legacy kind/job 两字段默认 false；未主动开启旧job权限。

## Schema parity / non-destructive rollout

clean/upgrade version32、全部control signatures/definition hashes/result shapes/owners/secdef/config/effective PUBLIC/runtime ACL、全部public table/index/view name/kind、全部constraint definitions、job-kind policy均逐值一致（跨DB不比较OID、catalog创建时刻）。升级路径内则比较原表OID不变。没有新增Token/Problems/notification领域表，原async_job tables/constraints和新的query/reconcile函数均存在。unknown⇒replay_safe CHECK由catalog及既有非法policy DB测试双重证明。

production rollout requires destructive down: NO。00028/00031 down明确forward-only拒绝；00029/00030/00032虽有down段，但完整forward已足够。本次未执行任何 Goose down/downgrade；仅清理自己创建的disposable DB。未要求生产删库或数据rewrite。

clean_upgrade_parity=PASS；non_destructive_rollout=PASS。

## Existing PostgreSQL regressions

命令：`go test ./internal/store -count=1 -json -run '<下面精确 test names 的 anchored alternation>'`，两批；CONTROL_DATABASE_TEST_URL 与 CONTROL_RUNTIME_DATABASE_TEST_URL 均显式指向该临时容器。沿用 DevRAM TMPDIR/GOCACHE/GOTMPDIR/npm/XDG cache 和 GOPROXY；没有运行 make generate/full-store/正式86-case。

17 top-level tests，51 subtests，合计68个test结果：passed68 / failed0 / skipped0；unexpected skip=0。

- PASS `TestNodeAccountQualityV4ForwardUpgradePreservesV1V2V3AndColumnsPostgres`
- PASS `TestNodeAccountQualityV4ACLAndNoNewDomainPersistencePostgres`
- PASS `TestNotificationDisplaySnapshotRuntimeACLAndProjection`
- PASS `TestAccountAvailabilityNotificationPaginationZeroAndMultipleTransitions`
- PASS `TestDingTalkCatalogCleanInstallAndForwardUpgrade`
- PASS `TestDingTalkCanonicalPayloadEnqueue`
- PASS `TestDurableJobRuntimeCatalogAndStateBypassMatrix`
- PASS `TestDurableJobExecutionPolicyForwardMigrationDefaultsAndOldJobs`
- PASS `TestDurableJobExecutionPolicyCatalogAndJobInvariants`
- PASS `TestDurableJobExecutionPolicyEnqueueSnapshotAndSameKeyConflict`
- PASS `TestProblemAccountsPostgresACL`
- PASS `TestProblemAccountsMigration29To30PreservesExistingSchema`
- PASS `TestAccountTokenHealthProjectionTTLAndExpectedValidUntilPostgres`
- PASS `TestAccountTokenHealthActiveInvalidPrecedenceAndRecoveryPostgres`
- PASS `TestDurableJobRuntimeRoleHasMinimumPrivileges`
- PASS `TestNotificationMigrationPreservesV1Postgres`
- PASS `TestProblemAccountsPostgresCoreProjection`

原始本地辅助证据：`/Volumes/DevRAM/tmp/pre61-{before,clean,upgrade}.json`、`pre61-acl-final.json`、`pre61-tests.jsonl`、`pre61-tests-extra.jsonl`、migration logs；报告仅输出结构/结论，不复制raw fixture行或连接信息。辅助脚本 `pre61-fixture.sql` / `pre61-catalog.sql` / `pre61-acl.py` 均在仓库外。

## Failure classification / limitations

- ENVIRONMENT setup correction：首次临时容器缺POSTGRES_DB，仓库init的CONNECT grant因此失败；删除该自建容器后以正确database名重建，最终bootstrap正常。没有改仓库init。
- TEST / FIXTURE DEFECT：早期直接Inventory fixture缺slot对齐/provider source/capability，分别被既有约束/P0409/P0503拒绝；丢弃该两测试DB，改为真实claim+finalize生成合法source。随后补齐fixture JSON的显式nullable字段，事务失败均完整rollback。最终从27重新生成fixture并forward、两库全矩阵PASS。不是migration defect，也未进行schema repair。
- TEST expectation correction：availability source internal helper原误按runtime ALLOW，既有42501正确；按既有owner-only contract分类并用owner验证可调用。
- OUTSIDE-6.1 KNOWN ISSUE：OpenAPI44/45与Gateway HTTPS stale tests未运行/未修改，仍留独立PRE-RA分类。不声称 full-store GREEN。
- 只读Luna子代理核对静态清单，主agent按实际catalog复核10项及实际ACL；不以静态结果代替运行证明。

P0: 0；P1: 0；P2: 0（本轮acceptance findings，自查，不冒充正式Review）。
6.1 eligible to close: YES，需后续正式审核；tasks.md保持OPEN、未改checkbox。
Repository tracked changes: NONE。Commit NONE；Push NONE；Runtime Acceptance NOT STARTED；Real DingTalk messages 0。

## Cleanup / final repository guard

已实际 DROP 本任务 `pre61_clean` / `pre61_upgrade`，并停止、删除 `phase5-pre61-postgres` 临时容器及其tmpfs数据。清理前catalog只剩本任务两库、空bootstrap库及postgres；既有测试自行创建的DB无残留。未清理任何其它容器、缓存或数据库。
最终 `git status --short` 空输出，`git diff --check` PASS；HEAD仍 `eab99a93bd2e16c83835b64da3566295ca8f8fee`。

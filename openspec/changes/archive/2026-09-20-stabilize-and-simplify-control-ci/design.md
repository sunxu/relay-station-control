# Design: Validation ownership and CI orchestration

## Validation Ownership Matrix

以下矩阵是本 change 的唯一 Current → Future ownership source。删除或迁移旧
job 前，future owner 必须存在且 replacement proof 实际通过。

| Evidence | Current Owner | Future Owner | Action |
| --- | --- | --- | --- |
| generate consistency | quality | quality | KEEP |
| Go/unit tests | quality | quality | KEEP |
| web tests | quality | quality | REPAIR |
| build/vet/OpenSpec/actionlint | quality | quality | KEEP |
| Go race | race | race | KEEP / CONDITIONAL |
| history static | history_static | quality | MOVE |
| history schema/store | history_postgres | history_schema_store | REPAIR |
| history process | history_process | history_process | REPAIR |
| history data-plane isolation | history_data_plane | history_data_plane | KEEP |
| snapshot recovery | postgres_snapshot | postgres_snapshot_core | REPAIR |
| lifecycle correctness | postgres_lifecycle | postgres_lifecycle_core | REPAIR |
| lifecycle capacity | postgres_lifecycle | lifecycle_capacity | MOVE |
| readonly correctness | postgres_readonly_query | postgres_readonly_core | SPLIT |
| readonly capacity | postgres_readonly_query | readonly_capacity | MOVE |
| upstream CLIProxyAPI compatibility | official_snapshot | compatibility workflow | MOVE |
| history aggregate | postgres_history | ci_required | REPLACE |
| history large-scale capacity | history-capacity.yml | capacity workflow | CONSOLIDATE |
| release image build | container.yml | container.yml | KEEP / TIGHTEN TRIGGER |

实施前的 Test Contract Coverage Review 必须证明：

```text
VALIDATION_OWNER_UNDEFINED = 0
TEST_COVERAGE_CONTRACT_GAP = 0
```

## Correctness and capacity separation

主 CI 只表达 correctness、security、schema compatibility、runtime/data-plane
isolation 和 required aggregate。lifecycle、readonly、history 的大规模
capacity evidence 统一由 `.github/workflows/capacity.yml` 负责，保留 smoke
作为 harness wiring proof，但不得把 smoke 报告为 formal capacity evidence。

## Compatibility separation

Pinned official CLIProxyAPI acceptance 迁移到
`.github/workflows/compatibility.yml`，名称明确为 upstream compatibility，
不得再描述为当前 production Node artifact acceptance。旧 owner 只有在新
workflow 至少实际 PASS 一次后才可删除。

## Change classification and required aggregate

`changes` job 只做保守的 docs/web/go/database/acceptance 粗粒度分类；不确定
影响时扩大验证而不是 skip。`CI required` 使用 `if: always()` 检查 classifier
决定应运行的 jobs：合法 skip 不失败，unexpected skip、failure、cancelled
均失败。最终 branch protection 可只依赖 `CI required`。

## Trigger matrix

| Change class | Minimum required validation |
| --- | --- |
| docs-only | lightweight docs/reference checks, `ci_required` |
| web-only | quality；无 Go/DB 影响时不运行 heavy PostgreSQL |
| Go/control | quality、race、相关 correctness；无法缩小时扩大 DB correctness |
| database | 全部 correctness PostgreSQL gates |
| acceptance/workflow | owning acceptance、workflow lint、`ci_required`；shared harness 变更时扩大到全部 acceptance |

## Diagnostics

Snapshot `runPrepare()` SHALL 暴露最早失败 checkpoint，例如
`prepare.open_owner`、`prepare.open_runtime`、`prepare.seed_fixture`、
`prepare.repository_init`、`prepare.claim_runnable`、`prepare.claim_identity`
和 `prepare.claim_fence`。History process failure SHALL 至少保留
`failed_test`、`phase` 和 sanitized fixed reason。Capacity failure SHALL
输出 test/scenario、scale、bounded metric summary 和 failure class。

## Release publication

`container.yml` 正式发布只由 `deploy-v0.<PHASE>.<REVISION>` tag 驱动。删除
无约束的 manual publication，或要求显式且经 regex 校验的 release tag，并确认
tag target 等于 build SHA；不得发布 generic `main` deployment tag。

## Runtime and rollback boundaries

本 change 不修改 production runtime、API、migration 或 schema。失败时回滚
对应 CI/docs commit 即可；不得移动已发布 deployment tag。任何发现真实
production defect、API/schema 变化或无法证明 replacement coverage 的情况都
必须停止并重新评估 release impact。

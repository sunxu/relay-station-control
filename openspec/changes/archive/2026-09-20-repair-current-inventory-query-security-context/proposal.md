# Proposal: 修复 Current Inventory Query 安全执行上下文

## Why

Migration 34 重建 `control_query_current_account_inventory_v1(...)` 时丢失了 Migration 9 compatibility contract 要求的 `TimeZone=UTC`，导致完整 forward schema 无法通过 `control_history_schema_compatibility_v1()`。

## What Changes

新增一个只恢复 function-local `TimeZone=UTC` 的 forward migration，并用 PostgreSQL catalog integration test 覆盖 fresh install 与 `51 → 52` upgrade。既有查询函数 body 与产品语义不变。

## Outcome

为 `control_query_current_account_inventory_v1(...)` 增加一个不可变的 forward migration，恢复既有 History compatibility contract 要求的 `TimeZone=UTC` function-local 配置。保留现有查询函数 body、字段投影、授权边界和产品语义不变，使完整 forward schema 恢复兼容。

## Scope

- Affected repository: `relay-station-control`
- Affected truth source: PostgreSQL forward migration chain and catalog contract test
- New migration: `00052_restore_current_inventory_query_timezone.sql`
- Validation: fresh install, `51 → 52` upgrade, catalog contract, History compatibility/process, readonly query regression

## Non-goals

- 不修改 Migration 9 或 Migration 34
- 不改变 current inventory query 的字段、过滤、分页、freshness 或 Node eligibility 语义
- 不改变 API、UI、History 行为或 runtime code
- 不做 production upgrade orchestration
- 不发布或移动任何 deployment tag

## Release and compatibility impact

这是尚未生产部署的 pre-production schema corrective。已发布 `deploy-v0.9.1` 和现有 artifact 不变；不创建 `deploy-v0.9.2`。Migration 52 仅恢复 function-local execution configuration，不做数据迁移。

## Security

保持 `SECURITY DEFINER`、`relay_control_migrator` owner、`search_path=pg_catalog` 和 `TimeZone=UTC`。测试直接读取 `pg_proc` catalog，不使用 secret 或 raw response。

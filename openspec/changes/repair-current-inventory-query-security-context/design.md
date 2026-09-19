# Design: 修复 Current Inventory Query 安全执行上下文

## Decision

KEEP EXISTING COMPATIBILITY CONTRACT。新增 migration 只执行：

```sql
ALTER FUNCTION public.control_query_current_account_inventory_v1(
  uuid, text, text, text, text, text, integer
) SET TimeZone = 'UTC';
```

不重建 function body。既有 catalog evidence 已证明 `SECURITY DEFINER`、owner 和 `search_path` 保持正确，因此不重复写入未失效属性。

## Migration and rollback

Migration 52 是 forward-only corrective，适用于 fresh install 和已执行到 51 的 schema。不得修改或重写 00009/00034。Down 仅在开发/test migration contract 中明确拒绝，不能作为 production rollback；生产回滚依赖停止新行为并保留 forward schema。

## Invariants

- `pg_proc.prosecdef = true`。
- owner 为 `relay_control_migrator`。
- `proconfig` 同时包含 `search_path=pg_catalog` 和 `TimeZone=UTC`。
- Current inventory query 的 projection、filter、pagination、freshness classification、ACL 和 audit semantics 不变。
- `control_history_schema_compatibility_v1()` 返回兼容结果。

## Validation design

最低 owning layer 是 PostgreSQL catalog/integration test。该 test 覆盖 fresh latest schema 和从 version 51 应用 forward migration 的 existing-schema upgrade。既有 readonly schema integration test 覆盖真实 repository projection 与权限矩阵；History schema/process acceptance 覆盖 compatibility consumer。Migration 不引入数据变更，因此不增加 backup/restore harness。

## Provenance and scope

本 change 只包含 migration、migration contract test 和 OpenSpec。不得携带 CI workflow、capacity、compatibility workflow 或其他未提交 stabilization draft。

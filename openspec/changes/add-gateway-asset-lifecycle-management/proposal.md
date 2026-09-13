## Why

Phase 6 已批准 Gateway & Relay Node Management Detailed Requirements。当前 Control 资产基础能力只能表达有限的 Gateway 记录，不能安全管理 active/retired 生命周期、current slot、历史 identity、原子 Replace、审计和 command replay。需要先建立 Gateway-owned shared foundation，供后续 Node changes 复用。

## What Changes

- 建立共享 `asset_admin_command_receipts`、canonical intent、advisory lock、revision、reason taxonomy 和 Phase 6 compatibility gate。
- 将 Gateway `instance_id` 提升为稳定行 identity，`singleton_id` 演进为 nullable current-slot marker。
- 增加 Gateway Register/Edit/Retire/Replace、Health 和 `/health` Connection Test 契约。
- 组合已归档 `internal-http-transport` baseline：Gateway management/Directory/Health target 仅允许 `http://`，`https://` 在 outbound 前 fail closed，不恢复 TLS 或 dual-protocol branch。
- 维护 Gateway historical rows、immutable replacement lineage、binding closure 和 Directory lifecycle fencing。
- 扩展 Asset Registry Gateway API/UI 读取与 lifecycle surface，不进入 data plane。

## Capabilities

### Modified Capabilities

- `asset-registry`
- `gateway-account-directory-ingestion`
- `relay-node-gateway-account-binding`

### New Capabilities

- `asset-admin-command`
- `gateway-asset-lifecycle`

## Impact

只修改 Control 的 forward migration、queries/sqlc、Store/service/API/UI、audit/metrics、测试和 runbook 规划；不直接实现本 change。本 change 是后续 Node lifecycle/operations changes 的 shared foundation。K1继续由受保护的`CONTROL_ASSET_INTENT_KEY_FILE`提供，不增加identity/digest anchor或deployment metadata，也不改变migration 33、compatibility class 1或floor 1。

## Non-Goals

- 不实现 Relay Node lifecycle 或 Node management operations。
- 不修改 Gateway/CLIProxyAPI 原生服务、Account/Group、routing 或 request data plane。
- 不创建 generic workflow/lifecycle framework。

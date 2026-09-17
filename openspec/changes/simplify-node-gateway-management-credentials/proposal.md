## Why

Phase 8 Stage 0 要把 Relay Node management credential 与 Gateway Directory credential 从运行时文件引用收敛为 Control 持久化的 protected-at-rest state，减少每资产 Secret 文件与引用编排，同时保持 Control 不进入数据面、Gateway/Node 原生职责和全部既有生命周期/命令语义。当前 Requirements、Architecture、Base TCCR 与 Compatibility Addendum 已冻结；本 change 只把这些批准结论翻译为可实施、可验证的 Control 计划，Implementation 仍为 NOT AUTHORIZED。

## What Changes

- **BREAKING**：资产写 API 移除 `reader_secret_ref`，分别引入 Node `management_credential` 与 Gateway `directory_credential`；读 API 继续只返回 `secret_configured`。
- 为两类 management credential 引入一个窄范围 protected-at-rest capability：外部 K2、AES-256-GCM、asset-bound AAD、数据库 protected columns/ACL 与 K2 identity commitment。
- 保持 K1 `CONTROL_ASSET_INTENT_KEY_FILE` 与既有命令等价用途不变；actor ownership 先于 target/credential/lifecycle/revision/remote processing，同 actor replay/conflict 可使用 K1 commitment，K2 不参与既有命令分类。
- Register/Edit/Replace 按冻结 tri-state 处理 credential，并将 set/clear、Retire erase、Replace predecessor erase 与既有资产/命令事务原子提交。
- Node Inventory、Phase 7 account operations 与 Gateway Directory ingestion 改用 protected credential resolver；生产路径不保留 `FileSecretResolver` fallback，Directory 协议、Node 原生管理协议和数据面均不变。
- 规划 forward migration `00051`，对 legacy non-null `reader_secret_ref` fail closed；不做 importer、backfill、dual-read、dual-write 或旧部署 rollback compatibility。
- 将兼容性提升到 class 4 / floor 4，扩展既有 signed manifest v1 gate；不引入 manifest v2。
- 最小适配生成 API/客户端与 Asset Registry UI，并补齐 K2 provisioning、backup/restore、wrong/missing K2、fresh local DB/re-register 和有界 acceptance evidence。

## Capabilities

### New Capabilities

- `asset-management-credential-protection`: 两类资产 credential 的 K2 bootstrap、AES-256-GCM sealed state、AAD、K2 identity commitment、Migration 00051、protected ACL、compatibility class/floor 4 与 fail-closed recovery 契约。

### Modified Capabilities

- `asset-registry`: 写入 credential、读取 `secret_configured` 与最小 Node/Gateway credential UX。
- `asset-admin-command`: actor-first、credential tri-state、intent v2、K1/K2 分工以及 credential-bearing command 原子性。
- `gateway-asset-lifecycle`: Gateway Directory credential 的 Register/Edit/Replace/Retire/Replace predecessor durable semantics。
- `relay-node-asset-lifecycle`: Node management credential 的 Register/Edit/Replace/Retire/Replace predecessor durable semantics。
- `gateway-account-directory-ingestion`: 在既有 endpoint/lifecycle/fencing contract 下解析 protected Directory credential，移除 legacy reference runtime path。
- `cliproxyapi-readonly-driver`: authenticated Node Inventory 调用改用 protected management credential，保持协议与解析契约不变。
- `account-admin-operation`: Phase 7 authenticated Node consumers 改用同一 protected credential resolver，保持 admission/replay/native mutation 语义不变。
- `runtime-acceptance-harness`: class-4 artifact、Migration 00051、sealed state/K2 commitment、runtime provenance 与最小代表性验收。
- `runtime-recovery-harness`: PostgreSQL 与 K2 联合备份恢复、正确/错误/缺失 K2、host migration 与 restart-only 生效契约。

## Impact

- Phase：Phase 8 Stage 0；结果：简化 Node/Gateway management credential handling。
- 产品实现仓库：`relay-station-control`；运行与跨仓真相依赖：`relay-station-ops`；`relay-station-gateway` 与 `relay-station-node-cliproxyapi` 无产品源码修改。
- 未来受影响真相源：`api/openapi.yaml`、Migration `00051`、受控 SQL/sqlc、生成 Go/TypeScript、Asset Registry UI、audit/metrics/secret scanner、compatgate、deployment/recovery Runbook 与 acceptance harness。
- Migration 计划：`00051`；兼容性：class `4` / floor `4`。这是 planning decision，本 change 不创建 migration、不产生 Stage 0 artifact/runtime evidence。
- 安全：K2 位于 PostgreSQL 外，plaintext 不持久化且不进入 ordinary API/UI/query/log/audit/metrics/trace/business surfaces；Provider/API/OAuth/access/refresh/password/DingTalk/raw response 等 Secret 边界不变。
- Rollout/rollback：fresh-install/no-importer；class-4 gate 与 floor-4/Migration-51 双向一致性先于 Control 启动。旧 class-3 artifact 在 floor-4 DB 上 fail closed；不支持旧部署 rollback、rolling upgrade、dual-read 或 dual-write。
- 数据面隔离：Control 仍不进入请求数据面，不修改 Gateway Account/Group 或路由，不改变 CLIProxyAPI Provider/Account/credential ownership、retry 或 cooldown。
- Non-goals：不建设 generic Vault、KMS plugin framework、rotation、provider/OAuth credential storage、Gateway Account/Group CRUD、scheduler、importer、legacy compatibility framework 或 Phase 8 后续 Stage 能力。

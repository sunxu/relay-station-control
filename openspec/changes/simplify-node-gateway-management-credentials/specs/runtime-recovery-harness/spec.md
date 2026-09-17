## ADDED Requirements

### Requirement: Recovery SHALL treat PostgreSQL and K2 as one recovery set
Backup、restore 与 host migration MUST 将 PostgreSQL protected state 与正确 K2 一起管理；不得从每资产 legacy Secret 文件重建。K2 变更仅在进程重启后生效，不支持 hot reload。

#### Scenario: database and correct K2 restore credential usability
- **WHEN** operator 恢复数据库及其匹配 K2 并重启 Control
- **THEN** Node 与 Gateway credential-dependent paths 均恢复，sealed state 与 K2 commitment 保持一致

#### Scenario: wrong or missing K2 fails closed
- **WHEN** operator 恢复数据库但提供错误或缺失 K2
- **THEN** credential-dependent paths不可用、credential-independent paths仍可用，且 sealed state/commitment 不被改写

### Requirement: Local transition SHALL use fresh database and re-registration
Stage 0 local/dev fixture transition MUST 使用 fresh local DB、正确 K2 provisioning 与 Node/Gateway re-register；不得建设 local legacy importer、backfill、dual-read 或 dual-write。

#### Scenario: legacy local database contains references
- **WHEN** local Migration 00051 guard发现 non-null legacy `reader_secret_ref`
- **THEN** transition停止并要求重建本地数据库与重新注册资产，不尝试自动导入

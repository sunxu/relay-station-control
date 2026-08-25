## Why

阶段 0 已固定 Control 的本地管理员认证、安全边界和技术栈，但当前 Control 只有健康检查与环境元数据骨架，尚不能安全地建立首个管理员、区分实名操作者或保护后续管理能力。阶段 1 必须先建立可审计的管理员访问基础，后续资产、采集和运维功能才能在明确的身份与会话边界内交付。

## What Changes

- 在 `control` 仓库新增一次性 bootstrap：运维人员使用运行时 Secret 创建首个实名 `super_admin`；成功提交后数据库永久关闭入口，重复和并发初始化均失败并产生安全事件。
- 新增本地管理员生命周期：固定且不可编辑的 `super_admin` 角色、管理员创建与一次性激活、启用/禁用、密码变更，以及服务身份与交互管理员身份隔离。
- 新增本地认证：Argon2id 密码哈希、统一失败响应、按账号与客户端来源限制失败尝试、生产环境强制 TOTP、单次显示且仅保存哈希的恢复码。
- 新增 PostgreSQL 服务端会话：随机 Cookie、CSRF 防护、空闲与绝对超时、注销、账号禁用后的会话撤销，以及高风险操作的短期重新认证证明。
- 新增认证与管理员操作审计基础：记录不可变操作者、动作、目标、结果、原因和请求关联信息，禁止记录密码、TOTP Secret、恢复码、会话令牌、激活令牌或运行时 Secret。
- 新增 bootstrap、登录、MFA、会话和管理员管理页面；生成的 Go 类型、TypeScript 客户端和 React Query Hooks 继续分别由 OpenAPI/Orval 生成，不手工修改。
- 本 change 属于阶段 1。非目标包括 RBAC/权限编辑器、OIDC/SSO、邮件邀请、双人审批、资产/Node/Gateway 管理、账号采集、异步任务框架和任何数据面改动。

## Capabilities

### New Capabilities

- `administrator-access`: 定义 Control 管理员 bootstrap、激活、本地认证、MFA、会话、固定授权、重新认证和安全审计的可观察行为。

### Modified Capabilities

无。

## Impact

- **受影响仓库**：仅 `control`。`ops` 中的 v1.0 系统设计与技术栈 ADR 作为既有真相源引用，不在本 change 中改写；`gateway` 与 `node-cliproxyapi` 不受影响。
- **OpenAPI 与生成物**：扩展 `api/openapi.yaml` 的认证、会话和管理员接口；运行 `make generate` 刷新 Go 服务端类型与 `web/src/api/generated/`，不得手工编辑生成文件。
- **Migration 与 sqlc**：新增不可变 Goose forward Migration，建立 bootstrap 状态、管理员、凭证、MFA、恢复码、激活令牌、会话、重新认证和审计表及约束；更新 `queries/` 并重新生成 `internal/store/sqlc/`。
- **UI**：新增 bootstrap、激活、登录、MFA 和管理员管理路由，使用路由级动态导入；未认证、MFA 未完成、已禁用和会话过期状态必须有明确但不泄密的用户反馈。
- **指标与审计**：新增固定低基数的认证结果、限流和活动会话指标；审计保留期沿用 v1.0 的 180 天初值。指标、日志和 Trace 不包含登录名、显示名、IP 原文或任何 Secret。
- **Runbook**：记录 bootstrap Secret 文件注入与移除、首个管理员恢复、管理员禁用、TOTP/恢复码处置、会话失效和数据库回滚步骤。
- **兼容性**：健康检查保持匿名可用；未来管理 API 默认要求已完成 MFA 的有效 `super_admin` 会话。当前无既有认证客户端，因此无外部 API 破坏性变更。
- **安全与回滚**：密码使用成熟 Argon2id 实现，令牌只保存不可逆摘要；回滚优先回退应用并保留新增表，只有确认无后续数据依赖时才执行 Goose down。bootstrap 已完成状态不得因普通应用回滚重新打开。
- **数据面隔离**：Control 仍不进入 AI 请求链路；认证、数据库或 UI 故障不得改变 Gateway 或 Relay Node 的现有请求处理。
- **依据**：v1.0 系统设计第 2.1、19、21.1、23（阶段 1）和 24.1 节，以及技术栈 ADR 第 3.3、5、8.2 节。

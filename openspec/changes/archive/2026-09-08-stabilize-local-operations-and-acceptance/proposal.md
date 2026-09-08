## Why

Phase 4 本地验收和部署依赖 `/Volumes/DevRAM/tmp` 中的一次性脚本，RAM 盘清理后无法按原命令复现，且脚本写死个人目录、账号来源和单 Node 假设。需要将必要流程固化到已有仓库入口，保留可复现证据，避免继续维护独立部署脚本。

## What Changes

- 将 Directory/Binding HTTP 验收整理到 Control `deploy/acceptance/`，显式输入目标身份和受保护配置；默认只读检查，显式操作模式才允许既有 bind 或故障演练。
- 在 ops `dev/devctl` 复用现有 Compose、版本检查、Secret 和健康检查，增加同迁移基线内的单服务更新、备份、Directory 开关及显式中断恢复操作，取代重复临时部署逻辑；不新增另一套 CLI。
- 固化自然 stale/recovery、精确 Binding 身份、源 v1、数据面独立、失败恢复和脱敏证据格式；不把业务状态写入本地文件作为新真相。
- 新 Runbook 记录旧临时脚本到正式入口的映射及复现方式。一次性 OpenSpec 同步/归档脚本不产品化；旧临时文件只有验证替代入口后才可按用户授权清理。

## Capabilities

### New Capabilities

无新增产品能力。此 change 是运维/验收工具固化，使用 `skip_specs: true`；CLI 使用与验收约束在 design/tasks 中明确。

### Modified Capabilities

无。Directory、Binding、Node 管理传输、Topology 的既有产品 contract 保持不变。

## Impact

- 规划归属 Control；实施涉及 Control `deploy/acceptance/`、Runbook，以及 ops `dev/` 的现有工具与文档，按仓库独立提交。Gateway/Node 产品代码不变。
- 依据 System Design v1.8/R4.7 的 Control 只观察与显式关联边界、ADR-0002 §2.2/2.3/2.5；只面向显式指定的本地开发 Compose 环境，不增加 staging/production 发布流程。
- OpenAPI、Go/TS generated client、migration、sqlc、数据库 schema、产品 metrics、audit schema、UI 均无变更；0 migration。工具调用现有 API 时保持正常认证、MFA、CSRF 和已有审计。
- 不引入新依赖、调度器或 Secret 存储。凭据/账号文件/会话/数据库备份仍位于仓库外受保护目录，输出仅固定分类与非敏感版本/时间信息。
- HTTP/HTTPS 管理出站采用已批准产品策略；工具访问 Control 登录接口仍验证既有 TLS，不把产品专用 transport 的证书例外扩大到登录客户端。
- 不承诺旧临时命令长期兼容；保留历史归档原文，新文档提供替代映射。不得修改已归档 change、自动重放 bind/rebind、删除数据或顺带升级远端依赖。

架构评审与修订已完成，规划基线保存于 `4242227`。用户随后授权继续实施；当前按 tasks 逐项实现和记录新验收证据，未完成项不得以旧归档结果替代。

## Why

阶段 1 已完成管理员认证基础，但 Control 仍缺少本环境 Gateway、Relay Node、Driver/capability、Provider 策略和账号监控区间的数据库真相源，无法在不接触外部系统的前提下提供可审计的资产视图。现在需要先固化资产注册边界和只读管理面，为后续持久任务、只读 Adapter 与账号采集 change 提供稳定外键和策略版本。

## What Changes

- 在 `control` 仓库新增资产注册基础，复用现有 `environments` 单例；**BREAKING（部署配置）**：新增必填 `CONTROL_ENVIRONMENT_ID`，并在启动时校验它及现有 `CONTROL_ENVIRONMENT` 与数据库环境 ID/类型一致，不一致时 fail closed。
- 新增本环境唯一 Gateway、Relay Node、Node Driver、固定 capability、不可变 Provider 策略版本/绑定/激活区间，以及 Node 账号监控激活区间的数据模型和数据库约束。
- 通过只读、受 `super_admin` 会话保护的 OpenAPI 接口和 React 页面展示资产、能力、当前策略与监控状态；本 change 不提供浏览器或产品 API 的资产写入入口。
- 只在数据库保存经校验的外部 endpoint 和 Secret 引用；API、UI、日志、指标与审计不得返回 Secret 引用或凭证内容。
- 新增固定低基数的资产注册聚合指标、环境不匹配/读取失败诊断，以及 Migration、sqlc、HTTP、前端和 PostgreSQL 18 验证。
- 更新阶段 1 运维 Runbook，说明资产由受控 Migration/部署流程登记、Provider 策略不可变、应用回滚保留表及环境不匹配处置。
- 不新增对 Gateway、Relay Node、Prometheus 或其他外部服务的调用，不读取账号清单，不创建 Worker/Outbox，不修改 Gateway 配置，也不进入请求数据面。

## Capabilities

### New Capabilities

- `asset-registry`: 定义单环境资产真相源、不可变策略与激活区间、受认证的只读资产 API/UI、Secret 隔离和数据面零副作用边界。

### Modified Capabilities

无。现有 `administrator-access` 的固定授权边界保持不变，本 change 仅复用该边界保护新增管理接口。

## Impact

- **阶段与结果**：阶段 1；运维人员和实名 `super_admin` 可以确认 Control 当前环境、唯一 Gateway、已登记 Node、Driver 能力、当前 Provider 策略及账号监控是否激活。
- **仓库**：只修改 `control`；`ops` 的 v1.0 系统设计、ADR 和阶段 0 Provider 策略是输入真相源，本 change 不修改 Gateway、Node 或 `ops` 产品部署状态。
- **OpenAPI/生成物**：扩展 `api/openapi.yaml` 的只读环境/资产接口，并同步 oapi-codegen、Orval 和契约测试；现有认证与健康接口保持兼容。
- **Migration/sqlc**：新增一个 additive、合并后不可修改的 Goose forward Migration，以及资产/策略/激活区间查询和 sqlc 生成物；现有环境与认证表不重建、不回填身份数据。
- **安全与审计**：产品 API 无资产写操作；所有列表/详情接口要求现有管理员会话。数据库可保存 opaque Secret 引用，但响应、日志、指标、Trace 和审计不得暴露引用值、凭证或未脱敏 endpoint 查询内容。
- **指标**：只增加固定 `asset_kind`/`result` 枚举的聚合数量和读取结果，不使用 environment ID、instance ID、endpoint、Node 名称、Driver 名称、policy version 或 Secret 引用作为标签。
- **UI/Runbook**：新增懒加载只读资产页及空状态、环境不匹配和读取失败展示；补充登记、策略版本、回滚和恢复说明。
- **兼容与回滚**：HTTP 变更仅新增路径；部署前必须把数据库已有 `environment_id` 配置为 `CONTROL_ENVIRONMENT_ID`，否则新版本拒绝启动。应用回滚保留新增表和策略历史。只有全新且无资产/策略/激活记录的数据库才允许人工 down，普通回滚不得改变环境单例或删除历史区间。
- **数据面隔离**：本 change 不建立任何出站 Adapter 或配置写路径；Control 或资产数据库故障只能使管理视图不可用，不得影响 Gateway/Node 已有请求。

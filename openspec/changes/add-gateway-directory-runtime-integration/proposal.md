## Why

Phase 4 的 Directory store/service 已实现，但 Control 主程序没有构造或运行它；实际部署无法获得 accepted Directory，也无法通过既有 Binding API 完成关联。当前开发 runbook 初始登记的 Gateway 为 HTTP 且 reader reference 为空，与现有 Directory HTTPS-only 要求不匹配（本次按用户决定修订该边界），现有 registrar 不能补全登记。

## What Changes

- 明确独立5s attempt与有界失败收尾；NULL reader reference在共同调度行锁内返回no-work，避免首次接入被自动生成的run锁死；单Gateway故障不阻断其他Gateway。
- 将已有 Directory scheduler、worker、reconciler 和 metrics 接入 Control 生命周期，默认关闭；保持 180s cadence、540s freshness 和既有 durable lease/fencing 语义。
- 通过现有 SecretResolver 读取 Directory 专用 token；直接支持 HTTP/HTTPS，不设origin白名单，HTTPS不验证证书链、有效期或主机名。统一修改Control的Gateway/Node管理出站transport；保留禁止redirect、请求预算与响应验证。HTTP明文且HTTPS不认证服务端，部署方承担网络和目标可信性。
- 新增仅限首次接入的受控 Gateway 配置补全操作：仅对无任何 Directory 运行/观测/快照和无任何 Binding 历史的同一 Gateway 仅将NULL reader reference补填为非空opaque reference，不修改endpoint或替换既有reference；保留稳定 UUID，现有 register 契约不变。
- 以既有 Binding action 的显式管理员操作验证最终 resolved 结果；不新增 Binding 能力，不自动绑定，不改变 Topology。

## Capabilities

### New Capabilities

- `control-management-outbound-transport`：Control当前及后续Gateway/Node管理出站调用的统一传输边界。

### Modified Capabilities

- `gateway-account-directory-ingestion`：默认关闭的进程生命周期接线、直接HTTP/HTTPS与Secret配置、恢复和本地接入验收。
- `cliproxyapi-readonly-driver`：移除目标许可/特殊地址拒绝及证书验证，保留只读方法、Secret和响应边界。
- `asset-registry`：仅首次接入的 Gateway 配置补全、并发与权限边界。

## Impact

主要仓库为 Control；后续实施中 ops 仅调整本地 HTTP Compose与部署文档，Gateway 仅配置既有 Directory 功能，不修改 Gateway source v1 或 Node 代码。依据 System Design v1.8/R4.7 和 ADR-0002 §2.1–2.2、R4.6/R4.7，数据面仍完全由 Gateway/Node 原生调度。

OpenAPI、generated Go/TS、UI 与 Binding HTTP identity contract 无变更；Account ID 仍为 source numeric int64 → Control/Web decimal string。新增一个最小 forward migration 用于仅填空reference的受控函数及必要权限/行级并发保护；不新增 Directory truth、table、column 或历史副本，不修改既有 migration。sqlc 仅按实际新增 query 生成，优先复用已有 queries/service。metrics 复用已有 Directory collector；补全操作与既有audit_logs的固定action在同一事务内提交，需最小扩展既有审计allowlist/shape；禁止写入凭据、引用或endpoint，Down保留历史兼容约束，详见design。

兼容性为默认关闭的 additive runtime 与受限运维入口；已有采集/绑定数据的 Gateway 不允许借此迁移目标。回滚关闭新 runtime 并保留 forward schema、资产与历史，旧二进制不支持 HTTP 时必须关闭采集，不能假称回滚后仍可采集。该契约已通过 Architecture Contract Re-review（2026-09-07，P1=0、P2=0）；本轮不实现、不执行 migration、不改本地运行状态、不修改已归档 Topology change。

本次安全边界由用户明确扩大到Control发起的所有Gateway/Node管理接口调用，包括健康、版本元数据和账号采集，以及未来新增管理客户端。移除origin/DNS/IP/CIDR许可、特殊地址拒绝和HTTPS证书验证；不改变Control入站TLS/登录、Gateway→Node数据面或模型提供商连接。此项是现有Node安全契约的breaking correction，不能描述为纯additive；无数据库或HTTP DTO变更。

精简范围：仅enabled与secret mapping两项运行配置；不新增HTTP开关、origin白名单、自定义CA配置、endpoint编辑、通用transport factory或全表锁。权限、原子审计、恢复、精度和数据面隔离验收保留。

实施精简：runtime仅ReconcileTick→WorkOnce，复用每项内已有调度；Node使用标准拨号和原专用transport；补填仅SQL模板；复用测试fixtures与统一Runbook交付。能力和验收范围不缩减。

真实runtime验收补充：同一最小migration提供有效run/fencing限定的目标只读函数，补足opaque reader reference安全读取；不扩大直接SELECT权限。

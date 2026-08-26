# CLIProxyAPI 只读 Node Driver 运行手册

## 1. 边界与当前状态

本手册适用于 `cliproxyapi` management 只读 Driver 基础。Driver 只能表达两个操作：

- `GET /healthz`，不携带 Management Key；
- `GET /v0/management/auth-files`，仅以 `X-Management-Key` 携带运行时解析的 Key。

当前版本没有 poll-run 调度器、HTTP 调用入口或持久化账号状态。Control 启动只建立固定 Driver registry，不自动请求 Node，不创建 durable job/Outbox，不修改资产、Node 或 Gateway。

Driver 只表示一次内存观察。`/healthz` 成功不代表账号池完整、Provider 可用、Gateway enabled 或 Node 可调度。

生产启动使用显式开关 `CONTROL_CLIPROXYAPI_DRIVER_ENABLED`，默认为 `false`。关闭时 registry 为空且不能根据数据库字符串动态加载 Driver；开启时只登记 `cliproxyapi.auth-files.v1` 和两项只读 capability。两种状态都没有自动调用者。

## 2. 部署前置条件

只有在以下条件全部满足时，未来的显式调用者才能构造 Driver：

1. 资产的 `node_type` 为固定 `cliproxyapi`，Driver 合约版本与代码登记值相同，并声明需要的 `management_health_read` 或 `management_account_inventory_read` capability。
2. management endpoint 是无 query/fragment/userinfo 的绝对 `https` URL；只有隔离的 management 网段才可显式允许 `http`。
3. DNS allowlist 使用精确 hostname 或明确 suffix，management CIDR 使用规范网络地址。plain-HTTP CIDR 必须是 management CIDR 的更窄子集。
4. opaque Secret 引用映射到容器中的安全普通文件。映射和 Secret 文件不得是 symlink/目录，owner 必须是 root 或 Control 运行 UID，group/other 不得具有任何权限，内容必须非空且有界。
5. HTTPS 使用系统信任根或显式 CA bundle，证书必须对 endpoint 的原始 hostname 有效。

任一缺失、矛盾、越界或未登记值都必须 fail closed，不得把 Secret 引用文本当作 Key，不得从数据库字符串动态加载 Driver。

### 2.1 Control 启动配置

| 环境变量 | 默认值 | 约束/说明 |
|---|---:|---|
| `CONTROL_CLIPROXYAPI_DRIVER_ENABLED` | `false` | `true` 时才构造固定 Driver/registry；仍不自动发请求 |
| `CONTROL_CLIPROXYAPI_MANAGEMENT_DNS` | 无 | 逗号分隔；小写 exact DNS，前导 `.` 表示仅子域 suffix；开启时必填 |
| `CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS` | 无 | 逗号分隔的规范 CIDR；开启时必填 |
| `CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS` | 无 | 可选；必须完全位于 management CIDR 内 |
| `CONTROL_CLIPROXYAPI_SECRET_MAPPING_FILE` | 无 | 开启时必填的 protected JSON mapping 绝对路径 |
| `CONTROL_CLIPROXYAPI_CA_FILE` | 系统信任根 | 可选 PEM CA；普通文件、不跟随 symlink、不可 group/other 写、最大 1 MiB |
| `CONTROL_CLIPROXYAPI_CONNECT_TIMEOUT` | `3s` | `100ms..10s` |
| `CONTROL_CLIPROXYAPI_REQUEST_TIMEOUT` | `15s` | `1s..30s`，且不得小于连接超时 |
| `CONTROL_CLIPROXYAPI_HEALTH_MAX_BYTES` | `65536` | `128..1048576` |
| `CONTROL_CLIPROXYAPI_INVENTORY_MAX_BYTES` | `5242880` | `1024..5242880` |
| `CONTROL_CLIPROXYAPI_INVENTORY_MAX_RECORDS` | `1000` | `1..1000` |
| `CONTROL_CLIPROXYAPI_SECRET_MAX_BYTES` | `4096` | `1..65536` |
| `CONTROL_CLIPROXYAPI_SECRET_MAPPING_MAX_BYTES` | `65536` | `1..1048576`，且不得小于 Secret 上限 |

开启后，任一配置非法都在 PostgreSQL 连接和 HTTP listener 前以固定 `component=node_driver reason=invalid_runtime_config` 结束。日志不包含环境变量原值。

## 3. 网络与反向代理

- Control 与 Node management 接口使用独立网络路径；Gateway 和浏览器不能访问该接口。
- Driver 的专用 Transport 固定为无代理、无 Cookie jar、无自动重试、拒绝所有重定向。`HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` 及小写变体不能影响它。
- 每次连接都重新解析 DNS，并要求全部解析 IP 通过 allowlist。Driver 只拨号本次已验证 IP，不从混合 DNS 结果中挑选“看似可用”的地址。
- loopback、link-local、unspecified、multicast 和云元数据地址无条件拒绝，即使误配的宽 CIDR 包含它们。
- 若在 Node 前方放置反向代理，只允许上述两个精确 GET；auth-files 路径的认证 header 不得被记录，其他 method/path 应在边缘层拒绝。

## 4. 限制、失败与恢复

默认连接超时为 `3s`，总请求超时为 `15s`。健康响应使用小型上限；auth-files body 默认最大 `5 MiB`、记录最多 `1,000`条。超限、非 `200`、JSON/`files` 形态非法或 inventory mode 无法判定时，不返回部分结果。

运行时诊断只能使用固定 reason，不得记录 endpoint、hostname/IP、Secret 引用/路径/Key、provider/email、版本头、请求/响应 header/body 或原始错误。指标只允许封闭的 `node_type|operation|result` 低基数维度。

故障恢复不需要清理缓存或重启：

1. 修复 Secret 文件权限/内容、DNS、allowlist、路由或证书；
2. 使用下一次显式调用重新执行 Secret/DNS/IP/TLS 全套验证；
3. 不要为未知结果手工重放或将 Driver 登记为 durable-job Executor。

Control/Driver/management 网络停止时，只读观察暂停；Gateway 与 Relay Node 已有数据面请求应继续。

## 5. Secret 轮换

1. 在受保护位置原子替换 Secret 文件，保留相同 opaque 引用映射；新文件必须满足普通文件、owner-only 写入和大小边界。
2. 先在 Node/反向代理激活新 Key，再替换 Control 侧文件；若上游支持短暂双 Key，在窗口结束后立即撤销旧 Key。
3. 下一次显式 inventory 调用重新读取文件，无需重启 Control。
4. 验证只保留固定结果与计数；不得将新旧 Key、引用、文件路径或响应复制到日志/工单。

## 6. 发布与回滚

1. 先验证 registry、allowlist、Secret 映射和 CA 配置，再发布 Control。配置不合法应在 listener 前失败。
2. 在隔离测试服务完成 DNS/IP、Host/SNI、CA、redirect、proxy、body/record limit 和 timeout/cancel 验收。
3. 官方镜像或真实测试 Node 冒烟最多两个 Node，所有 management 请求串行，任意相邻请求间隔至少 `10s`。不修改 Node 配置或账号。
4. 应用回滚只回退 Control 二进制和 Driver 配置。本基础无 Migration/sqlc/OpenAPI/UI 变更，不执行数据库 down，不修复 Node。

## 7. 无 Secret dry run

下列命令不需要 endpoint、Management Key 或 PostgreSQL，且不使用本地代理：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export npm_config_proxy='' npm_config_https_proxy='' npm_config_noproxy='*'
export npm_config_registry='https://registry.npmmirror.com'

go test ./internal/drivers/... ./cmd/control -count=1
go test -race ./internal/drivers/... ./cmd/control -count=1
make generate
git diff --check
```

OpenSpec 验证使用已安装 CLI，同样不需要 Node 凭据：

```sh
openspec validate add-control-cliproxyapi-readonly-driver --strict
openspec validate --specs --strict
```

官方镜像/真实 Node 冒烟不是 dry run；只能使用受控验收脚本执行，不得为了手工调试绕过 allowlist、TLS 或 `10s` 限速。

## 8. 官方镜像/受控 Node 冒烟

`deploy/acceptance/cliproxyapi-readonly-smoke.sh` 是本 change 唯一允许的直接 management 冒烟入口。它最多接受两个 Node，用全局锁防止并发，并调用与 Control 相同的 Go Driver/Secret resolver/SSRF/DNS/TLS/parser 路径。每次请求结束后无条件等待 `10s`，包括 HTTP 失败和超时。它仅输出 node 序号、固定 result/reason、mode 和有界计数；原始 body/header/error 只在有界内存中处理，不落盘。

不得使用 `ops/phase0ctl check` 或 `deploy/acceptance/control-auth-e2e.sh data-plane|all` 代替本冒烟：它们的 health/auth-files/models 管理请求不具备该全局 `10s` 间隔。

配置只使用受保护环境变量、owner-only Secret mapping/Secret 文件和现有 Provider 策略快照，不要把 endpoint、Secret 引用或 Key 写入命令行、shell history 或验收记录：

```sh
export CONTROL_DRIVER_SMOKE_NODE_1_ENDPOINT='set-in-protected-shell-environment'
export CONTROL_DRIVER_SMOKE_NODE_1_SECRET_REFERENCE='set-in-protected-shell-environment'
# Node 2 可选；endpoint 与 opaque Secret reference 必须成对设置。
export CONTROL_DRIVER_SMOKE_NODE_2_ENDPOINT='set-in-protected-shell-environment'
export CONTROL_DRIVER_SMOKE_NODE_2_SECRET_REFERENCE='set-in-protected-shell-environment'
export CONTROL_DRIVER_SMOKE_MANAGEMENT_DNS='node-1.example.invalid,node-2.example.invalid'
export CONTROL_DRIVER_SMOKE_MANAGEMENT_CIDRS='set-to-canonical-management-cidrs'
export CONTROL_DRIVER_SMOKE_PLAIN_HTTP_CIDRS='' # 仅隔离 management 网需要
export CONTROL_DRIVER_SMOKE_SECRET_MAPPING_FILE='/run/control/cliproxyapi-secret-map.json'
export CONTROL_DRIVER_SMOKE_CA_FILE='/run/control/management-ca.pem' # 可选
export CONTROL_DRIVER_SMOKE_PROVIDER_POLICY_ID='set-to-current-policy-uuid'
export CONTROL_DRIVER_SMOKE_ACTIVE_PROVIDERS='antigravity'
export CONTROL_DRIVER_SMOKE_OUT_OF_SCOPE_PROVIDERS=''

deploy/acceptance/cliproxyapi-readonly-smoke.sh
unset CONTROL_DRIVER_SMOKE_NODE_1_ENDPOINT CONTROL_DRIVER_SMOKE_NODE_1_SECRET_REFERENCE
unset CONTROL_DRIVER_SMOKE_NODE_2_ENDPOINT CONTROL_DRIVER_SMOKE_NODE_2_SECRET_REFERENCE
```

实际顺序固定为 Node 1 health、等待、Node 1 auth-files、等待、Node 2 health、等待、Node 2 auth-files、等待。脚本失败后人工重跑前也必须确保上一次请求已经结束至少 `10s`；异常杀进程可能留下锁目录，只有确认无运行中冒烟后才能清理它。

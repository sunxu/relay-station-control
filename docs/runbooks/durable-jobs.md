# Control 持久任务运行手册

## 1. 适用范围

本手册适用于阶段 1 的 PostgreSQL 持久任务基础。PostgreSQL 是任务、执行租约、生命周期事件和事务 Outbox 的唯一真相源；进程内 wake signal 和未来可选的 Redis Publisher 只缩短发现延迟。

当前生产 registry 为空，不包含 Gateway、Relay Node、账号采集、历史压缩、Docker、GitHub、SSH 或其他外部操作执行器。默认入队语义将 Outbox 标记为 `suppressed/publisher_disabled`，不要求部署 Redis。

## 2. 运行配置

| 环境变量 | 默认值 | 允许范围 | 说明 |
|---|---:|---:|---|
| `CONTROL_JOB_WORKER_CONCURRENCY` | `4` | `1..32` | 进程内 Execute Worker 上限 |
| `CONTROL_JOB_RECONCILER_CONCURRENCY` | `2` | `1..32` | 进程内 Verify/Rollback 上限 |
| `CONTROL_JOB_POLL_INTERVAL` | `1s` | `100ms..1m` | PostgreSQL 可执行任务扫描间隔 |
| `CONTROL_JOB_RECONCILE_INTERVAL` | `5s` | `1s..1m` | 可恢复任务扫描间隔 |
| `CONTROL_JOB_DATABASE_BACKOFF` | `5s` | `1s..1m` | 数据库故障后的循环退避 |
| `CONTROL_JOB_SHUTDOWN_GRACE` | `8s` | `1s..30s` | 停止认领后等待当前 goroutine 退出的上限 |

任一值解析失败或超界时 Control 在开放 HTTP listener 前 fail closed，只记录固定 `component=jobs` 和 `reason=invalid_runtime_config`，不打印环境变量原值。

任务类型自己的 timeout、lease、最大 Execute attempt、是否可安全重放和是否允许 rollback 由不可变 `async_job_kinds` 记录固定，并复制到每个任务。修改进程级并发或扫描间隔不能改变已入队任务的执行策略。

## 3. 正常状态

任务状态：

```text
pending -> running -> verifying -> succeeded
   |          |           |
   |          +----------> retry_wait -> running
   |          |           +---------> rolling_back -> rolled_back
   |          +---------------------> failed
   +--------------------------------> cancelled
```

- `pending`：已原子提交，等待 Worker。
- `running`：Worker 持有有效 lease/fencing token；Execute 结果尚未确认。
- `verifying`：只允许 Reconciler 调用 Verify，不能再次直接 Execute。
- `retry_wait`：已明确证明本次没有副作用，可以在 `available_at` 后再次 Execute。
- `rolling_back`：只允许已登记且支持 rollback 的任务进入。
- `succeeded|failed|rolled_back|cancelled`：不可离开的终态。

`async_job_events` 是不可更新、不可删除的机器生命周期证据；实名管理员发起的未来业务操作仍必须同时写既有 `audit_logs`。

## 4. 管理视图和指标

有效 `super_admin` 可以访问：

- `GET /api/jobs`
- `GET /api/jobs/{job_id}`
- 管理页面 `/jobs`

接口只返回公开 ID、固定 kind/status、attempt、UTC 时间、固定错误码、Outbox 聚合状态和脱敏事件。payload、payload hash、幂等键、lease owner/token、Outbox envelope 和错误摘要不会投影到 API/UI。

Prometheus 指标：

```text
relay_control_async_jobs{status}
relay_control_async_job_oldest_pending_seconds
relay_control_async_job_expired_leases
relay_control_outbox_oldest_pending_seconds
```

除封闭 `status` 外不使用标签。job kind、job ID、operation ID、任务参数和错误内容不能成为指标标签。

## 5. 故障处置

### 5.1 最老 pending 超过 5 分钟

1. 查看 `/jobs?status=pending` 和 `/jobs?status=retry_wait`，确认 `available_at`、attempt 和固定错误码。
2. 检查 Control 到 PostgreSQL 的连接、连接池和数据库锁等待。
3. 检查数据库是否存在 Control 当前二进制未注册的 active job kind；启动门禁正常情况下会以 `registry_mismatch` 阻止这种版本组合运行。
4. 不要直接把任务改为 `succeeded`，不要重置 attempt，不要删除事件来消除告警。

### 5.2 存在过期执行 lease

1. 确认 Reconciler 正在运行且 PostgreSQL 可用。
2. 在任务详情查看最近 `verification_started`/`rollback_started` 事件。
3. Reconciler 必须先用稳定 operation ID 验证实际状态；结果未知时只允许有限验证重试，不能直接重复 Execute。
4. 验证预算耗尽后任务进入 `failed` 人工处置。当前 change 不提供 HTTP 重试/取消入口，不得使用临时 SQL 绕过状态机。

### 5.3 Outbox pending 或 publishing 积压

- 当前 PostgreSQL-only 部署中的新任务应为 `suppressed/publisher_disabled`，不会形成 pending 积压。
- 未来启用 Publisher 后，通知语义为 at-least-once；发布成功但回写前崩溃可以产生相同 event ID 的重复通知。
- Worker 始终轮询 PostgreSQL，因此通知丢失、重复、乱序或 Publisher 不可用不能阻止任务推进。
- 不要把 Redis/Publisher 状态当作任务完成证据。

### 5.4 PostgreSQL 中断

Worker、Reconciler 和只读任务 API 会暂停或返回脱敏 `503`，不会把任务伪造为成功。循环按 `CONTROL_JOB_DATABASE_BACKOFF` 退避；数据库恢复后无需清理内存队列或重启即可继续扫描。

Control/PostgreSQL 故障不得影响 Gateway 或 Relay Node 已有请求。若数据面同时故障，应按各自 Runbook 独立排查，禁止通过修改 Control 任务状态恢复流量。

### 5.5 优雅停机与崩溃

收到 `SIGTERM`/`SIGINT` 后，HTTP server 和任务循环停止接收新工作；当前执行 context 被取消并在配置的 grace 内 drain。无法确认的工作保留持久 lease，重启后由 Reconciler 验证，而不是由停机钩子猜测成功或失败。

强制终止同样不会丢失已提交任务、事件或 Outbox；恢复依据仅来自 PostgreSQL。

## 6. 发布、回滚和 Migration

1. 先以 Migration owner 执行 Goose up，再发布新应用。
2. 新版本启动时读取 active job kind 目录；数据库与代码 registry 不一致会 fail closed。
3. 应用回滚只回退二进制和前端，保留任务四表及全部证据。
4. 重新升级后从持久状态恢复，不手工重放未知外部操作。
5. Goose down 仅允许 `async_job_kinds`、`async_jobs`、`async_job_events` 和 `operation_outbox` 全空的全新数据库；存在任一记录时数据库拒绝 down。

生产普通回滚禁止执行 down、`TRUNCATE`、删除任务或禁用保护 trigger。需要引入首个真实 job kind/Executor、Redis Publisher、人工取消/重试入口或证据保留清理时，必须另建 OpenSpec change。

## 7. 安全边界

- 任务 payload 只能使用已注册、严格、版本化且最大 64 KiB 的 JSON object schema。
- 字段名中的 password、secret、token、credential、authorization、cookie、private key、API key、raw response 和 command 等敏感形态会被拒绝。
- 运行时角色不直接拥有任务表通用写权限，只能执行批准的 `SECURITY DEFINER` 状态函数和脱敏读取。
- 日志不得记录 payload、任意 `error.Error()`、SQL 参数、连接串、URL、命令或外部响应。
- 本阶段生产 Executor registry 为空；任何 Gateway/Node/互联网调用都属于范围越界。

## 8. 无本地代理验收

下面的命令均先移除本地 HTTP(S)/ALL proxy。npm registry 固定为项目批准的镜像；命令不会把数据库口令、Cookie 或任务内容放进命令行参数。

### 8.1 代码与生成物门禁

在仓库根目录执行：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export npm_config_registry='https://registry.npmmirror.com'

make generate
go test -race ./internal/jobs ./internal/store ./internal/api ./cmd/control -count=1
(
  cd tools
  go test ./...
)
(
  cd web
  npm test
  npm run typecheck
  npm run build
)
git diff --check
```

PostgreSQL 集成测试只允许使用可创建和删除隔离数据库的专用测试服务。连接口令放在 mode `0600` 的 `PGPASSFILE`，服务参数放在 `PGSERVICEFILE`；环境变量只引用服务名，不含凭据：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export PGSERVICEFILE='/run/control-test/pg_service.conf'
export PGPASSFILE='/run/control-test/pgpass'
export CONTROL_DATABASE_TEST_URL='postgres://?service=control-test-owner'
export CONTROL_RUNTIME_DATABASE_TEST_URL='postgres://?service=control-test-runtime'

go test -race ./internal/store ./internal/api -count=1
```

不得把上述测试服务指向生产数据库。发布时 Goose 同样只引用受保护的 service 配置；不要向 `psql` 或 Goose argv 传入带口令的连接串：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export PGSERVICEFILE='/run/control/pg_service.conf'
export PGPASSFILE='/run/control/pgpass'
export DATABASE_URL='postgres://?service=control-migrator'

make migrate-up
```

### 8.2 生产只读冒烟

先以 `umask 077` 准备只含当前管理员会话的临时 Cookie jar，并通过环境变量提供 Control 地址和 CA 文件。下列冒烟只发出 `GET`，最大页长固定为 `200`；响应和响应头只写入受保护的临时文件，并在退出时删除：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
: "${CONTROL_BASE_URL:?set the Control origin}"
: "${CONTROL_CA_FILE:?set the CA file}"
: "${CONTROL_ADMIN_COOKIE_JAR:?set the protected Cookie jar}"

umask 077
job_body="$(mktemp)"
job_headers="$(mktemp)"
trap 'rm -f "$job_body" "$job_headers"' EXIT HUP INT TERM

curl --fail --silent --show-error \
  --cacert "$CONTROL_CA_FILE" \
  --cookie "$CONTROL_ADMIN_COOKIE_JAR" \
  --request GET \
  --dump-header "$job_headers" \
  --output "$job_body" \
  "$CONTROL_BASE_URL/api/jobs?limit=200"

awk 'BEGIN { IGNORECASE=1; ok=0 } /^Cache-Control:[[:space:]]*no-store/ { ok=1 } END { exit !ok }' "$job_headers"
jq -e '
  ([.. | objects | keys[]]
    | map(select(. == "payload" or . == "payload_hash" or
                 . == "idempotency_key" or . == "lease_owner" or
                 . == "lease_fencing_token" or . == "error_summary" or
                 . == "outbox_envelope"))
    | length) == 0
' "$job_body" >/dev/null
```

若列表非空，可从受保护响应中选择一个公开 `job_id` 再对详情执行相同的 `GET`、`no-store` 和禁用字段检查。不要把 ID、响应正文或 Cookie 复制进工单或验收证据。任务 API 没有创建、重试、取消或删除入口；冒烟过程中若发现任何写方法可用，立即停止发布。

自动化的 `TestDurableJobHTTPReadOnlySecurityPaginationAndRecovery` 还会注入 canary，验证其不进入响应正文、响应头、结构化日志或审计；`TestCollectorHasOnlyClosedStatusLabel` 和 `TestStructuredLogContractRejectsUnregisteredDimensions` 验证指标/日志维度白名单；Web 组件测试验证禁用字段和 canary 不进入浏览器 DOM。

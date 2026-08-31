# Control 账号清单 Poll Run 运行手册

## 1. 适用范围与安全边界

本手册适用于阶段 2 的账号清单 poll-run foundation。PostgreSQL 的 UTC 时间、唯一 Node/槽记录、pinned Provider policy、lease/fencing 和终态聚合证据是唯一真相源。Poll service 只允许调用现有 Node Driver 的 `ListAccountInventory` 固定只读操作；不得调用 Probe、Gateway、模型数据面、Node 写接口或任意 URL。

本 change 只保存 Node/Provider 聚合结果，不保存账号明细、email、account key、endpoint/IP、Secret 引用或值、Management Key、header/body、原始错误，也不创建 snapshot/current state、promotion、missing/out-of-scope 生命周期、压缩、告警、OpenAPI/UI 或人工重试入口。自动 poll 不是实名管理员操作，不写管理员 audit row。

Migration 9 之后，已终态 poll 只有在对应历史压缩完成、snapshot 已清空且达到固定 30 天保留期后，才可由 history retention 受控删除；普通 poll 运行时仍不得删除。顺序、暂停和恢复见 [`account-inventory-history-compaction.md`](account-inventory-history-compaction.md)。

## 2. 固定槽与状态机

`scheduled_at` 只能由 PostgreSQL UTC 时钟计算并按 300 秒对齐。Scheduler 只创建仍处于 120 秒启动宽限的当前槽，不枚举过去空槽，也不把当前响应挂到历史槽。

```text
pending -> running -> finalized
   |          |
   |          +-> retry_wait -> running
   +-------------------------> abandoned
retry_wait ------------------> abandoned
running ---------------------> abandoned
```

- Worker 必须先取得进程内 HTTP 并发额度，再在短事务中认领；没有额度时 run 保持 `pending`，lease 不开始。
- 认领写入随机 fencing token 和数据库计算的 30 秒 lease，并返回数据库计算的剩余 grace。Driver context deadline 是剩余 grace 与 15 秒请求上限的较小值。
- Driver 已形成的 timeout、网络/Secret/HTTP 非 200、contract invalid、disk fallback 或 identity 不完整观察都作为真实槽证据 `finalized`，不得在同槽隐藏重试。
- 只有 Control 崩溃、完整 finalize 无法提交或 lease/fencing 丢失属于未知执行。宽限内且 attempt 未耗尽时复用原 run；初始 `max_attempts=2`。过 grace 或 attempt 耗尽只转 `abandoned`，不请求 Node。
- `finalized` 和 `abandoned` 不可重开。不要直接 UPDATE/DELETE/TRUNCATE poll 表，不要使用人工 SQL 补采。

## 3. 运行配置与容量门禁

Control 已接入以下固定配置契约。`CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED` 默认为 `false`；生命周期 foundation 部署后，启用 poll 还必须同时显式设置 `CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED=true`，并通过数据库新函数与权限兼容检查。只有 lifecycle、poll 和 `CONTROL_CLIPROXYAPI_DRIVER_ENABLED` 都显式启用且全部配置通过校验时，poll service 才启动网络采集。不得把“表已存在”、只启用 Driver 或只启用 lifecycle 当作 poll service 已启用。紧急回滚必须先同时关闭 poll 与 Provider policy mutation，再运行旧二进制并保留 forward Migration。

| 配置 | 初始值 | 安全要求 |
|---|---:|---|
| period | `300s` | 必须固定五分钟 |
| poll start grace | `120s` | 严格小于 period |
| max monitored Nodes | `50` | 资产上限以内 |
| HTTP concurrency | `10` | 50 Node 时不得低于 10，最大 50 |
| worst-case request | `15s` | 包含 Secret/DNS/TLS/body/parser |
| lease | `30s` | 必须覆盖 request 与 finalize 余量 |
| max attempts | `2` | 不得无界恢复 |
| dispatch margin | `10s` | 容量公式显式余量 |
| finalize margin | `10s` | lease 显式余量 |
| scheduler interval | `1s` | 有界扫描 |
| worker scan interval | `500ms` | 无 busy-loop |
| reconcile interval | `5s` | 小于 lease |
| database backoff | `1s..30s` | PostgreSQL 故障时不请求 Node |
| shutdown grace | `20s` | 先停调度/认领，再有限收尾 |

启动门禁计算：

```text
last_batch_start = (ceil(max_monitored_nodes / concurrency) - 1) * worst_case_request
last_batch_start + dispatch_margin < poll_start_grace
lease >= worst_case_request + finalize_margin
```

任一配置缺失、超界或矛盾时，poll service 必须在 Scheduler/Worker 启动和任何 Node 请求前 fail closed，只输出固定 `invalid_runtime_config`。Control 其他只读/API 功能和 Gateway/Relay Node 数据面不应因此改变。

## 4. 指标与日志

允许的 Prometheus 指标只有：

```text
relay_control_account_inventory_poll_run_state{instance_id,state}
relay_control_account_inventory_scheduler_lag_seconds{instance_id}
relay_control_account_inventory_queue_wait_seconds{instance_id}
relay_control_account_inventory_poll_start_lag_seconds{instance_id}
relay_control_account_inventory_transport_success{instance_id}
relay_control_account_inventory_contract_valid{instance_id}
relay_control_account_inventory_provider_snapshot_complete{instance_id,provider}
```

这些值必须从 PostgreSQL 持久时间和最新证据重建。失败观察只要完整 `finalized`，就表示该槽已完成并推进 scheduler completion；transport/contract gauge 表达失败。`abandoned` 或从未创建的槽仍是可识别缺口。重启不能把 queue/start lag 清零。

禁止 promotion、stale、account、coverage 等尚未实现的指标。禁止 poll-run ID、policy version、email/account、endpoint/IP、Secret、Node version/commit 或原始 error 成为标签。Provider 必须来自 pinned policy 的受控、规范化 allowlist。

结构化日志只允许固定 `component/action/result/reason/node_type/state/attempt_bucket`，以及受控排障用稳定 `instance_id`。不得打印任意 `error.Error()`、SQL 参数、连接串、Provider 账号、请求/响应或凭证。建议日志原因与 `inventorypoll.ControlReason` 保持同一封闭字符串，如 `database_unavailable|invalid_claim|grace_exhausted|lost_lease|attempts_exhausted|shutdown`。

## 5. 故障处置

### scheduler lag 增长

1. 确认最新 run 是 `pending/running/retry_wait/abandoned` 还是缺失槽。
2. 检查 PostgreSQL 连接、锁等待和 Scheduler 固定错误分类；不要用 Go wall clock 或手工 INSERT 创建历史槽。
3. 若资格、capability、Driver contract、监控区间、policy activation/binding 不一致，保持 fail closed 并修复资产真相，不猜测 policy。

### pending 排队或 poll start lag 接近 120 秒

1. 检查 concurrency 与最多 50 Node 的容量公式。
2. Worker 必须先取得并发额度再认领；禁止通过延长 lease 掩盖排队。
3. 已过 grace 的 pending/retry_wait 只能由 Reconciler 置 `abandoned`。不要提升 concurrency 超过上限或重开历史槽。

### expired lease / lost fencing

1. 宽限内且 attempt 小于 2：Reconciler 可把原 run 转 `retry_wait`，下一 Worker 复用同一 run。
2. 过 grace 或 attempt 耗尽：只转 `abandoned`。
3. 旧 Worker finalize 影响零行时丢弃内存结果，不覆盖恢复者，不把迟到观察保存为新 run。

### PostgreSQL 中断

Scheduler、Worker、Reconciler 按有界退避暂停；没有已认领、可 fenced finalize 的持久归属时不得调用 Node。恢复后先 Reconcile，再处理仍有效的当前槽。晚于 grace 的槽不补采。数据库停止只暂停采集，不应影响 Gateway/Relay Node 已有模型请求。

### Node、Secret、DNS 或 contract 故障

只要 Driver 返回封闭观察且 finalize 可提交，本槽进入 `finalized`，不进入同槽 retry。依赖恢复后等待下一有效固定槽；禁止重写旧失败历史。

## 6. 停机、发布与回滚

停机顺序固定为：停止 Scheduler 创建、停止 Worker 新认领、取消尚未 dispatch 的 context、给已返回观察的 fenced finalize 有限收尾。未确认的 `running` 保留 lease，重启后由数据库 Reconciler 恢复；停机钩子不得猜测结果或强制 finalized。

发布先由 Migration owner 执行 additive forward Migration，再发布通过环境、Migration、Driver registry 和容量校验的应用。应用回滚只停止新 poll 并回退二进制，保留 `account_inventory_poll_runs` 与 provider results。普通回滚不得执行 down；只有两表全空、从未形成证据且没有后续依赖的全新环境才允许 DBA 人工 down。

## 7. 无代理与敏感 Canary 验收

所有 Go/npm/Docker/make 命令都显式清除大小写 HTTP/HTTPS/ALL proxy。npm registry 固定为批准镜像：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

deploy/acceptance/account-inventory-poll-run.sh static
```

敏感扫描目录只能包含受保护的脱敏 PostgreSQL 投影、Prometheus 文本、JSON 日志、测试输出和 acceptance artifact；不要放入真实响应或凭证。Scanner 从环境读取唯一测试 canary，既不把值放入 argv，也不回显命中的文件或内容：

```sh
umask 077
export CONTROL_POLL_CANARY_SCAN_DIR='/absolute/protected/poll-artifacts'
export CONTROL_POLL_CANARY_ENDPOINT='endpoint-canary-unique'
export CONTROL_POLL_CANARY_SECRET_REFERENCE='reference-canary-unique'
export CONTROL_POLL_CANARY_SECRET_VALUE='secret-value-canary-unique'
export CONTROL_POLL_CANARY_EMAIL='email-canary-unique'
export CONTROL_POLL_CANARY_RESPONSE_BODY='body-canary-unique'
export CONTROL_POLL_CANARY_RESPONSE_HEADER='header-canary-unique'
export CONTROL_POLL_CANARY_RAW_ERROR='error-canary-unique'

deploy/acceptance/account-inventory-poll-run.sh scan
```

Scanner 拒绝 symlink、非普通文件、单文件/总量/文件数超限，只输出固定成功或失败原因。Canary 命中时不要打印匹配行；删除污染 artifact 后重新从 allowlist 投影生成。

## 8. 容器与真实 Node 骨架

`container` 模式固定使用官方 CLIProxyAPI `v7.2.141` 原版镜像及其 pinned digest、内部 Docker 网络和合成空账号目录。它通过生产 Driver 只执行一次 auth-files，并在该最后一次请求后等待 10 秒；不调用 Probe，不访问真实 Node、互联网账号或模型数据面：

```sh
deploy/acceptance/account-inventory-poll-run.sh container
```

容器验收覆盖官方镜像的空目录 `disk_fallback` 契约；runtime、HTTP 非 200、非法/超限 contract、数据库中断/fencing 恢复和模拟数据面持续成功由 fake Driver 与 PostgreSQL 集成测试覆盖。网络只能出现固定 auth-files GET，不能出现 Probe、管理写方法、Gateway/模型请求或任意目标。验收不得把 raw body 保存到报告。

`real-node` 模式当前同样因 adapter 未接线而 fail closed。未来仅在阶段 0 获得明确批准后接入既有生产 Driver/runtime，不接受任意命令或 URL，并必须保持以下不可删除门禁：

- 最多两个受控 Node；不得用于 10 并发容量测试。
- 与现有 CLIProxyAPI readonly smoke 共用一个全局跨进程 lock，所有 management 请求串行。
- 每次请求成功或失败后都等待至少 `10s`，包括最后一次；失败后不得立即人工重跑。
- 只输出固定状态、reason、mode 和聚合计数；不输出 poll ID、instance ID、endpoint、Secret、provider account/email、header/body 或原始错误。
- 只调用 poll-run 所需的账号清单只读 GET；不调用 Probe 或写接口。

当前命令可用于验证 fail-closed 门禁，但不会请求真实 Node：

```sh
deploy/acceptance/account-inventory-poll-run.sh real-node
```

真实 adapter 接线后，必须先以 fake request 测试全局锁和“成功/失败/最后一次均冷却 10 秒”，再由两人复核脱敏证据目录并运行 canary scanner。

## 9. 完整发布门禁

在实现核心 runtime/store/app wiring 后，按顺序执行：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

make generate
make test
go test ./...
go test -race ./...
go vet ./...
npx --yes @fission-ai/openspec@latest validate add-control-account-inventory-poll-run-foundation --strict
git diff --check
```

PostgreSQL 集成测试必须使用隔离测试库和受保护的 `PGSERVICEFILE`/`PGPASSFILE`，不得把口令放进 argv。Docker 容器验收需在相同无代理环境执行。最后确认 worktree/acceptance 目录没有 runtime cache、数据库 dump、真实账号数据、Cookie、Secret 或原始 Node 响应。

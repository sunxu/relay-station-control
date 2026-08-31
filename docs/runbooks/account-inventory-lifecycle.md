# Control 账号清单生命周期运行手册

## 1. 适用范围与不变量

本手册适用于 `add-control-account-inventory-lifecycle-foundation`。PostgreSQL 中的 `account_inventory` 是当前账号生命周期唯一真相源；它只能由已应用的完整 runtime Provider promotion 在既有 fenced finalize 事务内推进。生命周期、snapshot items、Provider 当前指针、promotion 分类和 poll 终态必须全有或全无提交。

本阶段不新增账号产品 API/UI、导出、人工状态编辑、补采、告警、趋势、覆盖率、保留清理或历史事件。唯一允许的 Node 操作仍是既有 Driver 的固定账号清单只读 GET。Lifecycle finalize、Provider scope activation、内部读取、恢复和回滚不得调用 Probe、Gateway、模型数据面、Node 写接口或任意未登记目标。

标准化 email 和 `account_key` 只允许进入受保护的 snapshot、duplicate、lifecycle 列以及授权内部读取值。普通日志、指标、错误、SQL 参数日志、测试输出和验收 artifact 禁止出现逐账号 identity、poll/policy ID、endpoint/IP、Secret/Management Key、header/body、Node version/commit、未知字段或原始错误。

Migration 9 的 history retention 不重算或删除当前 lifecycle；到期 poll 清理可使 current source 外键合法变为 `NULL`，但已冗余的来源元数据、基础状态、missing 计数和 lifecycle 必须保持。不要把空 source 外键当成当前状态损坏，详见 [`account-inventory-history-compaction.md`](account-inventory-history-compaction.md)。

## 2. 状态机与证据来源

只有 `promotion_applied=true` 的完整 runtime Provider 快照是 lifecycle 证据。完整空集合也是有效证据；transport/contract 失败、disk fallback、identity/duplicate 不完整、`policy_changed`、`stale_poll`、abandoned 槽和未提交事务都不是证据。

| 当前状态 | 下一次合格快照 | 新状态 | 缺失计数 | 时间语义 |
|---|---|---|---:|---|
| 无基线 | 出现 | `present` | 0 | first/last seen 使用同一数据库 observed time |
| 无基线 | 空或未出现 | 无行 | — | 不从历史 snapshot 推断账号 |
| `present` | 未出现 | `suspected_missing` | 1 | `missing_since` 仍为空，last seen 不变 |
| `suspected_missing` | 未出现 | `missing` | 2 | `missing_since` 固定为第二次缺失的数据库 observed time |
| `missing` | 未出现 | `missing` | 2 | 计数饱和，`missing_since` 不改写 |
| `suspected_missing`/`missing` | 出现 | `present` | 0 | 清空 missing，保留 first seen并刷新 last seen/来源 |
| 任意 active 状态 | Provider 移出范围 | `out_of_scope` | 0 | 与 policy activation 使用同一数据库时间 |
| `out_of_scope` | Provider 重新 active但账号未出现 | `out_of_scope` | 0 | 不累计 missing |
| `out_of_scope` | 重新 active后的合格快照中出现 | `present` | 0 | 清空 out-of-scope time并刷新实际出现来源 |

每个 Provider 独立推进。Provider pointer 的槽位单调检查必须先于 lifecycle 转换；迟到旧槽只能得到 `stale_poll`，不能倒退来源或再次增加 missing。重复 finalize、旧 fencing 和 COMMIT 结果未知恢复最终只能形成一次转换。

## 3. 首次基线与连续缺失排障

### 首次部署后没有 lifecycle 行

这是预期行为。Migration 不扫描历史 snapshot，也不把既有 Provider pointer 回填为账号状态。部署后首次合格 promotion 只为实际出现账号建立 `present` 基线；首次合格空集合继续保持零行。

只查看按 instance/provider/lifecycle 聚合的有界计数，不要查询或打印 email/account key。若 snapshot promotion 已应用但基线仍为空，先确认该快照是否确实为完整空集合，再检查 lifecycle-aware finalize 版本是否启用；不得人工从历史 snapshot 补行。

### `suspected_missing` 增长

确认前后两次证据都是该 Provider 的完整 runtime promotion。第一次缺失只应产生计数 1，`missing_since` 必须为空，last seen 和实际出现来源不得刷新。中间的失败、降级或策略变化槽不会打断也不会增加有效 promotion 计数；下一次合格且仍缺失的 promotion 才进入 `missing`。

### `missing` 增长或恢复

第二次合格缺失把计数置为 2，之后必须饱和，且 `missing_since` 不再变化。账号重新出现时自动恢复 `present`、清零 missing 并保留 first seen；该恢复使用同一轮既有 GET，不允许人工补采或额外请求 Node。

## 4. Provider 移出与重新加入

Provider active→out-of-scope 必须通过 lifecycle-aware、实名 actor和有效 reason 的受审计 activation。policy activation、Provider monitoring status、现有账号 `out_of_scope` 转换和审计必须同事务提交。不要直接更新 Provider state 或 lifecycle 表。

重新加入 active 只改变 Provider monitoring status。旧账号仍为 `out_of_scope`；下一次合格 promotion 中实际出现的账号恢复 `present`，未出现旧账号继续 out-of-scope且不累计 missing。未来预约切换若不能在 effective time 原子执行，管理路径必须 fail closed，不能提前改变 lifecycle。

并发 finalize 与 scope activation 只允许两种结果：旧策略 promotion 先完成，或新策略切换先完成且旧 poll 以 `policy_changed` 跳过。出现混合 Provider/账号状态时立即停用 poll 与 policy mutation并按数据库故障处理。

## 5. 故障、恢复与回滚

### PostgreSQL 故障、超时或连接耗尽

生命周期没有内存副本、后台补做 job 或 Outbox。事务失败必须同时回滚 lifecycle、snapshot、Provider pointer、promotion 和 poll 终态。恢复只使用持久 poll、lease、fencing 和 pinned policy；已经观察到的 Node 失败或 lifecycle skip 不得触发同槽额外 GET。

数据库恢复后先核对非终态 poll 和固定失败分类，再恢复写路径。禁止直接修补 lifecycle 行、重放旧 candidates或从历史快照重算。Control/PostgreSQL 停止期间，已提交状态保持不变；Gateway/Relay Node 已有模型流量不依赖 lifecycle。

### 发布顺序

1. 在隔离 PostgreSQL 18 执行 additive forward Migration，并确认 lifecycle 表初始为空、历史 snapshot/promotion 未改写。
2. 发布理解 lifecycle-aware finalize/activation 的新二进制，默认保持 lifecycle poll 与 policy mutation关闭。
3. 核对新函数、权限、运行时启动兼容检查和聚合指标后，先启用 lifecycle-aware policy mutation，再启用 poll 写路径。
4. 观察至少两个合格 promotion；只比较封闭 lifecycle 聚合、promotion reason、事务耗时和 WAL，不读取逐账号 identity。

可在隔离数据库中用下列只读查询检查对象；函数名输出不含账号数据：

```sql
SELECT to_regclass('public.account_inventory') IS NOT NULL AS lifecycle_table_present;

SELECT proname
FROM pg_proc
JOIN pg_namespace ON pg_namespace.oid = pg_proc.pronamespace
WHERE nspname = 'public'
  AND proname LIKE 'control_%account_inventory%lifecycle%'
ORDER BY proname;
```

### 应用回滚

1. 先关闭 poll 写路径和 Provider policy mutation 两个开关，并等待已有 Worker/finalize 退出。
2. 确认没有 running poll、没有持有 lifecycle-aware函数的事务，再回退旧二进制。
3. 保留 forward Migration、`account_inventory` 和全部已提交状态。旧二进制不得删除、重算或继续推进 lifecycle。
4. 恢复新版本后，从下一次合格 promotion继续；不补停机槽。

普通生产回滚禁止执行 down。受保护 down 只允许 lifecycle 表为空、没有新 out-of-scope 状态且无后续依赖的全新环境。

## 6. 请求计数与数据面隔离

- Store/Migration/fencing/policy/canary 验收直接使用合成数据库 fixture，Node 请求数固定为 `0`。
- fake Driver 每个 poll observation只调用现有 `ListAccountInventory` 一次；首次基线、第一次缺失、第二次缺失和恢复四轮对应四次 fake 调用，不是四次额外网络请求。
- Provider 移出或重新加入是数据库管理事务，本身请求 Node `0` 次；重新加入后的下一次合格 poll 仍只有原有一次 GET。
- 官方容器模式只做一次合成 runtime GET 并等待 10 秒，用于证明生产 Driver 请求边界；连续缺失/恢复由 PostgreSQL 多轮 fixture 证明，不能把一次响应重解释成多轮 lifecycle 证据。
- lifecycle runner 的 `real-node` 模式固定 fail closed并保持 `request_count=0`。若未来另行批准阶段 0 验收，必须复用 snapshot 全局锁、固定两个登记 Node、严格串行，每次成功/失败和最后一次后等待至少 10 秒，并单独记账。

## 7. 脱敏验收

所有 Go/npm/Make/Docker 命令必须清除大小写 HTTP/HTTPS/ALL proxy。生命周期主入口为：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

deploy/acceptance/account-inventory-lifecycle-run.sh static
deploy/acceptance/account-inventory-lifecycle-run.sh postgres
deploy/acceptance/account-inventory-lifecycle-run.sh container
deploy/acceptance/account-inventory-lifecycle-run.sh data-plane
deploy/acceptance/account-inventory-lifecycle-run.sh scan-smoke
deploy/acceptance/account-inventory-lifecycle-run.sh scan-matrix
```

当前增强后的 `postgres` gate 在隔离 PostgreSQL 18 上覆盖 Migration 不回填、首次基线、连续缺失、恢复、out-of-scope/re-add、fake Driver→real Store、未提交终止、commit-unknown replay、权限/保护写和 policy-mutation shell 开关。独立容量子门禁覆盖 1/10/50 Node × 1000 账号的 lifecycle 行数、WAL、锁等待、最后 dispatch 与事务/批次时长，并分别执行 120 秒 dispatch grace 和 30 秒 lease 预算。只有增强 gate 在最终提交上实际通过后，才能保留其成功分类。`container` 先运行该数据库 gate，再经 pinned official-image synthetic gate 执行唯一一次 GET，并在同一次生产 Driver/Worker/Store 链路中断言一个 `present` lifecycle 行。`data-plane` 在已形成两行 lifecycle 后停 PostgreSQL，停机窗口运行独立 synthetic loopback `50/50`，重启后再次通过 Store 和持久指标读取两行；它不访问 Gateway 或 Node。

为防止 `go test -run` 在没有匹配测试时仍返回成功，PostgreSQL gate 会先核对 `internal/store` 已登记连续缺失/恢复、Provider scope/re-add、权限/保护写、Migration no-backfill、fake Driver→real Store、未提交终止、commit-unknown 和容量 suite；缺少任一 suite 时固定返回 `lifecycle_implementation_unavailable`，不得生成通过证据。功能 suite 与 1/10/50 Node 容量 suite 分开执行，分别固定返回 `lifecycle_store_core_gate_failed` 或 `lifecycle_capacity_gate_failed`；容量 worker 只在取得 10-slot 并发额度后才启动 30 秒 lease，并独立断言最后 dispatch 不超过 120 秒。故障恢复和数据面隔离由独立 `data-plane` gate 证明，不折叠进该成功分类。

`scan-smoke` 只对 CI 中单一受保护合成聚合 artifact 验证 scanner 的配置和遍历边界。`scan-matrix` 将全部 15 类 canary 实际送入 production CLIProxyAPI Driver/Worker 的成功与网络失败输入、Worker lost-lease/database-error 的 policy-race/rollback 输入，以及生成的 sqlc lifecycle finalize 参数 formatter；组件把真实产生的结构化日志、指标计数、固定错误、redacted 参数格式和 Go test output 写入 success、failure、policy-race、rollback 四个受保护子目录，再复用正式 `scan` 路径。该门禁使用一个本地 synthetic management GET和两个 in-process fake Driver 调用，不访问真实 Node或生产 artifact，也不允许保留任意原始日志。

Canary scanner 的输入目录只能包含已脱敏的聚合投影、固定错误、指标和测试摘要，不得放原始响应、数据库 dump 或凭证。所有 canary 都经环境变量注入，scanner 的失败输出不会回显命中值：

```sh
umask 077
export CONTROL_LIFECYCLE_CANARY_SCAN_DIR='/absolute/protected/lifecycle-artifacts'
export CONTROL_LIFECYCLE_CANARY_ENDPOINT='endpoint-canary-unique'
export CONTROL_LIFECYCLE_CANARY_IP='address-canary-unique'
export CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE='reference-canary-unique'
export CONTROL_LIFECYCLE_CANARY_SECRET_VALUE='management-key-canary-unique'
export CONTROL_LIFECYCLE_CANARY_EMAIL='email-canary-unique'
export CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY='account-key-canary-unique'
export CONTROL_LIFECYCLE_CANARY_RESPONSE_BODY='body-canary-unique'
export CONTROL_LIFECYCLE_CANARY_RESPONSE_HEADER='header-canary-unique'
export CONTROL_LIFECYCLE_CANARY_VERSION='version-canary-unique'
export CONTROL_LIFECYCLE_CANARY_COMMIT='commit-canary-unique'
export CONTROL_LIFECYCLE_CANARY_RAW_ERROR='error-canary-unique'
export CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER='sql-parameter-canary-unique'
export CONTROL_LIFECYCLE_CANARY_POLL_ID='poll-id-canary-unique'
export CONTROL_LIFECYCLE_CANARY_POLICY_ID='policy-id-canary-unique'
export CONTROL_LIFECYCLE_CANARY_UNKNOWN_FIELD='unknown-field-canary-unique'

deploy/acceptance/account-inventory-lifecycle-run.sh scan
```

## 8. 发布门禁

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

make generate
make test
make build
go test ./...
go test -race ./...
go vet ./...
npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-lifecycle-foundation --strict
npx --yes @fission-ai/openspec@1.10.0 validate --all --strict
git diff --check
```

归档前必须确认：所有 lifecycle scenario 有自动化证据或明确受控人工证据；OpenAPI、生成客户端和 React 路由无差异；transition counter 若无持久事实则未实现；工作树没有 runtime cache、数据库 dump、逐账号 identity、Secret、原始响应或本机绝对路径。

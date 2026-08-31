# Control 账号清单快照与 Promotion 运行手册

## 1. 范围和边界

本手册适用于 `account-inventory-snapshot` foundation。它在既有 poll run 的 fenced finalize 事务内，为完整 runtime Provider 保存字段白名单化的不可变快照，并原子推进 `(instance_id, provider)` 当前来源。PostgreSQL 是 poll、策略版本、snapshot items、重复证据、Provider 当前指针和 promotion 分类的唯一真相源。

快照阶段不增加 Node 请求。唯一允许的网络操作仍是既有 Driver 的固定账号清单只读 GET；不得调用 Probe、Gateway、模型数据面、管理写接口或任意 URL。它也不实现账号生命周期、missing/out-of-scope 推进、连续缺失、stale、历史压缩、覆盖率、告警、OpenAPI、管理页面或人工 retry/promotion。

标准化 email 和 `account_key` 属于敏感账号身份，只允许进入受保护的 snapshot/duplicate 列。普通日志、指标、错误、SQL 参数日志、测试输出和验收 artifact 禁止出现 email、account key、poll/policy ID、endpoint/IP、Secret、header/body、原始错误、Node version/commit 或未知响应字段。

运行时角色不能直接 SELECT/INSERT/UPDATE/DELETE/TRUNCATE 快照、重复或 Provider state 表。后续内部生命周期只能通过固定 `instance_id`、规范 Provider、有界 `account_key` cursor 和最多 500 行的受控读取函数访问当前快照；本 change 没有把该函数接到产品 HTTP API，也没有任意历史枚举入口。

Migration 9 允许 history compactor 在固化不可变摘要后分批删除至少 72 小时前的 snapshot，并在满足固定 30 天门禁后清理 poll 历史。清理只会让 current source 外键合法变为 `NULL`；Provider/current 行中冗余的来源时间、版本、提交、基础状态和生命周期必须保持。操作边界见 [`account-inventory-history-compaction.md`](account-inventory-history-compaction.md)。

## 2. Promotion 决策

finalize 固定先锁 poll run 并验证 running、lease 和 fencing，再锁该 run 对应的当前 Provider policy binding。promotion 与 poll completeness 是不同证据：

| 条件 | Snapshot evidence | Provider 当前指针 | Promotion |
|---|---|---|---|
| binding 仍是 pinned policy，Provider runtime 且完整 | 保存全部去重 items；零账号也是合法空快照 | 只向更晚槽推进 | applied |
| 当前 binding 已切换 | 可保存 poll/provider/duplicate 采集证据，不保存 items | 不变 | skipped: `policy_changed` |
| transport 失败 | 不保存 items | 不变 | skipped: `transport_failed` |
| contract 无效 | 不保存 items | 不变 | skipped: `contract_invalid` |
| disk fallback | 不保存 items | 不变 | skipped: `disk_fallback` |
| identity 缺失 | 不保存该 Provider items | 不变 | skipped: `provider_identity_incomplete` |
| 节点内重复 key | 只保存有界 duplicate evidence | 不变 | skipped: `provider_duplicate` |
| 更旧槽迟到 finalize | 保留允许的终态证据，不覆盖较新 current | 不变 | skipped: `stale_poll` |

同一 Node 的 Provider 独立判断。一个 Provider 缺 identity 或重复不得阻止另一个完整 Provider promotion。空但完整的 active Provider 会写零条 item并推进当前指针；不能用 item 数判断是否存在快照。

旧版本已经 finalized、但早于 snapshot Migration 的 Provider 行属于“未评估 promotion”。它们继续导出 snapshot-complete 等既有指标，但不应合成 `promotion_applied=0` 或任意 skipped reason。

## 3. 一致性和故障处置

### `policy_changed` 增长

1. 确认近期是否发生 Provider policy activation/binding 切换。
2. 这是预期的串行化结果：旧 poll 仍按 pinned policy 保存采集证据，但不得按新 policy 重新解释或 promotion。
3. 等待下一有效五分钟槽。不要人工改 provider state、重放旧观察或请求 Node 补采。

### 完整 Provider 没有推进

1. 查看封闭 promotion skipped reason，而不是读取或打印账号 identity。
2. 检查 inventory mode 是否为 runtime、contract 是否有效、Provider identity/duplicate 聚合是否完整，以及是否属于 `stale_poll`。
3. 如果数据库 finalize 未提交，poll 必须保持可恢复的非终态；不得出现 finalized 但半份 items 或半个指针。
4. 如果新槽已成为 current，迟到旧槽不得回退指针。

### 空快照

`promotion_applied=true` 且该 poll/Provider 的 item 数为零表示“完整观察到零账号”，不是缺少快照。不要据此生成 missing、告警或人工补采。后续生命周期 change 才能定义新旧快照差异的业务语义。

### PostgreSQL 中断、连接耗尽或事务超时

快照没有内存 current state，也没有第二个异步 promotion 任务。finalize 事务失败必须整体回滚；既有 Reconciler 只按原 poll、原 pinned policy、原 grace/attempt 规则恢复。已经形成的 Node 失败或 promotion skip 不触发同槽 Node 重试。数据库恢复后先核对 lease/fencing 与未终态 run，再恢复 poll；不要直接写 snapshot/provider-state 表。

### 停止与应用回滚

关闭 poll service 只暂停后续快照提升，不影响 Gateway/Relay Node 数据面和已提交 current state。发布顺序是先 additive forward Migration，再发布理解新 finalize 契约的二进制。应用回滚应先停止 poll，再回退二进制并保留新表、字段和历史；普通生产环境禁止执行 down。

受保护 down 只适用于从未形成 snapshot/duplicate/provider-state 或 promotion 标记、且没有后续依赖的全新环境。不要为应用回滚删除快照历史。

## 4. 指标与日志

既有七个 poll 指标继续保留，并新增：

```text
relay_control_account_inventory_provider_promotion_applied{instance_id,provider}
relay_control_account_inventory_provider_promotion_skipped{instance_id,provider,reason}
```

`promotion_applied` 只对已评估 Provider 导出 `0|1`。`promotion_skipped` 只在未应用时导出值 `1`，reason 必须属于上表封闭集合。Provider 必须来自 pinned policy 的规范化 allowlist。重启后所有值从 PostgreSQL 持久证据重建，不从内存猜测。

promotion 日志只允许固定 `component=poll_worker`、`action=promote`、`result=success|skipped`、固定 reason、`state=finalized`、受控 instance ID 和 Provider。普通 finalize 失败继续使用既有固定分类；禁止格式化 AccountObservation、snapshot finalize request、数据库参数或任意 `error.Error()`。

## 5. 无代理验收

所有 Go/npm/Docker/make 命令必须显式清除大小写 HTTP/HTTPS/ALL proxy；npm registry 固定为批准镜像：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

deploy/acceptance/account-inventory-snapshot-run.sh static
```

真实数据库恢复门禁使用 pinned PostgreSQL 18 镜像、loopback 动态端口和该次运行独占的命名卷。它会在 finalize 等待 policy binding lock 时停止数据库，验证未提交事务全量回滚，再从持久 poll/lease/fencing 状态恢复；随后注入 `statement_timeout`、将 runtime LOGIN 角色的连接上限压到 1 并占满以确认 SQLSTATE `53300`，最后再次 stop/start 并从数据库重建 current snapshot 与指标。数据库停机期间只运行独立的 synthetic loopback HTTP 数据面，不访问 Gateway、真实 Node 或管理接口：

```sh
deploy/acceptance/account-inventory-snapshot-run.sh postgres
```

正常和失败退出都会定向删除该 Compose project、命名卷及 `/tmp` 下的受保护日志/二进制目录。输出只包含固定分类、SQLSTATE 和有界计数；不要为了排障回显 Goose/Store 原始日志、连接串、标识符或快照内容。

敏感扫描目录只放脱敏数据库投影、Prometheus 文本、JSON 日志、固定错误、测试输出和验收 artifact，不得放真实响应、数据库 dump 或凭证。Scanner 不把 canary 放入 argv，也不回显命中文件或内容：

```sh
umask 077
export CONTROL_POLL_CANARY_SCAN_DIR='/absolute/protected/snapshot-artifacts'
export CONTROL_POLL_CANARY_ENDPOINT='endpoint-canary-unique'
export CONTROL_POLL_CANARY_SECRET_REFERENCE='reference-canary-unique'
export CONTROL_POLL_CANARY_SECRET_VALUE='secret-value-canary-unique'
export CONTROL_POLL_CANARY_EMAIL='email-canary-unique'
export CONTROL_POLL_CANARY_RESPONSE_BODY='body-canary-unique'
export CONTROL_POLL_CANARY_RESPONSE_HEADER='header-canary-unique'
export CONTROL_POLL_CANARY_RAW_ERROR='error-canary-unique'
export CONTROL_POLL_CANARY_ACCOUNT_KEY='account-key-canary-unique'
export CONTROL_POLL_CANARY_UNKNOWN_FIELD='unknown-field-canary-unique'
export CONTROL_POLL_CANARY_SQL_PARAMETER='sql-parameter-canary-unique'

deploy/acceptance/account-inventory-snapshot-run.sh scan
```

标准化 email/account key 只能在专门的数据库负向测试中进入隔离 PostgreSQL 的预期受保护列；生成 artifact 前必须投影为固定计数和分类。Scanner 对所有 artifact 一律禁止这些值。

## 6. 官方容器与真实 Node

官方镜像验收由两个隔离且互斥的观测组成：既有 disk-fallback gate 使用合成空 auth 目录；runtime/promotion gate 使用脱敏的合成 runtime fixture、生产 Driver/Worker 和隔离 PostgreSQL 18 Store/fenced finalize。两个 gate 都先取得与既有只读 smoke 共用的全局跨进程 lock，固定运行未修改的 CLIProxyAPI `v7.2.141` pinned digest 和内部 Docker 网络。每个 gate 各自只执行一次固定 auth-files GET，并在该次请求成功或失败后（包括各自最后一次请求）等待 10 秒；快照 finalize/promotion 不新增 Node 请求。两个 mode 来自不同运行，不以同一响应同时证明 runtime 和 disk fallback。

当前容器入口执行 runtime/promotion gate：

```sh
deploy/acceptance/account-inventory-snapshot-run.sh container
```

既有独立 gate 直接验证官方镜像的 disk-fallback mode，并与 PostgreSQL/Worker 门禁组合证明该 mode 跳过 promotion；当前 gate 验证官方 runtime 观测通过真实 Store/fenced finalize 原子写入 snapshot item、Provider state 并应用 promotion。两者都只使用合成 fixture，不含真实账号身份或生产凭证，也不修改 Node、不调用 Probe、Gateway 或管理写接口。空 runtime 快照、多 Provider 独立 promotion、policy race、重复、fencing、指针单调和事务崩溃等其他分支继续由 fake Driver 与 PostgreSQL 18 集成测试覆盖。不要把合成 fixture 的 raw body 写入报告。

阶段 0 的两个已登记测试 Node 可通过专用入口验收；该入口不接受任意 URL、容器名、Provider 或 Secret 值，只接受仓库外受保护 runtime 根目录。它固定核验两个 Node 的镜像、本机端口边界和共同 private bridge，通过精确 DNS/CIDR allowlist 使用生产 Driver，随后由单并发 Worker 和一次性隔离 PostgreSQL 18 Store 执行 fenced finalize：

```sh
PHASE0_RUNTIME_DIR=/absolute/private/phase0 \
  deploy/acceptance/account-inventory-snapshot-run.sh real-node
```

两个 Node 各执行一次相同的账号清单只读 GET，严格串行，并在每次成功或失败后（包括最后一次）等待至少 10 秒；不调用 Probe、Gateway 或管理写接口。管理密钥只通过 `0600` 文件只读挂载和 opaque mapping 引用，不进入命令参数、环境值或输出。真实 identity 只短暂进入一次性 PostgreSQL 的受保护 snapshot/duplicate 列；验收必须先核验删除 Secret mount、数据库容器、internal network、identity-bearing volume 和临时目录，再释放同宿主共享的全局跨进程 lock，最后才输出固定聚合计数与 promotion 分类。

失败或信号中断会保留 stale lock，禁止立即重跑。只有确认无验收进程、无临时资源且最后一个请求（若存在）已结束至少 10 秒后，operator 才可删除该空 lock；不得靠改 `CONTROL_DRIVER_SMOKE_LOCK_DIR` 绕过。该 lock 只提供同宿主串行保证，不是跨主机分布式锁。

## 7. 发布门禁

在隔离 PostgreSQL 18 上完成 Migration、权限、finalize、策略并发、pointer 单调和 protected down 测试后，执行：

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
npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-snapshot-foundation --strict
npx --yes @fission-ai/openspec@1.10.0 validate --all --strict
git diff --check
```

最后确认运行时角色不能直接读写删改受保护表、只能调用有界内部当前快照函数，并确认无新增产品 API/UI、真实 Node 请求严格等于已批准的两次固定 GET，且工作树没有 runtime cache、数据库 dump、账号 identity、Secret 或原始响应。

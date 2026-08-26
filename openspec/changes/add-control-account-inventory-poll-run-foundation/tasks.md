## 1. 边界、配置与状态模型

- [x] 1.1 对照系统设计 v1.0 第 9.6、12、13.4、20.3、21.1、23、24.2 节、ADR-0001、现有 asset/Driver/durable-job spec，固化本 change 的 poll-run 专用状态机和账号快照/生命周期/压缩/API/UI 非目标
- [x] 1.2 定义 poll period、`poll_start_grace`、max monitored nodes、concurrency、worst-case request、lease、max attempts、scan/reconcile interval 配置，设置 300s/120s/50/至少10/15s/30s/2 的保守默认和安全上限
- [x] 1.3 实现容量公式与启动校验，覆盖 concurrency<10、last batch 超 grace/余量、lease 不覆盖 request+finalize、非五分钟周期、无界扫描和矛盾配置在网络调用前 fail closed
- [x] 1.4 定义封闭 poll status、固定执行/transport/contract/degraded reason 和状态转换表，证明 poll run 不注册为 `async_job` kind 且 production durable-job executor registry 不变

## 2. Migration、权限与 sqlc

- [x] 2.1 新增单个 additive Goose Migration，创建 `account_inventory_poll_runs` 与 `account_inventory_poll_provider_results`，包含 UUID、数据库 UTC 时间、五分钟槽、唯一 Node/槽、pinned policy、lease/fencing、聚合结果和必要外键/索引
- [x] 2.2 用 CHECK/trigger/受控函数封闭 pending/running/retry_wait/finalized/abandoned 字段组合、终态不可逆、attempt/lease/finalize 时间关系、abandoned 无观察和 provider 唯一性
- [x] 2.3 实现运行时最小权限：允许幂等调度、认领、恢复、fenced finalize 和指标读取，拒绝直接终态篡改、DELETE/TRUNCATE、任意 policy 改写及绕过 provider 集完整性
- [x] 2.4 新增 sqlc 查询/事务原语，覆盖当前槽资格查询、幂等创建、SKIP LOCKED 认领、lease Reconcile、原子 finalize、state/lag 读取；运行 `make generate` 并验证生成物可复现且未手改
- [x] 2.5 编写 Migration 正/反向与 schema 集成测试，覆盖 300 秒对齐、UTC session、重复键、策略/Node 外键、状态非法组合、provider 缺失/额外/重复、权限负向和非空表禁止普通 down

## 3. UTC Scheduler 与资格过滤

- [x] 3.1 实现只依赖 PostgreSQL 时间的当前五分钟槽计算和幂等 Scheduler，不使用 Go wall clock 生成 `scheduled_at`，不枚举/补建已过期空槽
- [x] 3.2 在同一调度边界过滤 Node capability、`relay_node_inventory_monitoring_activations` 半开区间、Node type/Driver contract 和 Provider policy activation/binding，首次插入固定 policy version
- [x] 3.3 测试 Node 在 UTC 日中途首次纳管、暂停、重新纳管、退役和恰好区间边界；确认 Gateway/Compose/资产读取状态不会隐式修改监控资格
- [x] 3.4 测试重复 tick、并发 Scheduler、当前槽重启和 policy 切换，证明只保留一个 run/原 policy；重叠区间、binding 损坏和缺策略 fail closed 且不调用 Driver
- [x] 3.5 实现 pending/retry_wait 过 grace 的 abandoned 扫描，验证旧空槽不创建、旧已建槽不带观察终止、下一有效槽正常创建

## 4. 有限并发、认领与 dispatch deadline

- [x] 4.1 实现进程级有界 poll semaphore，严格先取得并发额度再用 `FOR UPDATE SKIP LOCKED` 认领，稳定排序且没有额度时 run 保持 pending/不启动 lease
- [x] 4.2 实现短认领事务：数据库判断 grace、原子 running/attempt/首次与最近 started/随机 fencing/30s lease，并返回数据库计算的 `grace_remaining`
- [x] 4.3 将 `min(grace_remaining,15s request timeout)` 作为相对 context deadline 贯穿现有 Driver；注入慢 Secret、DNS、拨号和调度暂停，证明过 grace 前未发出的 HTTP 被取消
- [x] 4.4 使用 fake Driver/Clock/DB 测试 1/10/50 Node、公平稳定认领、concurrency 上限、无排队 running、context 取消、goroutine/连接释放和停机 drain
- [x] 4.5 使用 50 个本地受控 fake Node 验证 concurrency>=10、15 秒最坏耗时与数据库余量下最后一批在 120 秒 grace 内开始；记录容量公式、实测分布和退出码

## 5. Driver 调用、finalize 与聚合证据

- [x] 5.1 实现从 asset/pinned policy 构造固定 `NodeTarget`/`InventoryRequest` 的 poll invoker，复用既有 registry/Secret/SSRF/无代理边界，不增加任意 HTTP、Probe 或隐藏 retry 能力
- [x] 5.2 实现 fenced finalize 事务：锁 poll run、核对 running/lease/token，使用数据库 `observed_at`，写固定策略全部 active Provider 聚合行并原子置 finalized
- [x] 5.3 将 Driver transport failure/非200、contract invalid、disk fallback、missing provider/email、节点内 duplicate、unsupported/out-of-scope 和合法零记录 Provider 映射为 Node/Provider 聚合证据；Node 失败 finalized 而不进入同槽 retry
- [x] 5.4 在 transport/contract 失败时由 pinned policy 补齐全部 active Provider 的 incomplete/degraded 行；验证 provider 集缺失/额外/重复、汇总计数不一致或非法 reason 使 finalize 整体回滚
- [x] 5.5 在 policy 创建后并发切换 binding，证明 Driver 与 finalize 始终使用旧 pinned policy；本 change 不锁 current binding、不写 snapshot/current state/promotion，也不把新 policy 重解释旧观察
- [x] 5.6 建立持久化字段 allowlist 与编译/单元测试，证明 Accounts/email/account key/duplicate identity、endpoint/IP、Secret reference/key、header/body、未知字段和原始 error 无法进入 poll tables、日志或错误

## 6. Lease、fencing、崩溃与数据库恢复

- [x] 6.1 实现 Reconciler：lease 过期且在 grace/attempt 内从 running fenced 转 retry_wait，过 grace 或耗尽 attempt 转 abandoned；pending/retry_wait 到期直接 abandoned
- [x] 6.2 测试旧 Worker 在 lease 过期、fencing 替换、终态提交后回写均影响零行，finalized/abandoned 无法重开且不会追加 Node 请求
- [x] 6.3 分别在 pending 创建后、认领提交前后、Driver 请求前/中/后、provider rows 写入中和 finalized 提交前后注入崩溃，验证唯一 run、有界最多两次只读 GET、无部分终态和过 grace 不补采
- [x] 6.4 在 Node 已返回后中断 PostgreSQL，验证 finalize 全回滚、lease 到期按剩余 grace 恢复；数据库恢复晚于 grace 时 abandoned 且迟到 Worker 结果被拒绝
- [x] 6.5 测试 PostgreSQL 启停/连接耗尽/事务超时的有界退避，确认数据库不可用期间不调用 Node、不 busy-loop、不产生内存真相，恢复后先 Reconcile 再处理当前有效槽
- [x] 6.6 测试优雅停机先停止 Scheduler/认领、取消未 dispatch context、有限 finalize；未确认 running 保留 lease并在重启后走同一恢复路径，不由停机钩子猜测结果

## 7. 指标、日志与安全负向

- [x] 7.1 实现 poll state、scheduler lag、queue wait、poll start lag、transport、contract 和 Provider snapshot-complete 指标，从数据库持久时间恢复并严格封闭 state/provider/reason/mode
- [x] 7.2 测试 finalized transport/contract 失败仍推进 scheduler completion，abandoned/缺失槽保持可识别缺口，重启不清零 queue/start lag；不提前导出 promotion/stale/account/coverage 指标
- [x] 7.3 实现 poll 结构化日志 allowlist，禁止 poll ID、policy version、email/account、endpoint/IP、Secret、version/commit、原始 error/body/header，固定 instance ID 只在受控排障字段使用
- [x] 7.4 对成功及所有失败/恢复路径注入唯一 endpoint、Secret 引用/值、email、body、header 和错误 canary，扫描 PostgreSQL、日志、指标、错误、test output 与 acceptance artifact，验证只存在允许聚合值
- [x] 7.5 验证 runtime 角色无法通过 SQL/API 创建历史槽、人工 retry/补采、删除证据或调用管理写路径；自动 poll 不新增实名管理员 audit row

## 8. 容器、官方镜像、Runbook 与综合验收

- [x] 8.1 新增 poll service 配置/启停/容量/lag/abandoned/数据库恢复/应用回滚 Runbook，说明 PostgreSQL UTC、无历史补采、Node 失败 finalized、max attempt=2 和保留 forward Migration
- [x] 8.2 用官方 CLIProxyAPI v7.2.141 原版镜像和脱敏 fixtures 完成 container poll-run 验收，覆盖 runtime、disk fallback、HTTP 非200、非法/超限 contract 和无 Node/Gateway 修改
- [x] 8.3 如运行阶段 0 的两个真实测试 Node，使用全局串行请求且每次成功/失败后与最后一次后等待至少 10 秒，只记录脱敏状态/计数；验证 6 个测试账号由 Driver 分类但数据库/报告不保存 email或原始响应
- [x] 8.4 在 Go/npm/Docker/make 显式清除 uppercase/lowercase HTTP/HTTPS/ALL proxy（npm registry 使用 `https://registry.npmmirror.com`），验证 Driver/容器运行也不读取本地代理
- [x] 8.5 执行 `make generate` clean check、`make test`、`go test ./...`、`go test -race ./...`、`go vet ./...`、PostgreSQL migration/store 集成、容器恢复/数据面隔离验收和全部敏感 canary 扫描
- [x] 8.6 验证停止 Scheduler/Control/PostgreSQL 只暂停采集，模拟 Gateway/Relay Node 数据请求持续成功，且网络计数器仅观察固定账号清单只读 GET、无 Probe/写接口/任意目标
- [x] 8.7 运行 `openspec validate add-control-account-inventory-poll-run-foundation --strict` 和全部主规格 strict 校验；对照 proposal/design/spec/tasks、系统设计 v1.0 与 ADR-0001 记录版本、命令、退出码和无 Secret 证据
- [x] 8.8 整理可独立审查的提交计划，使用 `git status --short`、生成物差异、Migration 范围、临时目录/容器和敏感扫描确认 worktree 只包含本 change 且无 runtime/cache/真实账号数据

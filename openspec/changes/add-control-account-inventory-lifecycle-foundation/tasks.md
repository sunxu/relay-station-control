## 1. 契约、模型与实现边界

- [x] 1.1 对照系统设计 v1.0 第 9.4、9.6、12.1、21、23、24.1 节、ADR-0001 和已归档 poll/snapshot specs，整理 lifecycle 字段、状态、transition reason 与网络非目标对照表，并以 design/spec strict 校验证明无冲突
- [x] 1.2 固化 `present|suspected_missing|missing|out_of_scope`、计数 0/1/2、`missing_since` 第二次缺失时间、`out_of_scope_since` 和 first/last seen 组合，以表驱动 Go/SQL 测试覆盖全部合法与非法组合
- [x] 1.3 定义 lifecycle-aware finalize 与 policy activation 的版本化函数签名、旧函数权限和回滚开关，使用 schema 测试证明新应用不能绕过 lifecycle 路径且旧二进制回滚时可停用两个写路径
- [x] 1.4 建立敏感字段 allowlist，确认 email/account key 只可进入 snapshot/duplicate/lifecycle 受保护列与授权内部 DTO，并以仓库静态扫描证明 OpenAPI、前端和普通观测没有新增逐账号暴露

## 2. Additive Migration、约束与权限

- [x] 2.1 新增下一号 forward Goose Migration 创建 `account_inventory`、唯一键、来源外键、字段白名单和 lifecycle/time/count CHECK，并以 PostgreSQL 18 migrate up 与 schema introspection 验证对象准确
- [x] 2.2 扩展 `account_inventory_provider_states` 的 `monitoring_status`/`out_of_scope_since`、索引和兼容 active 默认，使用带既有 snapshot/provider pointer 的 Migration 测试证明 lifecycle 表保持空且历史 promotion 不被改写
- [x] 2.3 添加受控 immutable/current-state 保护、稳定锁序所需索引和 current poll `ON DELETE SET NULL`，以直接 UPDATE/DELETE/TRUNCATE、跨 Node/poll 和非法 account key 的负向 SQL 测试证明绕过被拒绝
- [x] 2.4 创建 lifecycle-aware finalize 数据库函数，保留 snapshot-only 旧函数并固定 owner、`SECURITY DEFINER`、search_path 与 EXECUTE 权限；以运行时/迁移/未授权角色矩阵测试证明只有预期函数可写 lifecycle
- [x] 2.5 创建 lifecycle-aware Provider policy activation 函数，复用实名 actor、reason、activation history 与 binding 锁；以 SQL 集成测试证明 active→out-of-scope 全有或全无、重新 active 不提前恢复账号、非法/未来范围切换 fail closed
- [x] 2.6 实现受保护 down，只允许 lifecycle 空、无新 out-of-scope 状态且无后续依赖的全新环境恢复旧权限/函数；以空库成功和非空库拒绝测试验证生产状态不会被破坏

## 3. Finalize 生命周期转换

- [x] 3.1 在 lifecycle-aware finalize 中复用已验证 snapshot candidates，以一次数据库 observed time upsert 首次/持续出现账号并刷新白名单状态与来源；用首次基线、重复出现和 first_seen 不变测试验证
- [x] 3.2 实现 present→suspected_missing→missing、计数在 2 饱和且 missing_since 在第二次缺失后稳定，以连续完整快照和完整空集合集成测试验证每一步
- [x] 3.3 实现 suspected_missing/missing 重新出现恢复 present、清零 missing 并保留 first_seen，以恢复前后字段精确断言验证
- [x] 3.4 排除未出现 out_of_scope 账号的 missing 累计，只让重新 active 后实际出现账号恢复 present，以部分旧账号返回、全空和新账号出现测试验证
- [x] 3.5 将 lifecycle 写入 snapshot items、Provider pointer、promotion result 和 poll finalized 的现有 fenced 事务，以中途故障注入证明任一写入失败全部回滚
- [x] 3.6 在 Provider pointer 单调检查后才转换 lifecycle，并以 stale_poll、policy_changed、transport/contract 失败、disk fallback、identity/duplicate 不完整和 abandoned 槽矩阵证明状态与计数完全不变

## 4. 策略并发、幂等与恢复

- [x] 4.1 固定 poll→binding→Provider state→按 account key lifecycle 的锁顺序，并以并发旧/新槽 finalize 和死锁检测测试证明状态只前进且事务有界结束
- [x] 4.2 并发执行 lifecycle finalize 与 active→out-of-scope activation，证明结果只能是旧策略 promotion 先完成或新策略切换先完成且旧 poll policy_changed，两种结果都无混合账号状态
- [x] 4.3 覆盖旧 fencing、过期 lease、已 finalized 重放和重复函数调用，断言影响零行或返回同一终态且 missing 不会重复增加
- [x] 4.4 在 lifecycle upsert、missing 批量更新、Provider state、policy audit 和 COMMIT 前后注入连接中断，验证未提交整体恢复、已提交不重放且提交未知最终只有一份状态
- [x] 4.5 停止/重启 PostgreSQL并模拟连接耗尽、事务超时和 Control 重启，验证不创建内存真相、不 busy-loop、不补历史槽且恢复后只处理下一有效/可恢复 poll

## 5. sqlc、Store 与应用接线

- [x] 5.1 新增 lifecycle-aware finalize/activation 和内部 current lifecycle 查询的 sqlc 源文件，运行 `make generate` 两次并确认第二次无差异、生成文件没有手工编辑
- [x] 5.2 扩展 Store finalize DTO/adapter 调用新函数并保持 Driver observation 生命周期仅限调用栈，以单元测试证明 payload 有界、非法枚举/计数失败且错误不包含身份
- [x] 5.3 将 Provider policy 管理写路径切换到 lifecycle-aware activation，并增加兼容启动检查/开关，以应用集成测试证明新版本拒绝旧函数路径且回滚模式同时关闭 poll 和策略 mutation
- [x] 5.4 实现按 instance、可选 provider/lifecycle、严格 limit 和 account-key cursor 的内部稳定读取，测试空页、边界 limit、组合筛选和排序，同时确认未注册任何 HTTP route
- [x] 5.5 核对 `api/openapi.yaml`、生成 TypeScript 客户端和 React 路由保持不变，并运行现有前端 typecheck/test/build 证明本 foundation 对现有 UI 零回归

## 6. 指标、日志与敏感数据防护

- [x] 6.1 从 PostgreSQL 当前状态实现 lifecycle total 聚合，标签仅允许 instance/provider/lifecycle；若无持久 transition 事实则明确不实现 transition counter，并以进程重启前后指标一致性测试验证
- [x] 6.2 扩展结构化日志 allowlist，只记录固定 operation/result/reason 和受控 instance/provider，以成功、失败、policy race、恢复路径日志测试证明没有逐账号 transition 或 SQL 参数
- [x] 6.3 向 email/account key、endpoint/IP、Secret/Management Key、header/body、版本/提交和 raw error 注入唯一 canary，扫描数据库非允许列、日志、指标、错误、test output 与 acceptance artifact，验证只在三类受保护身份列中允许命中且报告不回显值
- [x] 6.4 以运行时、产品 API、非授权数据库角色尝试枚举和任意写删改 lifecycle，验证最小权限/路由边界拒绝且审计与错误输出脱敏

## 7. 容器、容量与端到端验收

- [x] 7.1 用 fake Driver 覆盖完整/空 runtime、首次/二次缺失、恢复、多 Provider 独立 promotion、所有 skip reason 和 out-of-scope/re-add，断言每轮只有既有固定账号清单 GET
- [x] 7.2 使用 1/10/50 Node 与最大账号记录模型测量 lifecycle 批量写、锁等待、事务时间和 WAL，验证仍满足 120 秒 dispatch grace、30 秒 lease、并发至少 10 与既有容量公式
- [x] 7.3 使用官方 CLIProxyAPI v7.2.141 原版镜像与脱敏 runtime/disk fixtures，经生产 Driver/Worker/PostgreSQL 18 Store 验证 lifecycle；统计证明不修改 Node且不调用 Probe、Gateway、模型数据面或管理写接口
- [x] 7.4 如使用阶段 0 两个真实测试 Node，保持管理请求全局串行且每次成功/失败及最后一次后等待至少 10 秒，只保存脱敏聚合与 lifecycle 分类，并以请求审计证明无额外 GET
- [x] 7.5 停止 Control/PostgreSQL/lifecycle 写路径并持续执行 synthetic 数据面检查，验证只暂停状态推进且 Gateway/Relay Node 模型流量不受影响

## 8. Runbook、证据与最终门禁

- [x] 8.1 编写 lifecycle Runbook，覆盖首次基线、完整空集合、连续缺失、恢复、Provider 移出/重新加入、数据库故障、函数版本检查和脱敏排障，并由命令示例 dry-run 验证可执行
- [x] 8.2 在 Runbook 固化 rollout 与 rollback：先 schema/新二进制后启用，回滚先关闭 poll 与 policy mutation并保留 forward Migration；用隔离环境演练证明旧二进制不删除或重算状态
- [x] 8.3 运行 Migration/schema/Store、全部 Go 单元与集成、`make generate`、`make test`、`make build`、`go test ./...`、`go test -race ./...`、`go vet ./...` 和前端门禁，保存不含敏感值的通过摘要
- [x] 8.4 运行 container acceptance、故障恢复、策略竞态、容量、数据面隔离和完整 canary 扫描，核对每个 spec scenario 都有自动化证据或明确的受控人工证据
- [x] 8.5 运行 `openspec validate add-control-account-inventory-lifecycle-foundation --strict`、全部主规格 strict 校验和 `git diff --check`，对照 proposal/design/spec/tasks 与系统设计确认无漂移
- [x] 8.6 检查 `git status --short`、生成物复现、Migration 范围、OpenAPI/UI 零差异和临时容器/目录，确认 worktree 只包含本 change 实现并整理 Conventional Commits 分层提交计划

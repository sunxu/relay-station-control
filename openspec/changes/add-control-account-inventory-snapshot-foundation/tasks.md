## 1. 边界、模型与标准化契约

- [ ] 1.1 对照系统设计 v1.0 第 9.6、12、13.4、20.3、21.1、23、24.2 节、ADR-0001 和现有 poll/Driver/asset specs，固化 snapshot/promotion 专用边界及生命周期、压缩、告警、API/UI 非目标
- [ ] 1.2 定义 provider/email 最小确定性标准化与 `account_key=normalized_provider+":"+normalized_email`，明确不做 plus-address、别名、文件名/path/name/auth index 或外部目录推断
- [ ] 1.3 定义 snapshot item、duplicate evidence、Provider current state、promotion applied/skipped 的封闭字段、状态、reason、时间和计数上限
- [ ] 1.4 建立敏感字段分类：email/account_key 仅允许进入受保护快照列；与未来 HMAC `account_id` 分离，禁止进入普通日志、指标、错误和 acceptance artifact

## 2. Migration、约束与最小权限

- [ ] 2.1 新增单个 additive Goose Migration，创建 `account_inventory_snapshot_items`、`account_inventory_poll_duplicates`、`account_inventory_provider_states` 并扩展 poll/provider promotion 字段
- [ ] 2.2 添加 Node/poll/provider/account key 外键、唯一键、状态/时间/计数 CHECK、终态不可变 trigger、当前指针单调更新和 `ON DELETE SET NULL` 来源约束
- [ ] 2.3 更新受控 fenced finalize 数据库函数，固定 `SECURITY DEFINER` owner/search_path，运行时角色只获得函数执行和必要只读权限
- [ ] 2.4 实现受保护 down：新表、promotion 标记或后续依赖非空时拒绝；空全新环境可恢复旧函数签名并删除新增对象
- [ ] 2.5 编写 PostgreSQL 18 Migration/权限/schema 集成测试，覆盖非法 key/email、额外 Provider、重复 item、跨 Node poll、直接写删改、TRUNCATE 和非空 down

## 3. 内存投影、账号键与重复预分组

- [ ] 3.1 实现纯函数 provider/email 标准化与 account key 生成，覆盖大小写、首尾空白、空值、非法 UTF-8、长度边界和 Unicode，不泄露输入到错误
- [ ] 3.2 将 Driver `AccountObservation` 投影为有界 snapshot candidate，严格 allowlist status、计数和源时间，拒绝溢出、非法枚举与未知 Provider
- [ ] 3.3 按 `(provider, account_key)` 预分组；唯一组生成 candidate，重复组只生成 occurrence count 证据并令所属 Provider 不完整，不任意选择/合并记录
- [ ] 3.4 验证缺 provider/email、unsupported/out-of-scope 不产生 snapshot item；无法识别内容只影响聚合计数，原记录在投影后释放
- [ ] 3.5 测试 active Provider 合法零记录仍形成完整空快照；多 Provider 中一个重复/缺 identity 不影响其他完整 Provider candidates

## 4. 原子 finalize 与策略并发

- [ ] 4.1 扩展 Repository/finalize request，使 poll 聚合、Provider 结果、duplicates 和 snapshot candidates 一次传入且无法绕过 pinned policy
- [ ] 4.2 固定锁顺序：锁 poll run 并验证 running/lease/fencing，再锁当前 policy binding；策略切换使用同一 binding 锁，禁止 Go 锁或先读后写竞态
- [ ] 4.3 当前 binding 与 pinned policy 不同时保存采集/duplicate 证据，所有 Provider 标记 `promotion_applied=false/policy_changed`，不写 items、不更新指针
- [ ] 4.4 策略相同时为每个 snapshot-complete runtime Provider 批量写入 items、更新 Provider 当前指针并设置 applied=true；空完整 Provider 同样推进指针
- [ ] 4.5 不完整 Provider 不写 items、不改变旧指针，并记录固定 skip reason；同 Node 其他完整 Provider 必须独立 promotion
- [ ] 4.6 在数据库重新计算/核对 key、Provider 全集、candidate/duplicate/聚合计数，任一额外、缺失、重复或矛盾输入使整个 finalize 回滚

## 5. 幂等、fencing、崩溃与恢复

- [ ] 5.1 测试同一 fenced finalize 幂等语义、旧 fencing/过期 lease/终态迟到写影响零行，snapshot/duplicate/provider pointer 不产生部分变化
- [ ] 5.2 注入 snapshot items 批量写、duplicate 写、binding lock、Provider pointer 更新、promotion 标记和 finalized 前后崩溃，验证事务全有或全无
- [ ] 5.3 验证 finalize 提交未知继续使用原 poll/pinned policy/最多两次 Control 恢复；已形成的 Node 失败或 promotion skip 不触发同槽 Node 重试
- [ ] 5.4 并发执行旧/新槽 finalize，证明 Provider 指针只前进不倒退；并发策略切换只能得到“旧策略完整提升”或“policy_changed 不提升”两种结果
- [ ] 5.5 PostgreSQL 停止、连接耗尽、事务超时和重启后从持久 poll 状态恢复，不产生内存当前快照、不 busy-loop、不影响 Gateway/Node 数据面

## 6. Store、读取边界与观测

- [ ] 6.1 新增 sqlc 批量参数/查询与 Store adapter，运行 `make generate` 并证明生成物可复现、没有手改生成代码
- [ ] 6.2 实现受限的 Provider 当前快照读取接口供后续内部生命周期消费；稳定排序、有界页大小，不新增产品 HTTP API
- [ ] 6.3 新增 Provider promotion applied/skipped 指标，严格封闭 instance/provider/reason，验证重启后从 PostgreSQL 当前证据恢复
- [ ] 6.4 扩展结构化日志 allowlist；禁止 email、account_key、poll/policy ID、版本/提交、endpoint、Secret、原始错误和数据库参数
- [ ] 6.5 对 PostgreSQL、日志、指标、错误、test output 和 acceptance artifact 执行 canary 扫描，仅允许标准化 email/account_key 出现在预期受保护快照列

## 7. 容器、容量与综合验收

- [ ] 7.1 用 fake Driver 覆盖 runtime 完整/空、disk fallback、transport/contract 失败、缺 identity、duplicate、unsupported/out-of-scope 和多 Provider 独立 promotion
- [ ] 7.2 使用 1/10/50 Node 与现有 15 秒最坏模型验证扩展 finalize 的事务/锁/WAL 开销仍满足 120 秒 dispatch grace、30 秒 lease 与并发至少 10 的边界
- [ ] 7.3 用官方 CLIProxyAPI v7.2.141 原版镜像和脱敏 runtime/disk fixtures 验证 snapshot/promotion；不修改 Node，不调用 Probe/Gateway/写接口
- [ ] 7.4 如运行阶段 0 两个真实测试 Node，继续全局串行且每次成功/失败后与最后一次后等待至少 10 秒；只记录脱敏计数和 promotion 分类
- [ ] 7.5 验证 Control/PostgreSQL/poll 停止只暂停快照提升，模拟 Gateway/Relay Node 数据面持续成功；网络计数仍只有固定账号清单只读 GET
- [ ] 7.6 编写 snapshot Runbook 与脱敏 evidence，覆盖启停、policy_changed、空快照、指针回退保护、数据库恢复、应用回滚和 forward Migration 保留

## 8. 最终门禁与提交准备

- [ ] 8.1 所有 Go/npm/Docker/make 命令显式清除大小写 HTTP/HTTPS/ALL proxy，npm registry 使用 `https://registry.npmmirror.com`
- [ ] 8.2 执行 `make generate`、`make test`、`make build`、`go test ./...`、`go test -race ./...`、`go vet ./...` 和完整 PostgreSQL migration/store 集成
- [ ] 8.3 运行官方镜像 container acceptance、静态/恢复/数据面隔离验收和全部敏感 canary 扫描，确认无真实账号、Secret、原始响应或 runtime artifact 留在仓库
- [ ] 8.4 运行 `openspec validate add-control-account-inventory-snapshot-foundation --strict` 与全部主规格 strict 校验，对照 proposal/design/spec/tasks 和系统设计 v1.0
- [ ] 8.5 使用 `git status --short`、`git diff --check`、生成物差异、Migration 范围、临时容器/目录扫描确认 worktree 只包含本 change
- [ ] 8.6 整理可独立审查的提交计划，按 OpenSpec、Migration/Store、投影/runtime、观测/安全、app/acceptance/docs 分层提交

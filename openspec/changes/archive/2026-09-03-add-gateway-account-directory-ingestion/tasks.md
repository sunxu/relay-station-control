# add-gateway-account-directory-ingestion Tasks

## 1. Contract and schema foundation

- [x] 1.1 对照 System Design v1.8 / R4.7、ADR-0001/ADR-0002 和已归档 Gateway Directory contract，冻结字段、状态、freshness、失败分类、恢复、generated_at 边界、fingerprint 字段集和非目标对照表
- [x] 1.2 设计并实现 additive Migration，新增 Directory run / snapshot / current-state / item 持久表、唯一键、检查约束、索引，并优先使用最小 Migration + sqlc/store query；仅在跨表原子 invariant 确实需要时才加最小 DB function
- [x] 1.3 为 ingestion run、snapshot 和 current-state 设计最小权限 schema 与幂等键，明确 `UNIQUE (gateway_instance_id, scheduled_at)` 与 `UNIQUE (gateway_instance_id, fingerprint)`，确保旧二进制 forward/backward 兼容
- [x] 1.4 明确复用现有 `gateway_instances.reader_secret_ref`，不新增 Directory 专用 Secret 表或字段

## 2. Ingestion engine

- [x] 2.1 实现每个 Gateway epoch-aligned 180 秒 slot 的 scheduler、`(gateway_instance_id, scheduled_at)` 幂等复用、lease、fencing 和 worker 认领逻辑
- [x] 2.2 实现 Gateway Directory fetch、全量验证、规范化和 content fingerprint 计算，失败时整体拒绝
- [x] 2.3 实现 current pointer、`last_success_received_at`、`last_source_generated_at` 和 run result 的同事务更新
- [x] 2.4 实现 normalized content 未变化时只刷新 observation、变化时按 `(gateway_instance_id, fingerprint)` create-or-reuse immutable snapshot 并推进 current pointer 的分支
- [x] 2.5 固化成功未变化内容必须刷新 `last_success_received_at`、失败/timeout/validation reject/partial read 不刷新的行为
- [x] 2.6 固化 contract 外额外字段整轮 reject、redaction 仅用于诊断输出的行为

## 3. Recovery, idempotency, and freshness

- [x] 3.1 实现 Reconciler，用持久状态恢复过期 / 未知结果 / 重启中的 ingestion run
- [x] 3.2 实现 fresh → stale → recovery 语义，确保失败不刷新 freshness，成功才恢复 fresh
- [x] 3.3 实现每个 Gateway 最多一个 active run 的并发门禁，并补双 worker / 旧 fencing 负向测试
- [x] 3.4 为重复调度、worker 崩溃、提交未知和幂等恢复补恢复测试
- [x] 3.5 固化 commit 前丢失的内存响应不可重放、unknown commit 只靠幂等键/唯一约束/fencing 判断
- [x] 3.6 固化 lease 过期后在允许窗口内复用同一 durable run，否则终结失败的恢复语义

## 4. Security, redaction, and observability

- [x] 4.1 实现 raw response、service token reference、Gateway DB credential、endpoint 敏感字段的脱敏和拒绝路径
- [x] 4.2 为 malformed URL、坏 schema、重复 id、unsafe URL、超限和 source-time sanity 注入 security-negative 测试
- [x] 4.3 补低基数状态指标、失败计数和脱敏日志/审计测试，确保不泄露 raw response 或 Secret
- [x] 4.4 冻结 `generated_at` 的 future tolerance、maximum source age 和 allowed backward skew 及其边界测试
- [x] 4.5 冻结 fingerprint 字段集与固定 ID 升序序列，排除 generated_at/received_at/request_id/run_id/HTTP metadata

## 5. Validation and documentation

- [x] 5.1 补最小的数据库迁移验证和 integration 测试，覆盖成功、未变化、失败、恢复和 stale 边界
- [x] 5.2 更新 Control runbook，说明 ingestion 启停、重启恢复和失败处置
- [x] 5.3 运行 `openspec validate add-gateway-account-directory-ingestion --type change --strict --no-interactive`、相关测试和 `git diff --check`

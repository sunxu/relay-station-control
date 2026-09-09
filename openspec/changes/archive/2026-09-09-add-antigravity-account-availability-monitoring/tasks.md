## 1. Planning and architecture gate

- [x] 1.1 阅读本地Control/CPA实现与canonical，记录source-investigation及安全证据缺口
- [x] 1.2 完成proposal/design/spec和验收矩阵，冻结非争议的六态/阈值/恢复/身份边界
- [x] 1.3 按用户决定关闭A1：UNKNOWN永远不告警，第一版排除other/runtime_unavailable告警；实施仍等待后续授权
- [x] 1.4 修正P1-1独立request_id与P1-2普通403需runtime旁证，完成Architecture复审及strict

## 2. Safe source projection（批准后）

- [x] 2.1 实现Antigravity file模式证明与有界错误白名单，保留basic_status/mode/completeness算法；单元覆盖缺字段/普通403/否定文本/Token canary
- [x] 2.2 贯穿driver/domain/snapshot/current的两个安全nullable字段；通过受控finalize版本原子落库，旧writer兼容，禁止raw字段
- [x] 2.3 Request normalize与versioned insert增加nullable auth_failure_reason，hash/taxonomy/identity/retention不变；覆盖success/401/invalid_grant/明确blocked/403/other/legacy replay；保留Request Quality原计数，availability按非空request_id去重

## 3. Persistence and evaluator（批准后）

- [x] 3.1 新additive migration：安全字段、checkpoint、occurrence、固定三种告警reason CHECK/ACTIVE partial unique；旧函数保留，ACL与Down/Up仅隔离库验证
- [x] 3.2 实现DB时间freshness/health六态判定与15分钟去抖；UNKNOWN永远不告警、other不进确认计数、disabled不创建、不虚假恢复，严格隔离Node与account；仅token/blocked可由两个不同request_id确认，无ID需交叉证据、禁止runtime-only确认，普通403必须有fresh账号级runtime error/unavailable
- [x] 3.3 实现checkpoint行锁/受控事务与bounded keyset reconciliation、未知commit重试/restart幂等，不按reconcile次数累计source
- [x] 3.4 实现成功或相邻两次完整active源恢复、冲突/缺失/degraded打断、旧event抑制及新复发occurrence；保留源摘要抵抗retention
- [x] 3.5 接入既有Inventory lifecycle callback及固定20秒周期，取消/超时隔离，不阻塞原poll/duplicate，不新增Node或Google请求

## 4. Readonly API and workspace（批准后）

- [x] 4.1 扩展账号页availability batch投影及独立occurrence GET，super_admin/no-store/CSRF/审计/keyset绑定，503与业务UNKNOWN分离
- [x] 4.2 更新OpenAPI并make generate，核对generated Go/TS/sqlc，旧客户端/查询字段兼容
- [x] 4.3 账号表Availability/Reason/Since、现有详情只读ACTIVE/RESOLVED证据，复用History/Incidents与系统时区，取消/Node隔离，无mutation

## 5. Acceptance and evidence（批准后）

- [x] 5.1 真实PG确认/恢复/来源计数/迟到/同timestamp/retention/跨Node隔离及并发race；覆盖同ID不同hash、两个不同ID、无ID两个hash拒绝、无ID+runtime允许、两次403+active拒绝、403+unavailable Warning、明确blocked Critical及已确认ID重放，不用mock替代持久证明
- [x] 5.2 PG ACL、旧writer NULL、migration Down/Up、原Inventory/Binding/Duplicate/Quality/History/Incidents回归和negative security
- [x] 5.3 API 401/403/404/400/503、cursor错配、未知与Unavailable；前端六态/loading/empty/失败/分页/详情/无操作测试
- [x] 5.4 100账号/10000events验证batch读取、分页后续账号与reconcile收敛、记录latency/query count，不提前增加缓存/index系统
- [x] 5.5 targeted Go/PG/race、frontend tests/typecheck/build、make test build、change/all strict及git diff --check
- [x] 5.6 完成runbook（no-ACK/不保证实时Google可用/恢复语义/回滚）、planning-validation、Implementation Final Review及工作树检查；未经授权不push/deploy/archive

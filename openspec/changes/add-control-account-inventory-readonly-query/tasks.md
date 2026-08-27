## 1. 契约、字段与安全边界

- [x] 1.1 对照系统设计v1.0第9.4、9.5、12.1、15.3、21、23、24.1节、ADR-0001、lifecycle主规格和现有OpenAPI，整理产品字段allowlist、固定enum、错误码、审计字段与明确非目标对照表
- [x] 1.2 固化`POST /api/account-inventory/query`请求/响应、instance必填、exact email、limit 1–100、`fresh|stale|out_of_scope`和no-store语义，以OpenAPI example/contract测试覆盖合法与非法组合
- [x] 1.3 建立敏感数据流图，证明email只进入request body、受保护SQL参数/列、授权response和短命UI state；account key只进入受保护列/内部DTO/AEAD plaintext，不进入产品schema
- [x] 1.4 定义query/audit事务、并发promotion可见性、实时keyset语义、15分钟cursor TTL和key rotation行为，使用design/spec strict校验证明无未决策略

## 2. Additive Migration、查询函数与最小权限

- [x] 2.1 新增下一号forward Goose Migration，创建版本化`control_query_current_account_inventory_v1`受控函数，固定owner、SECURITY DEFINER/search_path、参数/limit检查和返回字段白名单
- [x] 2.2 为instance/account-key、exact normalized email及常用封闭筛选增加最小必要索引，以1,000账号和最坏筛选`EXPLAIN (ANALYZE, BUFFERS)`证明有界且无多余宽索引
- [x] 2.3 授予runtime role仅新函数EXECUTE并保持账号/snapshot/Provider/asset表无任意SELECT/DML，以runtime、migrator、未授权角色权限矩阵测试验证
- [x] 2.4 验证Migration不新增身份副本、不回填/改写lifecycle、Provider pointer、poll/promotion或snapshot；既有数据前后逐字段校验一致
- [x] 2.5 实现受保护down，只在无后续依赖的全新环境撤销EXECUTE并删除专用函数/索引；生产状态与audit rows必须保留

## 3. sqlc、Store与query语义

- [x] 3.1 新增sqlc源查询和产品query Store DTO/adapter，复用受控函数并对全部枚举、时间、identity组合执行fail-closed验证；DTO formatter保持整体脱敏
- [x] 3.2 实现instance存在与capability资格读取，区分404 not found、409 capability unsupported和503 inconsistent state，不调用Driver或任意外部adapter
- [x] 3.3 实现Provider/lifecycle/basic status/exact email筛选、limit+1和account-key keyset，测试空页、边界limit、组合filter、稳定排序和不返回account key
- [x] 3.4 join Provider current state并派生fresh/stale/out_of_scope，测试degraded+fresh并存、15分钟边界、out-of-scope和缺失Provider state fail closed
- [x] 3.5 并发运行query与完整/空promotion、Provider scope切换和数据库rollback，证明只读取已提交状态、不阻塞状态机、不产生部分/默认填充结果

## 4. AEAD cursor与敏感筛选

- [x] 4.1 为现有auth keyring增加独立account-inventory cursor domain，创建AEAD codec并固定version、key version、environment AAD、随机nonce和长度上限
- [x] 4.2 实现actor、instance、规范化filter hash、after account key、issued/expiry payload及15分钟TTL，测试合法翻页、filter/actor/instance不匹配、过期和旧key短期读取
- [x] 4.3 覆盖篡改、截断、未知字段/version/key、错误tag、超长token和恶意plaintext，所有失败映射同一invalid cursor且不回显细节
- [x] 4.4 使用真实`provider:email` canary验证client token只能看到不可读ciphertext，URL、日志、错误、audit、浏览器storage和test artifact不出现account key/email

## 5. 审计、认证与HTTP Handler

- [x] 5.1 扩展audit category/action和detail allowlist，加入`account_inventory.view`及instance/filter-used/cursor-used/result-count字段，拒绝email/account key/cursor/filter value/hash和未知detail
- [x] 5.2 实现query读取与audit insert同一短事务，只有commit成功后返回；测试空结果、每一后续页、重复请求、commit失败和commit后连接断开
- [x] 5.3 更新OpenAPI并实现生成strict Handler，接入session/super_admin/CSRF、body size/unknown-field拒绝、no-store/request-id和固定400/401/403/404/409/503映射
- [x] 5.4 添加HTTP集成测试，覆盖认证/disabled admin/CSRF、Node资格、所有筛选、分页、并发请求、审计故障和响应字段allowlist
- [x] 5.5 用fake network counters证明Handler、Store、审计和错误路径不调用Node、Gateway、Prometheus、模型数据面或任意URL

## 6. OpenAPI生成与React账号页

- [x] 6.1 在`api/openapi.yaml`新增account-inventory tag、query body、严格schema/enum/error并运行`make generate`两次，确认第二次零差异且不手改Go/TypeScript生成物
- [x] 6.2 新增前端API wrapper、types和TanStack Query hook，确保POST body承载filter/cursor、响应不缓存到持久storage且错误formatter不包含request body
- [x] 6.3 新增账号清单route、导航和页面，Node选择器只展示capability Node，过滤变化清空cursor历史，刷新/unmount清除email与cursor内存state
- [x] 6.4 实现响应式表格与last-reported basic status、lifecycle、degraded、freshness/out-of-scope展示，以及独立loading/empty/400/401/409/503状态
- [x] 6.5 明确排除导出、批量选择、复制按钮、详情、mutation/补采控件，并以DOM/route负向测试防止未批准入口出现
- [x] 6.6 添加桌面、窄屏、键盘、可访问名称、filter reset、前后页、失效cursor、敏感state清理和错误不回显Vitest

## 7. 观测、容量与敏感信息门禁

- [x] 7.1 增加低基数query count/latency/result bucket/error指标，标签只允许operation/result/fixed error；结构化日志只允许固定字段和filter-used布尔值
- [x] 7.2 向email/account key/cursor、endpoint、Secret、poll/policy ID、版本/提交、raw error注入唯一canary，扫描成功、空结果、非法filter、audit failure、cursor failure和UI错误的最终日志/指标/audit/error/artifact
- [x] 7.3 检查request URL、redirect/Location、反向代理access log fixture、浏览器history/localStorage/sessionStorage和前端query cache持久化，证明敏感body/cursor不扩散
- [x] 7.4 使用1/10/50 Node与总计1,000个合成账号测量无筛选、各单筛选、组合筛选、深分页和并发管理员查询P50/P95/P99、DB buffers与audit写入开销
- [ ] 7.5 停止/重启Control和PostgreSQL并模拟连接耗尽、statement timeout、audit commit失败与key rotation，验证query fail closed、恢复后直接读取当前状态且数据面持续通过

## 8. Runbook、证据与最终门禁

- [x] 8.1 编写账号只读查询Runbook，覆盖Node capability、exact email、分页过期、fresh/degraded解释、审计不可用、DB参数日志关闭、key rotation和脱敏排障
- [x] 8.2 固化rollout/rollback：先Migration再新二进制、compatibility gate、导航开放、回滚保留函数/索引/audit且生产不执行down，并在隔离环境dry run
- [x] 8.3 运行Migration/schema/Store、全部Go单元与集成、`make generate`、`make test`、`make build`、`go test ./...`、`go test -race ./...`、`go vet ./...`和前端typecheck/test/build
- [ ] 8.4 运行PostgreSQL18 container acceptance、HTTP/UI、权限、并发promotion、容量、零外部请求、no-store和敏感canary门禁，保存不含身份值的验收摘要
- [x] 8.5 运行`openspec validate add-control-account-inventory-readonly-query --strict`、全部主规格strict校验和`git diff --check`，对照proposal/design/spec/tasks与系统设计确认无漂移
- [x] 8.6 检查`git status --short`、生成物复现、Migration/OpenAPI/UI范围和临时容器/目录，确认worktree只包含本change实现并整理Conventional Commits分层提交计划

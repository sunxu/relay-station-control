## 1. 数据库基础与约束

- [x] 1.1 新增单一 forward Goose Migration，创建 `btree_gist`、endpoint 校验/规范化函数及 `environments` 身份字段不可变保护，并用 PostgreSQL 18 集成测试验证合法 URL、恶意 URL、环境更新和删除行为
- [x] 1.2 在该 Migration 中新增 Gateway、Driver、Driver capability、Relay Node 和 Node capability 关系表及复合外键，并用集成测试验证唯一 Gateway、重复 Node、未知 Driver、未知 capability 和事务原子回滚
- [x] 1.3 新增 Provider 策略版本表及规范化/哈希/集合互斥/不可变约束，并用集成测试验证合法版本、重复内容、集合交叠、非规范名称及更新/删除拒绝
- [x] 1.4 新增 Provider 当前绑定和激活历史表、复合作用域外键与 GiST exclusion constraint，并用集成测试验证同作用域绑定、半开边界、并发重叠、跨作用域和过去生效时间均按设计处理
- [x] 1.5 新增 Node 账号监控激活历史表及区间约束，并用集成测试验证未激活、当前激活、预约激活、半开边界、重叠并发和回填拒绝
- [x] 1.6 为应用运行角色授予资产表只读权限、为受控登记角色授予最小写权限，并用权限测试证明应用角色不能登记/修改资产且登记角色不能读取或修改认证敏感表
- [x] 1.7 实现只允许空资产数据库执行的 Migration down 防护，并在一次性 PostgreSQL 18 实例验证空库可 down、含任一资产/策略/区间历史时 down fail closed 且 `environments` 保持不变

## 2. 环境身份启动门禁

- [x] 2.1 在配置层新增必填 `CONTROL_ENVIRONMENT_ID` 并复用 `CONTROL_ENVIRONMENT` 类型校验，运行配置单元测试覆盖缺失、超长、非法类型和合法配置
- [x] 2.2 新增只读环境单例查询与固定环境校验错误码，并用 store/service 测试验证 ID/type 精确匹配、缺行、错 ID、错类型和数据库错误不泄露值
- [x] 2.3 在 HTTP listener 建立前接入环境校验并禁止自动创建环境，使用进程级测试证明不匹配时非零退出且端口从未监听、匹配时正常启动
- [x] 2.4 更新 Compose、容器验收和示例环境配置以提供已有环境 ID，并运行启动 smoke test 证明升级配置可启动、移除或篡改配置会 fail closed
- [x] 2.5 增加身份错误恢复测试，先以错配置确认失败，再修正同一数据库配置并重启，验证无需修改环境单例即可恢复

## 3. 受控资产登记流程

- [x] 3.1 提供参数化、严格输入且不接受凭证内容的受控 Gateway/Driver/Node/capability 登记事务模板，并在一次性数据库验证相同内容可重放、冲突内容失败且无部分写入
- [x] 3.2 提供从阶段 0 Provider 策略输入创建不可变版本、切换绑定和写激活区间的串行化事务模板，并验证内容哈希确定、同内容幂等、并发切换只有一个成功且历史连续
- [x] 3.3 提供 Node 账号监控开启/关闭/预约的受控事务模板，并验证数据库时间、Node 行锁、固定 reason/actor、重复请求和并发请求不会产生重叠区间
- [x] 3.4 增加登记后对账 SQL，验证环境、Gateway 数量、Node/Driver/capability 外键、当前策略/激活区间和监控区间，并在损坏测试夹具上确认对账明确失败且不输出 Secret 引用

## 4. Store、事务快照与生成物

- [x] 4.1 新增环境、Gateway 和 Driver/capability 的 sqlc 只读查询，确保只投影 `secret_configured` 而不选择 Secret 引用，并用 store 集成测试核对空/非空结果
- [x] 4.2 新增 Node 列表/详情查询、固定过滤条件和数据库计算的监控状态，使用 read-only `REPEATABLE READ` 事务，并用分页测试验证稳定 instance ID cursor、最大 200 条、组合过滤及时间快照
- [x] 4.3 新增当前 Provider 策略/绑定/激活查询，并用 store 集成测试覆盖当前、未配置、预约、历史已结束和不一致数据 fail closed
- [x] 4.4 实现 cursor 编解码与过滤哈希校验，使用单元测试覆盖篡改、跨过滤器重放、非法 UUID、空 cursor 和正常翻页
- [x] 4.5 运行 sqlc 生成并执行 `make generate`/生成物 clean check，验证 Go 模型和查询代码可复现且不存在手工生成文件差异

## 5. OpenAPI 与 HTTP 只读接口

- [x] 5.1 在 `api/openapi.yaml` 定义六组资产 GET 路径、脱敏 schema、空状态、分页、`400/401/404/503` Problem 响应和 `no-store`，运行 OpenAPI lint/契约测试并重新生成 Go 与 Orval 客户端
- [x] 5.2 实现环境与 Gateway handler，使用 HTTP 集成测试验证有效管理员、空 Gateway、Secret 布尔投影、`no-store` 和数据库 `503` 映射
- [x] 5.3 实现 Node 列表与详情 handler，使用 HTTP 集成测试验证分页、过滤、详情 `404`、监控边界和非法 cursor `400`
- [x] 5.4 实现 Driver/capability 与当前 Provider 策略 handler，使用 HTTP 集成测试验证未配置状态、作用域隔离、数组规范顺序和生效时间
- [x] 5.5 将所有资产路由置于现有实名 `super_admin` 会话边界内，并用安全负向测试验证无会话、过期/撤销会话、伪造 cookie 及所有资产写 HTTP 方法均无法读取或修改数据
- [x] 5.6 增加响应与日志泄露测试，向 endpoint、Secret 引用和数据库错误注入 canary，验证 API body、header、日志、Trace、指标和审计中均找不到 Secret 引用、凭证、连接串或 URL 查询内容
- [x] 5.7 增加数据库中断/恢复 HTTP 测试，验证中断时返回脱敏可重试 `503` 且无旧缓存，恢复后下一请求无需重启即可返回当前资产

## 6. 指标与数据面隔离

- [x] 6.1 新增资产数量 gauge 和读取结果 counter 的封闭枚举实现，并用指标测试证明只接受固定 `asset_kind`/`operation`/`result`，拒绝任何请求或资产派生标签
- [x] 6.2 增加外部调用零副作用测试，在读取六组 API 和执行启动身份校验期间监测网络/任务/Outbox/Gateway 写入替身，验证没有 Adapter 请求、账号采集、任务创建或配置修改
- [x] 6.3 运行 Control 故障隔离验收，停止 Control/资产数据库后验证现有 Gateway/Node 请求路径不依赖 Control，并保存可复现命令与结果

## 7. React 资产页面

- [x] 7.1 新增 `/assets` 懒加载路由、导航入口和基于生成客户端的资源 hooks，运行前端路由/代码分割测试证明未访问页面时不加载资产 chunk
- [x] 7.2 实现环境、Gateway、Driver/capability 和当前策略只读卡片及独立空状态，运行组件测试验证未登记/未配置、正常数据、时间显示和无编辑控件
- [x] 7.3 实现 Node 表格、固定过滤器、cursor 翻页和监控状态/区间展示，运行交互测试验证组合过滤、前后翻页、空结果及稳定 key
- [x] 7.4 实现资源级失败状态与显式重试，运行组件测试验证 `401` 交回现有认证流程、`503` 不展示旧数据、内部错误不泄露且恢复后可重试成功
- [x] 7.5 增加浏览器网络与敏感信息测试，验证页面只请求同源 Control API，不直接请求 Gateway/Node endpoint，不渲染 Secret 引用、可点击管理 endpoint 或产品写操作

## 8. 文档、综合验收与证据

- [x] 8.1 更新阶段 1 Runbook 和配置参考，说明 `CONTROL_ENVIRONMENT_ID` 升级前置检查、最小权限登记、策略不可变、监控区间、对账、错误恢复和应用回滚，并用文档命令逐项 dry run
- [x] 8.2 在 PostgreSQL 18 容器执行完整 Migration up、登记、查询、应用回滚、重新升级和受保护 down 流程，保存版本、命令、退出码及不含 Secret 的结果摘要
- [x] 8.3 运行 Go 单元/集成/race 测试、OpenAPI/sqlc 生成检查、前端 lint/typecheck/test/build 和容器验收，记录全部命令及结果并修复非预期跳过
- [x] 8.4 执行专项安全回归，覆盖认证绕过、Secret canary、endpoint 注入、cursor 篡改、数据库最小权限、策略不可变和区间并发，并保存脱敏证据
- [x] 8.5 对照 `proposal.md`、`design.md`、`specs/asset-registry/spec.md` 与系统设计 v1.0 逐项核对实现，确认未引入 Adapter、账号采集、Worker/Outbox、Gateway 写路径或请求数据面依赖
- [x] 8.6 运行 `openspec validate add-control-asset-registry-foundation --strict`、生成物 clean check 和 `git diff --check`，确认任务证据已引用、文档一致且只剩本 change 预期文件
- [x] 8.7 整理可审查提交序列与最终验收摘要，使用 `git status --short` 和提交范围检查证明无临时凭证、测试数据库、运行产物或无关改动

# account-inventory-lifecycle Specification Delta

## MODIFIED Requirements

### Requirement: lifecycle读取与观测 MUST 有界且不泄露身份

Control SHALL 保留仅供内部Store使用的有界、稳定排序lifecycle读取函数，并 MAY 通过`account-inventory-readonly-query` capability向已认证、已启用super_admin提供单Node、有界、逐页审计的产品只读投影。标准化email/account key MAY只进入受保护snapshot、duplicate、lifecycle列、授权内部返回值和该受控产品响应；产品响应 MUST 不包含内部account key。它们与cursor明文、poll/policy ID、版本/提交、endpoint/IP、Secret、header/body和原始错误 MUST NOT进入普通日志、Prometheus标签、错误文本、SQL参数日志、测试输出或验收artifact。运行时角色 MUST无任意lifecycle表SELECT/DML权限。

#### Scenario: 内部读取生命周期
- **WHEN** 内部消费者按instance/provider与有界limit读取当前lifecycle
- **THEN** 数据库以稳定key顺序返回内部白名单字段且不扩大表权限

#### Scenario: 管理员读取当前账号页
- **WHEN** 合法super_admin通过受控query按单个具备capability的instance读取当前lifecycle
- **THEN** API在审计提交后返回产品字段白名单和email，不返回account key或任意内部/外部Secret

#### Scenario: 未经受控query读取身份
- **WHEN** 未授权主体、普通数据库角色或其他产品route尝试枚举lifecycle/email
- **THEN** 认证、route或数据库权限拒绝且不回显身份

#### Scenario: 敏感canary贯穿读取与失败路径
- **WHEN** 验收向email/account key、cursor、Secret、endpoint和原始错误注入唯一canary
- **THEN** 只有受保护身份列、授权产品响应/内部读取及不可读cursor ciphertext可包含相应值，日志、指标、错误、审计details和artifact不包含canary

#### Scenario: lifecycle指标导出
- **WHEN** Control导出当前状态聚合
- **THEN** 标签只包含受控instance/provider/lifecycle/fixed reason且不包含逐账号身份或高基数字段

### Requirement: lifecycle产品读取 MUST 保持只读与数据面隔离

Lifecycle foundation SHALL 允许独立的`account-inventory-readonly-query` capability新增受认证、逐页审计的产品OpenAPI、生成客户端和React当前账号页面，但 MUST NOT允许账号导出、人工状态操作、补采、promotion或删除入口。Query MUST只消费已提交current lifecycle/Provider state，不得改变状态或增加Node请求。Lifecycle仍 MUST NOT实现保留清理、HMAC account ID、跨Node重复、趋势/覆盖率/摘要/压缩或告警路由。它 MUST NOT修改Gateway/Node，也不得增加现有固定账号清单只读GET之外的外部请求。Control/PostgreSQL/query停止只暂停管理读取，不得影响既有模型流量。

#### Scenario: 管理员访问现有产品界面
- **WHEN** readonly query部署后管理员访问产品界面
- **THEN** 可读取有界current账号列表，但没有导出、状态修改、补采、promotion、删除、历史趋势或告警入口

#### Scenario: 查询与promotion并发
- **WHEN** 管理员query与lifecycle-aware finalize并发
- **THEN** query只观察一个已提交数据库状态，不锁住或部分读取未提交promotion，也不改变missing计数

#### Scenario: 网络调用范围验收
- **WHEN** lifecycle、query、恢复和策略切换验收运行
- **THEN** Node请求数量与既有poll-run一致，query不调用Gateway、Prometheus、模型数据面、Node写接口或未登记目标

#### Scenario: Control 或 PostgreSQL 停止
- **WHEN** lifecycle/query依赖停止或旧应用回滚
- **THEN** 已形成状态保留、新状态/读取按相应依赖暂停，Gateway与Relay Node已有模型请求继续

## RENAMED Requirements

- FROM: `### Requirement: lifecycle foundation MUST 保持产品与数据面边界`
- TO: `### Requirement: lifecycle产品读取 MUST 保持只读与数据面隔离`

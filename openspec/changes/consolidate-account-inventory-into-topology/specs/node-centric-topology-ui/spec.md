## MODIFIED Requirements

### Requirement: Topology SHALL 复用统一账号列表和只读详情

Control MUST以/topology作为唯一账号UI入口，以Inventory为账号集合显示账号状态与请求质量；MUST提供provider/email/basic_status/lifecycle/window/quality及25/50/100有界分页与独立手动容量诊断。MUST使用account_key打开既有请求历史及采集抽屉。账号无窗口内请求MUST显示Unknown，read failure MUST显示Unavailable。账号状态与请求质量MUST独立，不改变Inventory、Binding、Duplicate Ownership或运行时状态。MUST删除/account-inventory前端路由和导航，不提供旧链接重定向或alias；后端HTTP API保留。

#### Scenario: 双入口与详情
- **WHEN** 管理员选择Topology Node查看账号
- **THEN** 使用唯一AccountList和只读详情，默认present/15m/25并保留missing等记录；原独立页面与跳转链接不存在

#### Scenario: 无请求与不可用
- **WHEN** Inventory账号存在但窗口内无请求，或质量读取失败
- **THEN** 前者保留账号且显示Unknown/0/null，后者Unavailable而非空列表

#### Scenario: 切换Node与Incident
- **WHEN** 切换Node、浏览器popstate或Incident选择账号
- **THEN** Node变化清空旧筛选/页/详情并取消隔离旧请求，自动默认首页；Incident按当前Node/account_key打开同一详情

#### Scenario: 容量诊断
- **WHEN** 管理员手动刷新容量，返回可用/超限/禁用或失败
- **THEN** 展示现有环境容量及超限原因或Unavailable，401沿用会话退出；不改变调度或账号状态

#### Scenario: 旧前端地址移除
- **WHEN** 打开/account-inventory或其instance_id链接
- **THEN** 按既有未知路径规则处理，不加载旧页，不读取该参数查询账号，也不跳转到Topology

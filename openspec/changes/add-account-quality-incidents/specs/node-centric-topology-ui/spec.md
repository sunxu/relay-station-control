## ADDED Requirements

### Requirement: Topology SHALL 展示只读Account Incidents

现有Node detail SHALL提供Incidents七列表Account/Provider/Reason/Status/Hits/First Seen/Last Seen，突出Active且固定active-only，支持Provider/四类Reason过滤和有界分页。点击Account MUST使用精确account_key打开已有Request History。MUST NOT增加resolve/disable/账号retry/ack/comment或其它mutation，也不进入通知、自动处置、Durable Jobs、Prometheus/Grafana。

#### Scenario: 四种读取状态
- **WHEN** 等待读取、成功无匹配、读取错误或有Incident
- **THEN** 分别Loading/Empty/Unavailable/Populated，503不能被解释成Empty

#### Scenario: 过滤分页与History
- **WHEN** 切换provider/reason、翻页或点击Account
- **THEN** 过滤变化回首页，分页保留filters，History复用当前Node和canonical account_key而非email/index

#### Scenario: Node与会话隔离
- **WHEN** 切Node或旧请求迟到、当前session失效
- **THEN** 清Node范围分页与History选择、取消旧query，迟到success/401不污染新Node；当前401按现有方式清理会话

#### Scenario: 无副作用
- **WHEN** 查看active或读取失败后重试
- **THEN** 只读取现有证据，不改变Inventory/lifecycle/Binding/Duplicate/Quality/CLIProxy状态，不展示recovered伪状态

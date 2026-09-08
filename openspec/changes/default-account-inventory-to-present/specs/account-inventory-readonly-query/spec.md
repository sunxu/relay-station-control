## MODIFIED Requirements

### Requirement: React账号页 MUST 清楚呈现筛选、降级和错误状态

Control SHALL在已认证管理员的Topology内提供唯一账号UI，支持Provider、lifecycle、basic status、exact email、15m/1h质量、quality、25/50/100及前后页。MUST区分loading、empty、invalid、unsupported、unauthorized、unavailable；MUST区分basic status、lifecycle、Provider degraded、snapshot freshness。MUST NOT提供导出、批量选择、状态修改、删除、补采或promotion控件。

/topology?instance_id首次进入或选择另一Node MUST自动默认第一页，StrictMode不得重复提交；无Node不查询。编辑筛选 MUST清空cursor/结果/详情，只在显式查询时提交；翻页使用当前过滤的cursor历史。MUST使用现有Topology组合POST、CSRF/加密cursor/逐页审计，保留原Inventory HTTP API。email/cursor MUST仅在内存与body，离开时丢弃，不能写入URL、浏览器存储或持久查询缓存。失败不自动重放，允许手动重试；切换Node取消旧请求，旧响应不得污染新Node。


页面 MUST 默认使用lifecycle=present；管理员可选择其它既有生命周期或清空查询全部，仅改变读取过滤，不删除记录或修改Inventory truth。
#### Scenario: 深链接和StrictMode
- **WHEN** 打开携带instance_id的Topology，包括StrictMode
- **THEN** 默认present/15m/25首页只提交一次，失败展示真实状态且不自动循环；无效Node不回退其他Node

#### Scenario: 无Node及切换
- **WHEN** 无Node进入后选择Node或从一个Node切换另一个
- **THEN** 未选择不查，选择后默认首页；重置旧筛选/页/详情并取消隔离旧响应

#### Scenario: 显式筛选与分页
- **WHEN** 编辑过滤或每页数、查询、前后翻页
- **THEN** 编辑不发请求并清空cursor；查询提交规范化参数，翻页沿用同一筛选，敏感值只在POST body

#### Scenario: 错误与可访问性
- **WHEN** API返回400/401/403/404/409/503或使用键盘/窄屏
- **THEN** 保留现有错误类别和手动恢复，不把失败当空；输入/状态/按钮有可访问名称，关键内容可用

#### Scenario: 从带 instance_id 的链接首次加载
- **WHEN** 已认证管理员进入/topology?instance_id深链接
- **THEN** 预选Node并自动默认第一页一次

#### Scenario: Node不具备账号清单能力
- **WHEN** 当前Node不具备账号清单capability
- **THEN** 账号API继续拒绝并显示unsupported，不绕过capability校验

#### Scenario: 过滤器变化
- **WHEN** 管理员编辑筛选
- **THEN** 清空cursor/结果并等待显式查询；切换Node另按默认首页规则

#### Scenario: 窄屏或键盘访问
- **WHEN** 使用窄屏或键盘
- **THEN** 筛选、分页、状态及错误具备可访问名称

#### Scenario: URL 缺少 instance_id
- **WHEN** 无instance_id进入Topology
- **THEN** 未选Node不查，选择后自动默认首页，不再保留旧页面手动Node查询语义

#### Scenario: instance_id 无效或不受支持
- **WHEN** Topology深链接Node无效或不支持
- **THEN** 显示实际API错误，不回退查询其他Node

#### Scenario: 首次自动查询失败
- **WHEN** 首次读取失败
- **THEN** 显示真实错误，允许手动重试，不自动循环或伪造空结果

#### Scenario: 首次加载后编辑筛选
- **WHEN** 首次读取后编辑筛选
- **THEN** 清空cursor并只在显式查询后提交

#### Scenario: 首次加载后切换 Node 或翻页
- **WHEN** 切换Node或前后翻页
- **THEN** Node切换默认首页且隔离旧响应，翻页使用当前过滤cursor，初始URL不覆盖选择

#### Scenario: 默认只查询当前账号

- **WHEN** 管理员首次进入页面并由深链接或手动动作发起查询
- **THEN** 请求使用 lifecycle=present 和默认第一页，其它初始化及 StrictMode 规则保持不变

#### Scenario: 查看缺失或全部账号

- **WHEN** 管理员选择 missing 或清空生命周期筛选
- **THEN** 页面重置 cursor 且不自动提交；点击查询后分别提交 lifecycle=missing 或不传 lifecycle，并可显示既有缺失记录

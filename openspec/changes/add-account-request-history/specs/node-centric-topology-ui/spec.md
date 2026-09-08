## MODIFIED Requirements

### Requirement: Account Quality UI SHALL 区分四种读取状态

现有Node detail SHALL增加Account/Provider/Quality/Success/Requests/P95/Last Failure只读表格，支持15m/1h、现有Node Provider集合、All/Good/Degraded/Bad/Unknown、bounded pagination。SHALL以account_key为row identity；无值显示—，失败仅class和时间；MUST NOT展示raw failure body、mutation、request detail、chart或account action；仅允许View History进入本Node内的单账号只读Request History。

#### Scenario: Loading empty unknown unavailable
- **WHEN** 请求等待、成功空集合、账号无请求、请求错误
- **THEN** 分别显示loading、empty、Unknown账号行、Unavailable，不互相替代

#### Scenario: 筛选分页和Node切换
- **WHEN** 切换window/provider/quality或Node
- **THEN** 使用匹配参数读取并重置分页，旧Node延迟结果不能覆盖新Node；下一页保留筛选

#### Scenario: 只读交互
- **WHEN** 浏览、切换筛选、翻页或重试
- **THEN** 仅执行读取，不出现account action、quota、inspection、automation或全局health score

### Requirement: Topology SHALL 保护身份并安全恢复读取

页面及新history读取SHALL沿用实名super_admin会话、no-store与request ID。canonical account_key按最新duplicate规格仅供管理员文本展示，MUST NOT进入浏览器导航URL/storage/logs/metrics；仅Account Request History GET的account_key参数及内部cursor允许在认证HTTP请求URL中传递账号身份；不得泄露凭据或raw response。各区SHALL独立loading/empty/unavailable/retry，401清理全部会话数据，切换Node/筛选丢弃迟到响应；UTC来源时间保持可见，重启只重读数据库。

#### Scenario: 局部失败与乱序
- **WHEN** B已选中而A请求迟到，或History失败但Binding成功
- **THEN** A结果不覆盖B；History显示unavailable且不清除独立成功Binding，更不发mutation

#### Scenario: 会话与窄屏
- **WHEN** 会话失效，或在390px/桌面/键盘模式查看
- **THEN** 失效清理会话和内存；有权限时各区和两个Provider badge均可辨识，可操作控件有可访问名称且状态不只靠颜色

## ADDED Requirements

### Requirement: Topology SHALL 提供单账号Request History区域

Account Quality SHALL通过View History以account_key选择目标账号，在当前Node detail显示最近7天的Time/Model/Result/Failure/Latency/Request ID。成功显示Success及failure —，失败显示Failed及原五类failure；duration NULL和request ID空显示—。MUST NOT展示raw body/headers/tokens/prompt/completion、详情页、chart/export/search/date picker、retry request或account actions。

#### Scenario: 五种读取状态
- **WHEN** 无选中账号、等待请求、账号存在无事件、读取失败或存在事件
- **THEN** 分别显示Select an account to view request history、独立Loading、7天无事件Empty、Unavailable和历史表；404提示账号不存在，不能把错误当empty

#### Scenario: 分页和账号切换
- **WHEN** 翻页或选择另一个账号
- **THEN** 翻页使用专属cursor，换账号清cursor，用精确account_key而非email/index/display text发请求

#### Scenario: Node切换和迟到结果
- **WHEN** 切换Node时旧History未返回
- **THEN** 清除已选账号并取消旧请求，迟到结果不得覆盖新Node；Quality筛选分页可保留同Node明确账号上下文

#### Scenario: 无副作用
- **WHEN** 选择账号、读历史、翻页或读取错误重试
- **THEN** 仅查询既有证据，不改变Inventory/lifecycle/Binding/Duplicate/quality classification或CLIProxy状态

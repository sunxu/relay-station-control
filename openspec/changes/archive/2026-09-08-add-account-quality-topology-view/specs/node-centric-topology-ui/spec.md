## MODIFIED Requirements

### Requirement: Topology SHALL 保持纯只读职责

Topology SHALL 展示Node资产、Inventory evidence、Binding truth/resolution和Duplicate Ownership，MUST NOT 提供或调用bind/rebind/unbind、candidate submit、account mutation、自动修复或采集/调度操作。Topology MUST NOT 增加expected_binding_id或修改既有Binding事务。若已有独立管理UI且已验证可用，Topology只能deep-link到该入口；当前没有Binding管理UI时MUST NOT 创建替代mutation页面或无效链接。

#### Scenario: 浏览与刷新没有写入
- **WHEN** 管理员打开Topology、切换Node、翻页、刷新或从错误恢复
- **THEN** 只发生只读查询，不调用Binding action或数据面，不创建确认草稿或mutation恢复队列

#### Scenario: 不存在Binding管理入口
- **WHEN** 仓库没有独立Binding管理页面
- **THEN** Topology不展示候选提交控件和管理绑定链接，不能因UI便利而新增bind/rebind/unbind

#### Scenario: 账号清单导航
- **WHEN** 管理员需要查看既有账号清单
- **THEN** 仅deep-link稳定Node ID；原页面自己的CSRF/view audit保留，不由Topology绕过或执行该管理页面查询；独立Account Quality只读投影可复用Inventory安全读取，不改变原页面审计

## ADDED Requirements

### Requirement: Inventory 账号集合 SHALL 驱动 Account Quality

Node detail SHALL 提供单账号Account Quality表格，以现有Node current Inventory集合为准，LEFT merge已有15m/1h统计。MUST返回account_key opaque string、email、provider、quality、request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class。MUST NOT由event枚举账号或修改Inventory/lifecycle/Binding/Duplicate/CLIProxy状态。

#### Scenario: 零请求账号仍可见
- **WHEN** Inventory账号存在且窗口内无请求
- **THEN** 账号仍返回，三项count为0、rate/p95/last_*为null、quality为unknown

#### Scenario: Unresolved 不污染账号
- **WHEN** 同Node包含account_key=NULL事件或只有events没有Inventory的账号
- **THEN** 不归属其它账号且不制造fake row；原Node/Provider聚合不变

### Requirement: Quality SHALL 仅表示近期请求表现

分类 MUST仅存在read/API/UI层：count=0为unknown；有请求时success_rate>=95%为good，>=80%且<95%为degraded，<80%为bad。Latency MUST NOT参与分类或制造持久health score。

#### Scenario: 分类边界
- **WHEN** 请求成功率分别为100%、95%、94%、80%、79%
- **THEN** 分别为good、good、degraded、degraded、bad；active Inventory与bad并存

### Requirement: Account Quality API SHALL 有界且安全

GET `/api/topology/nodes/{instance_id}/account-quality` SHALL使用既有super_admin read授权、no-store，默认window15m/provider All/quality All/limit25；仅允许15m或1h，quality good/degraded/bad/unknown，limit1–100。SHALL provider和quality过滤后按account_key稳定cursor分页；cursor MUST绑定Node与filters。MUST复用已有统计，不逐账号发起Go到DB或HTTP请求。

#### Scenario: 过滤跨越Inventory页
- **WHEN** 满足quality的账号位于第一个Inventory chunk之后
- **THEN** 仍在对应过滤分页中可见，页大小有界且顺序确定

#### Scenario: 授权与参数
- **WHEN** 无session、非super_admin、非法window/quality/limit或跨filter cursor
- **THEN** 分别返回401、403或400，不读取账号结果

#### Scenario: 数据库失败和恢复
- **WHEN** read model/DB失败后重试恢复
- **THEN** 失败返回503不可用，不能empty或unknown；恢复后返回真实结果

#### Scenario: 100账号读取
- **WHEN** Node包含100 Inventory账号，查询两种窗口或provider/quality过滤
- **THEN** 使用一个store composition数据库请求，无账号级网络fan-out，记录内部函数成本和latency

### Requirement: Account Quality UI SHALL 区分四种读取状态

现有Node detail SHALL增加Account/Provider/Quality/Success/Requests/P95/Last Failure只读表格，支持15m/1h、现有Node Provider集合、All/Good/Degraded/Bad/Unknown、bounded pagination。SHALL以account_key为row identity；无值显示—，失败仅class和时间；MUST NOT展示raw failure body、mutation、history/drill-down、chart或action。

#### Scenario: Loading empty unknown unavailable
- **WHEN** 请求等待、成功空集合、账号无请求、请求错误
- **THEN** 分别显示loading、empty、Unknown账号行、Unavailable，不互相替代

#### Scenario: 筛选分页和Node切换
- **WHEN** 切换window/provider/quality或Node
- **THEN** 使用匹配参数读取并重置分页，旧Node延迟结果不能覆盖新Node；下一页保留筛选

#### Scenario: 只读交互
- **WHEN** 浏览、切换筛选、翻页或重试
- **THEN** 仅执行读取，不出现account action、quota、inspection、automation或全局health score

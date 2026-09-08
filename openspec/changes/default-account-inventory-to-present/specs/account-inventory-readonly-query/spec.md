## MODIFIED Requirements

### Requirement: React账号页 MUST 清楚呈现筛选、降级和错误状态

Control SHALL 为已认证管理员提供账号清单页面。页面 MUST 先选择具备账号清单capability的Node，支持Provider、lifecycle、basic status和exact email筛选及前后页导航，并分别呈现loading、empty、invalid filter、unsupported、unauthorized和unavailable状态。页面 MUST 明确区分last-reported basic status、lifecycle、Provider degraded和snapshot freshness，且 MUST NOT提供导出、批量选择、详情、状态修改、删除、补采或promotion控件。

当页面 URL 含 `instance_id` 时，页面 MUST 在首次加载阶段预选该值，并自动提交一次现有默认筛选的第一页查询。Node 的合法性、登记状态和 capability 继续由既有 API 校验；自动查询完成或失败后，页面 MUST 呈现现有结果或对应错误状态，不得因失败而自动循环重试。URL 缺少 `instance_id` 时，页面 MUST 保持手动选择 Node 后由管理员触发查询的行为。

首次自动查询只适用于页面初始进入。管理员编辑筛选或切换 Node 时，页面 MUST 清空 cursor 历史；翻页沿用现有 cursor 历史。请求由现有显式查询/分页动作提交；这些动作不得被隐式的 URL 自动加载逻辑再次覆盖。该行为 MUST 复用现有 POST 只读查询及其查看审计，不新增业务写请求、补采、状态变更或数据面调用。

页面 MUST 默认使用 `lifecycle=present` 查询当前账号；该默认值同时适用于深链接初次自动查询和无深链接时的首次手动查询。管理员 MUST 能显式选择其它既有生命周期，或清空生命周期条件查询全部记录；该操作只改变读过滤，不删除缺失记录或修改 Inventory truth。

#### Scenario: 从带 instance_id 的链接首次加载

- **WHEN** 已认证管理员打开包含合法、受支持 `instance_id` 的账号清单 URL，且未提交筛选
- **THEN** 页面预选该 Node，并自动执行一次默认筛选第一页查询，成功后显示该 Node 的账号结果

#### Scenario: Node不具备账号清单能力

- **WHEN** 管理员浏览Node选择器
- **THEN** 页面不为不支持capability的Node提供账号查询入口

#### Scenario: 过滤器变化

- **WHEN** 管理员修改任一筛选或切换Node
- **THEN** 页面清空cursor历史、回到第一页并只提交新筛选body

#### Scenario: 窄屏或键盘访问

- **WHEN** 管理员在窄屏或只使用键盘操作筛选和分页
- **THEN** 所有输入、状态、按钮和错误具有可访问名称且不因布局隐藏关键信息

#### Scenario: URL 缺少 instance_id

- **WHEN** 已认证管理员打开不带 `instance_id` 的账号清单 URL
- **THEN** 页面不自动查询，管理员选择受支持 Node 后点击现有查询动作才加载第一页

#### Scenario: instance_id 无效或不受支持

- **WHEN** URL 中的 `instance_id` 不是合法 UUID、未知、未启用或不具备账号清单 capability
- **THEN** 页面提交一次既有默认查询并显示 API 返回的 invalid、unsupported 或 unavailable 状态；不得回退查询其他 Node

#### Scenario: 首次自动查询失败

- **WHEN** 带 `instance_id` 的首次默认查询因认证、网络、数据库或受控查询错误失败
- **THEN** 页面显示对应 unauthorized 或 unavailable 状态，且同一次进入不会自动循环重试或伪造为空结果

#### Scenario: 首次加载后编辑筛选

- **WHEN** 首次自动查询完成后管理员修改 Provider、lifecycle、status 或 exact email 筛选
- **THEN** 页面清空 cursor 历史，并只在管理员触发现有查询动作后提交修改后的筛选第一页

#### Scenario: 首次加载后切换 Node 或翻页

- **WHEN** 管理员切换 Node 或使用下一页/上一页
- **THEN** 页面按现有显式动作重新查询对应 Node 或 cursor 页面，且 URL 初始 `instance_id` 不会覆盖该后续选择

#### Scenario: 默认只查询当前账号

- **WHEN** 管理员首次进入页面并由深链接或手动动作发起查询
- **THEN** 请求使用 lifecycle=present 和默认第一页，其它初始化及 StrictMode 规则保持不变

#### Scenario: 查看缺失或全部账号

- **WHEN** 管理员选择 missing 或清空生命周期筛选
- **THEN** 页面重置 cursor 且不自动提交；点击查询后分别提交 lifecycle=missing 或不传 lifecycle，并可显示既有缺失记录

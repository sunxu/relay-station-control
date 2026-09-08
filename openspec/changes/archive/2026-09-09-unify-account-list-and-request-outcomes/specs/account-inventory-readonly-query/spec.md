## ADDED Requirements

### Requirement: Account Inventory UI SHALL 复用统一账号工作视图

账号清单入口MUST保留Node选择、provider/email/basic_status/lifecycle、page size、容量诊断及既有深链接，使用与Topology相同的账号列表与详情组件展示Inventory与请求质量。旧Inventory HTTP API MUST继续保持其既有安全与读取契约；新UI使用Topology组合只读POST及既有CSRF/AEAD cursor/view audit，不新增mutation。

#### Scenario: 初始深链接
- **WHEN** 进入携带instance_id的账号页面，包括StrictMode
- **THEN** 预选Node并只触发一次默认present首页请求

#### Scenario: 手动筛选
- **WHEN** 无instance_id进入后选择Node，或编辑筛选
- **THEN** 保持显式查询交互；提交后过滤先于分页，不拼接两套结果

#### Scenario: 数据诊断入口
- **WHEN** 管理员查看缺失记录或采集信息
- **THEN** 保留生命周期筛选、容量诊断和采集详情，无需重新显示第二份账号表

## MODIFIED Requirements

### Requirement: React账号页 MUST 清楚呈现筛选、降级和错误状态

Control SHALL 为已认证管理员提供账号清单页面。页面 MUST 先选择具备账号清单capability的Node，支持Provider、lifecycle、basic status和exact email筛选及前后页导航，并分别呈现loading、empty、invalid filter、unsupported、unauthorized和unavailable状态。页面 MUST 明确区分last-reported basic status、lifecycle、Provider degraded和snapshot freshness，且 MUST NOT提供导出、批量选择、状态修改、删除、补采或promotion控件。

当页面 URL 含 `instance_id` 时，页面 MUST 在首次加载阶段预选该值，并自动提交一次现有默认筛选的第一页查询。Node 的合法性、登记状态和 capability 继续由既有 API 校验；自动查询完成或失败后，页面 MUST 呈现现有结果或对应错误状态，不得因失败而自动循环重试。URL 缺少 `instance_id` 时，页面 MUST 保持手动选择 Node 后由管理员触发查询的行为。

首次自动查询只适用于页面初始进入。管理员编辑筛选或切换 Node 时，页面 MUST 清空 cursor 历史；翻页沿用现有 cursor 历史。请求由现有显式查询/分页动作提交；这些动作不得被隐式的 URL 自动加载逻辑再次覆盖。统一账号页面 MUST 使用新增Topology组合POST读取，并复用CSRF/加密cursor/逐页审计；旧POST只读查询及其查看审计契约保留给既有消费者。允许只读请求历史/采集详情抽屉，不新增业务写请求、补采、状态变更或数据面调用。

#### Scenario: 从带 instance_id 的链接首次加载

- **WHEN** 已认证管理员打开包含合法、受支持 `instance_id` 的账号清单 URL，且未提交筛选
- **THEN** 页面预选该 Node，并自动执行一次默认筛选第一页查询，成功后显示该 Node 的账号结果

#### Scenario: Node不具备账号清单能力

- **WHEN** 管理员浏览Node选择器
- **THEN** 页面不为不支持capability的Node提供账号查询入口

#### Scenario: 过滤器变化

- **WHEN** 管理员修改任一筛选或切换Node
- **THEN** 页面清空cursor历史、回到第一页并在管理员显式查询后提交新筛选参数

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


### Requirement: query request MUST 避免在 URL 暴露敏感筛选

原Account inventory query MUST继续使用`POST /api/account-inventory/query`；统一列表使用`POST /api/topology/nodes/{instance_id}/account-quality/query`。两者MUST以JSON body承载敏感filters/cursor/limit，email、cursor和account identity MUST NOT出现在列表URL、redirect、Location、普通log或浏览器持久storage。统一响应允许既有Topology canonical account_key用于只读详情关联，不改变原Inventory响应。所有响应MUST no-store。既有Request History独立API契约保持不变。

#### Scenario: 管理员按 email 定位账号
- **WHEN** 管理员提交规范化exact email filter
- **THEN** email仅在受保护POST body、数据库比较与授权响应，不进入列表URL或重定向

#### Scenario: 非法敏感筛选
- **WHEN** email/provider超长、标准化为空、enum非法或body含未知字段
- **THEN** 返回固定400，错误/日志/审计不回显输入

### Requirement: query MUST 有界、稳定并绑定全部筛选

请求 MUST 要求一个 instance，原Inventory POST limit SHALL默认50，统一账号POST SHALL默认25；两者只允许1至100。Control MUST 在 Provider、lifecycle、basic status 和规范化 email 精确筛选后（统一账号POST另含window/quality）按不可变 account key升序执行 keyset pagination，并只在存在下一行时返回 cursor。它 MUST NOT 提供无界结果、offset pagination、模糊/前缀 email 搜索或跨 Node 聚合。

#### Scenario: 多页读取稳定账号集合
- **WHEN** 第一页达到 limit且存在额外行，管理员在相同 instance和filters下提交 next cursor
- **THEN** 下一页从上一页最后 account key之后继续，既有 key不重复；原Inventory API不返回内部account key，统一账号响应沿用Topology canonical account_key作为详情identity

#### Scenario: filter 变化后重放 cursor
- **WHEN** 客户端把 cursor用于不同 instance、Provider、lifecycle、basic status或email筛选（统一POST包括window/quality）
- **THEN** Control 以统一 invalid cursor 400拒绝，不执行部分查询或泄露 cursor payload

#### Scenario: promotion 与翻页并发
- **WHEN** lifecycle promotion在两个 page请求之间更新状态或插入新账号
- **THEN** 每页都返回请求时的 current view且按不可变 key继续；Control不声称跨页冻结快照，也不按更新时间重排既有账号

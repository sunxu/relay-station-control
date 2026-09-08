# account-inventory-poll-capacity Specification

## Purpose
让 Control 管理员无需随 Node 数量调整采集容量配置，以已有并发和时间预算自动确定可支持规模，并明确区分容量超限、采集关闭与读取失败，保留数据库调度和账号证据的既有职责边界。

## Requirements

### Requirement: Control SHALL 推导唯一内部采集容量
Control SHALL 取满足 `(ceil(N/C)-1)*(T+F+L)+N*Q+M<G` 的最大整数 N，且 `1<=N<=50`；C 为配置并发，T 为最坏请求时间，F 为 finalize timeout，L 为固定30秒生命周期回调预算，Q 为固定1秒claim预算，M 为 dispatch margin，G 为启动 grace。MUST 使用精确 duration 运算，等号不合格，无可行容量时拒绝启动。推导值 SHALL 同时用于启动验证和调度上限，不保留独立人工最大 Node 数。已有 lease/fencing/retry、5 分钟周期和 HTTP 并发限制 MUST 不变。

#### Scenario: 默认时间预算
- **WHEN** T=15秒、F=10秒、L=30秒、Q=1秒、M=10秒、G=120秒，C分别为1、2、7、10、25
- **THEN** 容量分别为2、4、14、20、50；50 Node / C=7 明确超限

#### Scenario: 时间边界
- **WHEN** 某 N 的最后批次启动时间加余量恰好等于 grace
- **THEN** N 不合格，系统选取更小的最大可行容量；若一个 Node 也不合格则启动失败

### Requirement: 超限 SHALL 保持整轮拒绝并可恢复
当数据库当前 UTC slot 的 eligible Node 数大于推导容量时，Control MUST 原子拒绝本轮新调度并分类 `capacity_exceeded`，不得截取前 N、改变 monitoring activation、伪造 poll 或刷新快照。既有运行任务继续受原 lease/fencing 约束。数量恢复后 SHALL 自动在正常可调度槽恢复，不补造过期槽。

#### Scenario: 超限及恢复
- **WHEN** C=1时 eligible Node 从2增加到3，再回到2
- **THEN** 3时本轮不新增 poll，既有证据保留；回到2后正常调度恢复，无需重启

### Requirement: 管理员 SHALL 读取明确容量诊断

认证管理员 SHALL 在账号清单页面看到全环境当前槽的 enabled、eligible Node 数、推导容量、并发、时间预算、按浏览器系统时区展示的评估槽与评估时间，以及 `ready|capacity_exceeded|disabled` 状态。诊断 MUST 使用与调度相同的 eligibility 和容量公式，明确这是当前条件评估而不是最近一次成功调度。数据库或配置读取失败 MUST 返回 unavailable，不能返回零或ready。超限提示 SHALL 明确说明整轮新调度暂停，提供调整并发或通过既有管理流程减少监控 Node 的建议；不得自动修改配置或监控。评估槽的 instant、接口传输和数据库时间仍遵循既有 UTC 契约，只有用户可见格式化使用系统时区与 `YYYY-MM-DD HH:mm:ss`。

#### Scenario: 超限可见
- **WHEN** eligible=3、capacity=2
- **THEN** 页面显示3/2及容量不足原因，即使未选择Node或尚未执行账号查询也能显示；不自动查询账号或生成账号查看审计

#### Scenario: 未授权和不可用
- **WHEN** 未认证访问或数据库不可用
- **THEN** 分别返回既有401或503错误；页面不得把失败显示为空任务、无账号或容量正常

#### Scenario: 重启与配置关闭
- **WHEN** Control重启或poll关闭
- **THEN** 重启后从数据库和当前配置重新评估；关闭时显示disabled，不谎称正在调度，也不丢失历史账号证据

#### Scenario: 策略不一致
- **WHEN** 有已启用监控且具备能力的Node，但当前槽策略缺失或不匹配
- **THEN** 诊断返回503 unavailable，不把该Node从计数隐去并显示ready

### Requirement: 旧环境变量 SHALL 有明确兼容行为
Control SHALL 忽略旧 `CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES`，存在时每次进程启动只发出固定弃用警告，不记录其原值。Ops SHALL 在部署新版时清理Compose与部署override的旧变量。新配置 MUST 不因旧值缺失、无效或偏小改变推导结果。

#### Scenario: 遗留低上限
- **WHEN** 旧变量仍为1、并发为1，eligible=2
- **THEN** 推导容量为2并允许正常调度，启动记录弃用警告

## MODIFIED Requirements

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

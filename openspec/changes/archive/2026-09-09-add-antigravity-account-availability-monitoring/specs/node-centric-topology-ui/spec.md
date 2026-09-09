## ADDED Requirements

### Requirement: Antigravity account workspace SHALL expose read-only availability

现有Account/Account Quality workspace SHALL为Antigravity file账号增加Availability、Reason、Since，状态仅Available/Token Invalid/Account Blocked/Forbidden/Unknown/Disabled；其他Provider不适用显示—，DB/API失败显示Unavailable而不是Unknown/Empty。账号集合继续来自当前Inventory并沿用现有lifecycle过滤。Since按浏览器系统时区YYYY-MM-DD HH:mm:ss；未知开始时间为—。

账号点击 MUST继续复用现有Request History/Incidents；availability occurrence可在现有账号详情内独立展示ACTIVE/RESOLVED、severity、确认和恢复时间，不改变Incidents的active-only聚合。MUST NOT增加mutation、raw detail、Token、账号操作或新独立应用。

#### Scenario: Availability and quality are independent
- **WHEN** Inventory present账号有Bad Quality但已可靠恢复可用，或无请求但fresh active
- **THEN** Availability可为Available且Quality仍为Bad/Unknown，不改质量分类或Inventory truth

#### Scenario: Select account and switch Node
- **WHEN** 选择账号或切换Node
- **THEN** 使用原account_key identity打开History/Incidents及只读availability证据；取消旧请求并隔离Node结果，无disable/delete/re-auth/resolve控件

#### Scenario: Unknown disabled and unavailable
- **WHEN** 安全读取证明stale或disabled，或API读取失败
- **THEN** 分别展示Unknown、Disabled、Unavailable；前两者不创建新故障且不能把旧ACTIVE标为已恢复

#### Scenario: Read persisted occurrences
- **WHEN** 管理员在现有详情查看availability ACTIVE/RESOLVED列表
- **THEN** 采用认证有界keyset读取、状态明确、原因安全枚举；当前Inventory不再存在时不能绕过History membership gate

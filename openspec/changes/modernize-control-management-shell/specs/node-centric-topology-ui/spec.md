## MODIFIED Requirements

### Requirement: Topology SHALL 保护身份并安全恢复读取

页面及新history读取SHALL沿用实名super_admin会话、no-store与request ID。canonical account_key按最新duplicate规格仅供管理员文本展示，MUST NOT进入浏览器导航URL/storage/logs/metrics；仅Account Request History GET的account_key参数及内部cursor允许在认证HTTP请求URL中传递账号身份；不得泄露凭据或raw response。各区SHALL独立loading/empty/unavailable/retry，401清理全部会话数据，切换Node/筛选丢弃迟到响应；时间字段的 instant 来源和传输契约保持不变，用户可见时间 MUST 按浏览器系统时区以 `YYYY-MM-DD HH:mm:ss` 展示。重启只重读数据库。

Phase 10 MAY 将该 capability 的主要 presentation owner 从名为 Topology 的页面重组为 `/monitoring`，并在迁移期保留 `/topology` 兼容 alias；Inventory evidence、Binding truth/resolution、Duplicate Ownership、Provider state、Account Quality 等既有 read-only / identity / error-isolation semantics MUST 保持。该 presentation migration MUST NOT 增加 mutation responsibility。

Phase 10 supersede 本 requirement 的历史 `390px` Mobile presentation acceptance。新的强制布局验收以 PC Desktop Browser `1280×720` 与 `1440×900` 为准；keyboard/accessibility contract 继续保留。

#### Scenario: 局部失败与乱序
- **WHEN** B已选中而A请求迟到，或History失败但Binding成功
- **THEN** A结果不覆盖B；History显示unavailable且不清除独立成功Binding，更不发mutation

#### Scenario: 会话与 Desktop / keyboard
- **WHEN** 会话失效，或在 `1280×720`、`1440×900`、keyboard 模式查看
- **THEN** 失效清理会话和内存；有权限时各区和两个Provider badge均可辨识，可操作控件有可访问名称且状态不只靠颜色，时间按系统时区以固定格式显示

#### Scenario: historical Mobile requirement is no longer a release gate
- **WHEN** Phase 10 acceptance inventory 发现历史 `390×844` Topology Browser case
- **THEN** 该 case SHALL 被标记为 superseded / non-blocking 或从 Phase 10 required suite 移除
- **AND** MUST NOT 为维持该历史 Mobile layout contract 牺牲新的 PC table / shell information density

#### Scenario: topology compatibility alias
- **WHEN** 迁移期用户访问既有 `/topology` deep link
- **THEN** SHALL 到达继续满足本 capability 既有只读诊断语义的 Monitoring presentation
- **AND** MUST NOT 因 route rename 丢失 Inventory / Binding / Duplicate / Provider / Account Quality 的既有语义

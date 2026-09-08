## MODIFIED Requirements

### Requirement: Topology SHALL 保护身份并安全恢复读取

页面及新history读取SHALL沿用实名super_admin会话、no-store与request ID。canonical account_key按最新duplicate规格仅供管理员文本展示，MUST NOT进入浏览器导航URL/storage/logs/metrics；仅Account Request History GET的account_key参数及内部cursor允许在认证HTTP请求URL中传递账号身份；不得泄露凭据或raw response。各区SHALL独立loading/empty/unavailable/retry，401清理全部会话数据，切换Node/筛选丢弃迟到响应；时间字段的 instant 来源和传输契约保持不变，用户可见时间 MUST 按浏览器系统时区以 `YYYY-MM-DD HH:mm:ss` 展示。重启只重读数据库。

#### Scenario: 局部失败与乱序

- **WHEN** B已选中而A请求迟到，或History失败但Binding成功
- **THEN** A结果不覆盖B；History显示unavailable且不清除独立成功Binding，更不发mutation

#### Scenario: 会话与窄屏

- **WHEN** 会话失效，或在390px/桌面/键盘模式查看
- **THEN** 失效清理会话和内存；有权限时各区和两个Provider badge均可辨识，可操作控件有可访问名称且状态不只靠颜色，时间按系统时区以固定格式显示

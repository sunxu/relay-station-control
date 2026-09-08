# ui-local-time-display Specification

## Purpose
定义 Control Web 将既有时间 instant 以浏览器系统时区和固定人类可读格式展示的只读契约。

## Requirements

### Requirement: Web SHALL use system timezone for displayed timestamps

Control Web 对管理员可见的既有时间字段 MUST 使用浏览器运行环境的系统时区格式化，并 MUST 输出 `YYYY-MM-DD HH:mm:ss`（24 小时制、零填充）格式。显示结果 MUST NOT 固定为 UTC、固定为某个业务时区或追加时区后缀。该规则适用于登录/会话、资产、任务、Topology、Inventory、Provider、Account Quality、Request History、Incidents、详情和容量等现有只读展示。

#### Scenario: 同一 instant 按系统时区显示

- **WHEN** API 返回一个有效 ISO instant，浏览器系统时区发生变化
- **THEN** 页面以对应系统本地时钟显示该 instant，文本格式仍严格为 `YYYY-MM-DD HH:mm:ss`

#### Scenario: 示例格式

- **WHEN** 本地时间为 2026 年 9 月 8 日 18 时 40 分 40 秒
- **THEN** 页面显示 `2026-09-08 18:40:40`

#### Scenario: 缺失或非法时间

- **WHEN** 时间字段为 null、空值或无法解析
- **THEN** 页面显示 `—`，不显示当前时间、UTC 文本或错误的 ISO 字符串

### Requirement: Transport and calculations SHALL retain existing time semantics

HTTP/API、PostgreSQL、审计、排序、cursor、窗口边界、freshness 和生命周期计算 MUST 继续使用既有 UTC/ISO instant 契约。系统时区格式化 MUST 仅发生在用户可见展示层，显示字符串 MUST NOT 写入 API 请求、数据库、URL、浏览器 storage、日志或指标。

#### Scenario: API and database instant unchanged

- **WHEN** 页面读取或提交包含时间的既有请求
- **THEN** 请求字段、数据库值、排序和窗口计算保持原有 UTC/ISO 表示，只有展示文本使用系统时区

#### Scenario: Display failure does not alter read state

- **WHEN** 时间显示值缺失或解析失败
- **THEN** 其它读取结果和状态保持不变，仅该时间位置显示 `—`

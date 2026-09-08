## Why

Control Web 当前多个只读页面将时间固定格式化为 UTC，管理员在本地环境查看事件、账号、Provider 和任务时需要额外换算。本 change 统一用户可见时间的显示方式：使用浏览器系统时区，并固定为 `YYYY-MM-DD HH:mm:ss`。

## What Changes

- 为 Control Web 增加统一的本地时间显示契约，覆盖登录/会话、资产、任务、Topology、账号清单、Provider、Account Quality、Request History、Incidents 和详情/容量等现有时间展示。
- 使用浏览器运行环境的系统时区，不固定 UTC、Asia/Shanghai 或其它业务时区；显示格式固定为四位年、两位月日、两位时分秒。
- 保留 API、数据库和筛选计算的 UTC/ISO 时间语义；仅改变展示层格式化。

## Capabilities

### New Capabilities

- `ui-local-time-display`：Control Web 用户可见时间的系统时区和固定格式。

### Modified Capabilities

- `node-centric-topology-ui`：Topology 的来源时间改为按浏览器系统时区展示，保留只读、状态和数据来源语义。

## Impact

受影响仓库仅为 `relay-station-control`，主要涉及 Web 共享格式化函数、现有组件和前端测试。OpenAPI、Go API、PostgreSQL schema、migration、generated client、collector、CLIProxy、metrics 和审计时间存储不变。无需 migration；回滚 Web bundle 即恢复此前显示方式。显示值不写回 API、数据库、URL、storage、日志或指标。设计沿用 System Design v1.8 / R4.7 以及 ADR-0001、ADR-0002 的 Control 只读观察边界；本 change 不改变这些边界。

## Compatibility Boundary

- API 与数据库继续以 UTC instant/既有 ISO 时间传输和保存，服务端窗口、排序、cursor 和 freshness 计算不变。
- 浏览器系统时区只影响人类可读文本；相同 instant 在不同时区显示不同本地时钟，这是预期行为。
- 无效或缺失时间继续显示 `—`，不得伪造当前时间。

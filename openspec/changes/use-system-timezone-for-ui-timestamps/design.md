## Context

现有 Web 组件分别使用 `toISOString()` 或 `Intl.DateTimeFormat` 固定 `timeZone: UTC`，并在部分详情字段直接渲染 API 字符串。需求是统一显示为示例 `2026-09-08 18:40:40`，且按浏览器系统配置的时区显示。

## Decisions

- 新增一个纯展示层、无副作用的时间格式化 helper，解析 API 返回的 instant，使用 `Date` 的本地字段或不指定 `timeZone` 的 `Intl` 格式化；不得固定业务时区或追加 `UTC` 后缀。
- 输出严格为 `YYYY-MM-DD HH:mm:ss`，使用零填充和 24 小时制。缺失、非法或不可解析值输出 `—`。
- 统一替换现有页面/组件中的用户可见时间文本，包括登录/会话、资产、任务、Topology、账号清单、Provider、质量、History、Incidents、详情和容量；时间输入控件若存在仍遵循其既有输入契约。
- `toISOString()` 仅可继续用于 HTTP/数据库传输、cursor 或计算，不得用于用户可见显示。API response、DB UTC、排序、窗口边界、审计和 freshness 逻辑不变。
- 测试通过设置运行时 `TZ` 或等价系统时区构造同一 instant，验证显示随系统时区变化且格式稳定；不依赖开发机固定时区。

## Invariants and Boundaries

- Control 仍只读观察；不增加 API、migration、持久化、数据面或 mutation。
- 现有认证、错误、loading/empty/unavailable 和 request cancellation 行为不变。
- 显示时间不进入 URL、storage、日志或指标；account identity 与时间传输边界不改变。

## Rollout and Rollback

先更新共享 formatter 及所有现有调用点，再运行受影响前端测试、typecheck 和 build。回滚只需恢复 Web bundle/commit；不会回滚或修改数据库及 API 数据。

## Evidence

规划阶段以 `rg` 清点固定 UTC 和直接渲染时间的调用点；实施阶段记录受影响文件、系统时区示例、缺失/非法值断言和前端验证命令。该 change 不要求完整 PostgreSQL 或数据面测试。

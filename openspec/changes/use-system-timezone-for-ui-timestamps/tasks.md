## 1. Contract and inventory

- [x] 1.1 清点 Control Web 所有用户可见时间字段和固定 UTC/直接字符串渲染点，确认 API/DB transport 用途不被替换
- [x] 1.2 创建共享系统时区 formatter，冻结 `YYYY-MM-DD HH:mm:ss`、24 小时制、缺失/非法值行为

## 2. Implementation

- [x] 2.1 更新登录/会话、资产、任务和其它管理页面的时间展示
- [x] 2.2 更新 Topology、Inventory、Provider、Account Quality、Request History、Incidents、详情与容量展示
- [x] 2.3 保持 API、数据库、排序、窗口计算、审计和传输时间契约不变

## 3. Verification

- [x] 3.1 增加同一 instant 在系统时区下的格式和跨时区断言，覆盖缺失/非法值
- [x] 3.2 运行受影响前端测试、typecheck 和 build，确认无用户可见 UTC 后缀或直接 ISO 文本残留
- [x] 3.3 运行 `make test build`，确认共享 formatter 与既有 Control 回归通过
- [x] 3.4 运行 `openspec validate use-system-timezone-for-ui-timestamps --type change --strict --no-interactive`、`openspec validate --all --strict` 与 `git diff --check`

## 4. Evidence and handoff

- [x] 4.1 更新 planning-validation.md，记录实现文件、测试命令和结果
- [x] 4.2 完成 scope/self-review、检查 git diff/status，并等待 Architecture + Implementation Final Review

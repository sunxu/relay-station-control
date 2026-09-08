# 验证证据

- 实现：`web/src/pages/AccountInventoryView.tsx`，首次挂载可取消 microtask 执行默认第一页；无初始 Node 时 execute 返回，后续筛选不自动提交。
- 测试：`web/src/pages/AccountInventoryView.test.tsx`，StrictMode 只请求一次并显示账号；修改筛选不自动请求；无 URL 不请求；失败显示错误并可手动恢复。
- `make test build`：PASS，16 个前端文件 / 72 个测试及 Go 检查、前端类型检查和构建通过。日志 `/private/tmp/inventory-deep-link-check.log`。Go 模块缓存写入有沙箱 warning，命令退出 0；部署镜像另行构建。
- 随后补充两个负向/恢复测试，`npm test -- --run src/pages/AccountInventoryView.test.tsx`：11/11 PASS，日志 `/private/tmp/inventory-deep-link-focused.log`。
- 不修改 HTTP API、生成代码、数据库或 migration。容量诊断本身仍不触发账号查询；深链接初始查询是独立的用户导航行为。
- OpenSpec change strict 和 git diff --check：PASS。提交前工作树仅本 change 文档及两个 Web 文件。

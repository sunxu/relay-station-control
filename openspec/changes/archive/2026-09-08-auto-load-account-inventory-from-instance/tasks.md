## 1. 现状与页面初始化

- [x] 1.1 核对账号清单页现有 URL 解析、Node 加载、query state 与服务端校验职责，记录实现入口与无 URL 的兼容行为
- [x] 1.2 实现合法 `instance_id` 的一次性初始预选与默认第一页查询，并保证无 URL 时仍由管理员手动选择后查询

## 2. 交互与失败状态

- [x] 2.1 保证筛选编辑、Node 切换和分页继续由显式动作触发并重置 cursor，不被初始化逻辑覆盖
- [x] 2.2 验证无 URL 不查询、失败手动恢复、StrictMode 防重复及筛选编辑仍需显式查询；复用既有服务端错误映射

## 3. 验证与证据

- [x] 3.1 运行账号清单 Web/前端专项测试，断言带 URL 首次只发一次默认查询且后续交互不隐式查询
- [x] 3.2 检查不涉及 API、数据库、migration、生成文件或数据面调用，并运行 `openspec validate auto-load-account-inventory-from-instance --type change --strict --no-interactive` 与 `git diff --check`
- [x] 3.3 记录实现、测试命令、结果和工作树状态，完成文档与实现证据对账

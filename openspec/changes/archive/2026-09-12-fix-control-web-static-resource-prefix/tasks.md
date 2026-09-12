## Tasks

- [x] 1. 按已冻结设计确认 `/static/` 与 `/assets`、API route 的 collision scan。
- [x] 2. 更新 frontend build configuration 为 `base: "/static/"`。
- [x] 3. 更新 embedded server routing：strip `/static/`、命中文件服务、miss 返回 404；保持 `/assets` 和 `/assets/` 为 Asset Registry SPA。
- [x] 3a. 更新 `web/src/auth/AuthContext.tsx` 的 pathname matcher/normalization，使
  `/assets` 与 `/assets/` 都解析为 `assets` 路由（例如等价于
  `pathname === "/assets" || pathname === "/assets/"` 的判断，或等价的
  trailing-slash normalization），不得引入新 router framework、全站 URL rewrite
  subsystem 或新的 route 抽象层。
- [x] 4. 增加 static miss、SPA route、API route 和 lazy-chunk regression tests。
- [x] 4a. 增加针对 `AuthContext.tsx` pathname matcher 的 unit/integration test，验证
  `/assets` 与 `/assets/` 均解析为 `assets` 路由。
- [x] 4b. 增加 E2E 场景：authenticated 直接导航 `/assets`、authenticated 直接导航
  `/assets/`、authenticated 用户停留在 `/assets/` 时浏览器 reload；三者均断言最终
  渲染的是 Asset Registry 页面（使用稳定 UI signal，例如 Asset Registry page
  heading 或专用 asset-page test id），且不得渲染 management/default 页面；断言不
  得只停留在 HTTP 200 / document loaded / SPA root 存在层面。
- [x] 5. 执行 production build 并验证 lazy chunks 成功加载。
- [x] 6. 更新 Web routing 文档与 Phase 6 prerequisite evidence。
- [x] 7. 记录测试、回滚说明和 clean-worktree evidence。

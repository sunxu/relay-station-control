## Context

本 change 已根据当前 Control 的 Vite 配置（未配置 `base`）、`internal/webui/embed.go`（当前对 dist 文件命中并将未知路径 fallback 到 `index.html`）和 router 顺序（API 先注册，NotFound 再进入 Web handler）选择 `/static/` 作为 reserved public static namespace。`/assets` 是产品路由，构建静态资源不得进入该 namespace。

## Decision

冻结以下实现设计与 observable contract：

- `GET /assets` 与 `GET /assets/` 返回 Asset Registry SPA shell。
- SPA lazy chunks 使用与 `/assets` 不冲突的构建资源 URL，并可成功加载。
- API routes 保持原有解析和响应行为。
- 不存在的静态资源返回稳定的 static-miss 结果，不 fallback 成 Asset Registry SPA。
- 非 `/assets` 的既有 SPA/API route 行为保持兼容。

具体映射为：Vite `base: "/static/"`；构建产物仍写入 `internal/webui/dist`；embedded handler 识别 `/static/`，去除该 public prefix 后仅在 embedded dist 中查找对应文件。文件存在时返回静态文件；文件缺失时返回 HTTP 404，绝不 fallback 到 `index.html`。`/assets` 与 `/assets/` 直接进入 SPA shell；其它已批准的 SPA deep link 仍 fallback 到 shell；API route 由 API router 优先处理，不能被 SPA 或 static handler 接管。

`/static/` 下的请求不得被解释为 product route；`/assets/...` 继续保留给 Asset Registry route 的应用路由语义。实现不得把静态资源路径注册为 `/assets/...` 产品资源的同义路由。

前端 in-browser route resolver（当前实现位于 `web/src/auth/AuthContext.tsx`）只精确匹配
`window.location.pathname === "/assets"`，`/assets/` 会落入 management/default 分支。
HTTP 层正确返回 `/assets/` 的 SPA shell 不等于前端最终渲染 Asset Registry——必须同时冻结
前端 pathname matcher 的等价识别契约：`/assets` 与 `/assets/` MUST 都解析为 `assets` 路由。
实现阶段允许的最小方案是等价于
`pathname === "/assets" || pathname === "/assets/"` 的判断，或等价的 trailing-slash
normalization；不得引入新 router framework、全站 URL rewrite subsystem 或新的 route
抽象层。本 change 目前只冻结该 planning 契约，不在本轮修改 `AuthContext.tsx`；对应
implementation task 见 tasks.md。

## Verification

使用生产构建启动 Control，验证 `/assets`、`/assets/`、lazy chunks、API regression 和 static miss；记录浏览器或等价 HTTP evidence。E2E 断言 MUST 证明最终渲染的是 Asset Registry 页面本身（例如 Asset Registry page heading 或专用 asset-page test id 等稳定 UI signal），不得只断言 HTTP 200、document loaded 或 SPA root 存在。验证范围至少包含：authenticated 直接导航 `/assets`、authenticated 直接导航 `/assets/`、以及 authenticated 用户停留在 `/assets/` 时浏览器 reload 这三种路径，且三者都必须最终渲染 Asset Registry 而非 management/default 页面。失败时只回滚本 change 的 build/server routing，不触碰资产数据。

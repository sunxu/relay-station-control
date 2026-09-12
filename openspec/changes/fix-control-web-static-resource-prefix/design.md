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

## Verification

使用生产构建启动 Control，验证 `/assets`、`/assets/`、lazy chunks、API regression 和 static miss；记录浏览器或等价 HTTP evidence。失败时只回滚本 change 的 build/server routing，不触碰资产数据。

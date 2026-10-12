## Context

见 proposal.md。现有 UploadNewAccountAction 手填邮箱并传一个 File，API 使用 multipart request/credential；service.validateCredential 在接受事务前检查身份、大小及 denylist。新增转换不需要 batch endpoint，Node 能力按用户确认作为前提。后端共享校验必须知道操作种类，否则放开 disabled 会意外放开 Replace Existing。

## Goals / Non-Goals

Goals：从真实 JSON 身份生成单账号请求，保留代理和新账号状态，逐账号真实结果，临时 Secret 生命周期与竞态隔离。

Non-Goals：多文件选择、通用 Provider importer、Node/Gateway 配置管理、OAuth refresh、服务端队列/批量事务、自动 retry/replace、运行配置迁移、DB/OpenAPI 结构变更、自动部署。

## Decisions

1. **纯转换模块与既有 UI/API 组合**：新增 frontend 纯解析模块；解析返回有界 prepared accounts 和安全 preview。拒绝新增后端导入服务或透传整个 Sub2API 文件。输入文件沿用 1 MiB，批量上限 100 为本方案保守 UI 边界；单账号生成文件仍须 <=1 MiB。
2. **字段映射**：单账号复制原生对象并规范化 email、补 disabled=false；先检测禁止键，不静默删键。批量用白名单构造 type=antigravity、email、refresh_token、可选 access_token/project_id/expired、disabled 和 proxy_url。expires_at 接受 Sub2API 原生 Unix 秒数字字符串（Gateway antigravity_oauth_service.go 已确认）或显式 timezone 的 ISO 时间，转为 RFC3339/ISO 同一 instant，不计算 Date.now()+expires_in，不导入 _token_version、model_mapping、custom_error_codes、extra、并发或优先级。project_id 优先；仅在其缺失、null 或空字符串时回退 antigravity_project_id，两个字符串不同不拒绝；可选值为 null 视为缺失，必要字段不可为空。批量 disabled 只读取 account.disabled，不猜测 status 或其它字段。
3. **代理映射**：代理记录按 proxy_key 建立唯一索引，有引用必须精确匹配；密码/用户名分别编码，支持 IPv6 host 括号，端口为 1..65535 的整数。明确配置的非法代理不得被省略。null/缺失 proxy_key 表示未配置；不暴露带认证 URL 的 parse error。后端独立重复验证 URL，canonical/alias 双出现拒绝；对 legacy 单 alias 不改写字节，交由已有 Node schema 处理。
4. **受控逐账号执行**：按文件顺序串行，独立 UUID 和 account_key，busyRef 防二次提交，固定目标 Node。每个结果持有安全身份、command ID、既有 projection 或结构化错误。非 401 失败继续，401 退出；不把 202 outcome_unknown 当成功。结果在文件清理后仍可读，不保留 credential。API 不支持 AbortSignal 时，不声称可撤回已发出的 mutation；卸载/失效只阻止下一项及迟到 UI 更新。中途切换 Node 清理上传上下文，不能把余下账号提交到新 Node。
5. **解析竞态**：使用 generation guard 隔离异步 File.text；换文件、取消、Node 变化、401、卸载使旧 generation 失效。敏感引用及时释放，不承诺 JS 内存物理擦除。
6. **后端窄修订**：沿用 canonical alias 与剩余 denylist，proxy_url 独立校验；disabled 依 operation kind 限制 upload_new。不改 HMAC、intent 编码、receipt、audit、serialization、Create collision 或 accepted race。验证发生于既有接受事务之前；不修改 credential bytes。无新增数据库事务、迁移、metrics 或原始错误暴露。
7. **兼容与验证**：现有手填邮箱 Browser fixture 迁至 JSON-derived preview，其它 Account Operations 不改。先跑纯解析/后端校验 tests，再 component 与真实 upload owning acceptance，最后 make test build；formal runtime 绑定 clean candidate，不得直接使用真实根目录样例或因无 acceptance env 弱化证明。

## Risks / Trade-offs

- [文件中非法条目] → 全文件预检失败，不出现解析阶段部分上传；partial success 只指已校验条目的独立服务端执行。
- [代理参数丢失/Secret 泄漏] → 精确关联、严格格式、编码测试、安全 preview 和 synthetic canary。
- [放开已有状态修改] → 操作种类传给校验，Replace disabled negative proof。
- [历史单账号 API] → disabled 缺失保持兼容，不在服务端补值，不改变 replay fingerprint。
- [上下文变化与 late completion] → generation/target guard，停止未开始项，不自动 replay。
- [回滚 UI/服务端不同步] → 同一 Control artifact 发布；回滚旧 artifact 后新代理上传将重新被拒绝，已有 Node auth file 不被自动改写。

## Migration Plan

无 schema migration。先完成与批准 coverage review，test-first 实施，形成 clean candidate 后执行既有 upload full-chain 与 Browser。记录精确 evidence 后再关闭 change；不自动 tag/push/deploy，不改历史 Phase 10 acceptance。

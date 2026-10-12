## Why

Accounts 的 Upload New Account 当前要求手填邮箱，只接收一个原生凭据文件，容易出现身份与文件不一致，不能直接导入 Sub2API 账号导出文件。此次后续能力变更让管理员从文件内容获得身份，并保留账号代理配置，逐账号获得真实执行结果。

## What Changes

- 删除可编辑邮箱输入；从 JSON 提取、规范化邮箱，以只读身份预览确认。
- 支持 Antigravity 原生单账号 JSON 与 Sub2API Antigravity OAuth 批量导出 JSON。
- 通过 proxy_key 关联并转换账号代理；不静默丢失代理或降级直连。
- 在浏览器内有界转换，逐账号调用既有 upload_new，独立 command_id，支持部分成功，不自动重试或替换。
- 保持凭据临时生命周期、401 边界与既有操作状态语义。
- 窄范围修订 Control 上传校验：允许经过校验的账号级 `proxy_url` 与布尔类型的初始 `disabled`，不放开其它运行控制字段；原生调度和代理执行仍由 Node 负责。
- 2026-10-12 只读密钥兼容 corrective：账号操作指纹密钥读取允许恰好 0400/0600，不要求生产密钥可写；保持其它安全校验、密钥字节、HMAC/replay 与部署只读挂载不变，执行状态见任务 7 与追加证据。

## Capabilities

### New Capabilities
- `account-json-import`: Accounts 上传入口双格式解析、身份派生、代理映射与逐账号结果。

### Modified Capabilities
- `account-admin-operation`: 从上传 denylist 中窄范围分离账号级代理与新账号初始禁用状态，规定明确的代理格式和布尔值校验、安全处理及兼容性；既有 Replace Existing 不借此放开禁用状态修改。单账号执行、receipt、audit、replay 与原生 API 子集不变。其 Phase 7 不增加 batch/all mutation 的约束继续用于服务端命令；新能力仅组合多次独立单账号命令。

## Impact

影响 Control 前端上传呈现、纯 JSON 转换、后端上传校验及对应测试。复用 account-operations-api 和既有 multipart API，不新增 batch endpoint；OpenAPI request/response schema、数据库、Migration、sqlc 和生成客户端无需改变。代理字节继续纳入现有 credential keyed fingerprint，不进入 receipt、audit、metrics 或日志。不修改 Gateway/Node 或新增数据面调度。遵守系统设计当前职责边界以及 ADR-0001 安全契约和 ADR-0002 原生调度职责。真实样例只供本地结构调研，不进入测试或提交。回滚恢复之前的上传校验与 UI，不需要 schema 回滚；已有 Node 账号不自动改写。

## Readiness

双格式导入已完成实现及首次验收，历史结果见 validation.md。2026-10-12 用户进一步授权：批量项目 ID 优先使用 project_id，不再因 antigravity_project_id 字符串与其不同而拒绝；仅在 project_id 缺失、null 或空字符串时回退。本轮执行状态以任务 6.1 与追加证据为准。批量上限 100、输入 1 MiB 保持原已批准边界。

## Frozen Boundary

- Node 支持账号代理作为用户确认的前提，不另行调研该能力。
- 单账号从顶层 email，批量从 credentials.email 派生身份；不从文件名、账号 name 猜测身份。
- 批量优先使用 credentials.project_id；其缺失、null 或空字符串时才使用 credentials.antigravity_project_id。两字段字符串不同仍以 project_id 为准，其余类型校验保持不变。
- 单账号代理来自 proxy_url；批量按 proxy_key 关联 proxies，保留协议、地址、端口及可选认证信息；无效引用不能静默丢弃或直连。
- 单账号 JSON 中的 disabled 必须是布尔值并原样保留；缺失时生成 disabled=false，非布尔值明确拒绝。Sub2API 批量没有明确禁用状态时生成 disabled=false，不从 auto_pause_on_expired 或其它调度字段推导；有明确 disabled 时同样只接受布尔值。
- 预览展示实际将导入的启用/禁用状态。disabled 仅用于 Upload New 的新账号初始状态，不能通过 Replace Existing 修改已有账号；已有账号状态继续由 Enable/Disable 命令管理。
- 仅放开账号代理与上述初始 disabled，不放开 weight、priority、重试、模型映射等既有禁止字段。原生单账号中出现其它禁止字段继续明确拒绝；Sub2API 外层调度字段不映射成 Node 运行配置，预览须说明未导入的配置类别。
- 每个账号独立命令与结果，允许部分成功；不自动 retry、replace、repair 或把 outcome_unknown 显示成成功。401 停止余下提交并清理敏感临时数据。
- 密钥、代理密码、原始 JSON 及带认证的代理 URL 不进入普通预览、持久存储、URL/history、日志或审计；可展示邮箱及代理是否配置。

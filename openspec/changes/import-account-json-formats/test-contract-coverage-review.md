# Test Contract Coverage Review

规划覆盖评审：PASS；最终执行与实际计数见 `validation.md`。
这是主 Agent 的覆盖审阅，不是独立子代理报告。用户已批准规划；unit/component、真实 PostgreSQL HTTP、Accounts Browser 与真实 Node upload owning acceptance 已执行。

| Contract | Risk | Owning Layer / Concrete Proof | Negative / Race | Browser |
|---|---|---|---|---|
| 双格式、无邮箱输入、JSON-derived identity | 错账号 | 新纯 parser unit：相反文件名仍按 email；AccountOperationsPanel component：输入不存在、preview/account_key 一致 | null/array/非法 JSON、unsupported platform/type、空邮箱、unsafe basename、238/239 bytes、大小 1 MiB/1 MiB+1、100/101 accounts、规范化重复；零 mutate | 单账号和批量真实路径 |
| 凭据映射、expiry、project ID | 丢凭据/伪造时间 | parser unit 精确比较生成对象；expires_at 转 ISO 同 instant，原生未知 provider 字段保留 | 两 ID 不同仍优先 project_id、主 ID 缺失时回退、refresh 缺失、可选类型错误、null/缺失、非法时间；不生成当前时间 | 代表生成 payload assertion |
| 代理关联与编码 | 默认直连/Secret 泄漏 | parser unit：http/https/socks5/socks5h、IPv4/domain/IPv6、特殊字符 user/password roundtrip | missing/duplicate reference、空 key、端口 0/65536、错误类型、host 注入；无引用不生成代理 | 用 synthetic proxy canary，preview 只显示 configured |
| disabled 默认及不推导 | 状态误写 | parser unit：true/false/缺失，auto_pause_on_expired=true 仍 false；component 状态预览 | null/string/number 拒绝；server service table 按 upload_new/replace 区分 | preview 与 payload |
| 后端 proxy/alias/denylist | 越权扩大配置 | internal/accountadmin/service_test.go 表驱动；覆盖两个 alias 单独合法、共同出现失败、所有剩余 canonical/alias 禁止键 | proxy null/object/空、unsupported protocol、query/fragment/path/control/invalid port；Replace disabled true/false 都拒绝 | 不重复完整矩阵 |
| 接受前错误无持久化/远程动作 | 错误已执行 | internal/api/account_operations_http_integration_test.go：真实 HTTP/PostgreSQL 检查 registry/operation/receipt 数与 Node call count=0 | identity mismatch、非法 proxy/disabled、401/403/CSRF 继续复用既有 proof 并回归 | 代表错误呈现 |
| 原始字节 HMAC/replay | 错误重放 | service canonical intent tests + 真实 store/API replay：同字节精确重放；仅 proxy/password/disabled 改变则 conflict，native count 不增长 | 旧 missing-disabled payload 兼容；wrong-key/conflict ordering 沿用既有 owning tests | 只检查 UI 零 retry |
| 独立顺序提交/部分成功 | 伪造 all-success | component fake API 返回 applied/failed/unknown；断言每项 unique command 与 file-order、失败继续、unknown 不重试 | double click、network error、403/409/503 不 expire；结果携带安全命令身份与未提交状态 | 代表 full-chain 双格式及 partial-success UI |
| 401 和目标上下文 | 跨 Node/旧 session 提交 | component delayed Promise：A first in flight，换 Node/卸载/401，旧请求可完成但 B 不发送、不恢复 UI；Accounts page cache/session tests | 401 stops vs 非401 continues，cancel before submission、late response after exit | 已有 session owning path + focused stop proof |
| File A→B 与清理 | 迟到凭据覆盖 | controllable FileReader：B resolves then A；只有 B 可提交；完成/401/卸载清理 input 与 prepared refs | A→B、Node change during submission、401 stops remainder | 无需重复竞态 matrix |
| Secret boundaries | 凭据持久泄漏 | component synthetic token/proxy canaries 不在 textContent、错误、projection；API integration persistence scanner 检查 HMAC-only durable state | raw JSON parse error 不包含输入，认证 URL parse error 不回显 | URL/history/storage/console/text + ALL fetch/xhr origin capture |
| 既有安全/数据面边界 | 绕过 CSRF/原生调度 | 既有 HTTP auth/CSRF、Node adapter allowlist、receipt/audit/serial tests 回归；git scope 无 DB/API/Node/Gateway production diff | 零 proxy probe、零 replace/repair endpoint、无 provider refresh | same-origin X-CSRF-Token 与精确 route allowlist |
| i18n/a11y/locator | UI 不可用 | resources parity/key/source tests；component 可访问状态/按钮标签、文字状态 | zh-CN/en locale、无 Secret aria-label | 1280 desktop zh/en；getByTestId 交互，无 DOM-order locator |

## State-space review

- 只读 intent key corrective owning proof：Store tests 表驱动覆盖 0400/0600 与其它权限、空路径、missing、directory、symlink、0/31/33 bytes；跨权限 HMAC 完全一致且不 chmod/write。真实 PostgreSQL HTTP 与 production wiring 使用 0400 fixture，保持非法 admission 零 durable/native mutation 和已有 replay/conflict/CSRF proof。无新增 crypto/provisioning 或 Secret 日志输出。

- 校验阶段：合法/非法单文件；有/无 proxy；disabled 缺失/true/false/非法；upload_new/replace_existing 独立覆盖，不能只测一个 happy path。
- 提交阶段：applied/noop/failed/outcome_unknown/HTTP error/401；尚未提交与已 dispatch 分开，不声称可取消远程 mutation。
- 竞态：A 先完成、B 先完成、cancel/Node-change/unmount 先于读取或提交完成；不得让另一目标继承旧 batch。
- 既有后端同账号序列化、restart、replay 与 receipt/audit 继续以既有 owning integration 为证据，不新建前端持久队列。

## Reconciliation

所有新增规范 requirement 已指定最低 owning layer 与具体 setup/result。无需 Migration 或生成客户端变更；正式 acceptance 必须冻结 clean candidate 与 artifact identity，执行缺失时不能填写 CLOSED/PASS。没有剩余无 owner 的规范要求；保守输入边界须由规划审阅确认。

# Control 账号清单只读查询 Runbook

## 1. 适用范围与安全不变量

本手册适用于 `add-control-account-inventory-readonly-query`。该功能只投影 PostgreSQL 中已经提交的当前账号清单，不采集、不补采、不修改账号生命周期，也不从历史 snapshot 重建状态。页面和 `POST /api/account-inventory/query` 只能访问 Control 与 PostgreSQL；它们不得调用 Relay Node、Gateway、Prometheus、模型数据面或任意外部目标。

完整 email 和内部 `account_key` 是敏感身份。email 只允许进入 HTTPS JSON body、受保护 SQL 参数/列、已授权响应和短命浏览器内存；`account_key` 只允许进入受保护数据库列、服务端 DTO 和 AEAD cursor 明文。请求 URL、redirect/`Location`、普通日志、指标、审计 details、浏览器 history、`localStorage`、`sessionStorage`、测试输出和保留证据都不得包含 email、account key、cursor、filter value/hash 或响应正文。所有成功和错误响应必须带 `Cache-Control: no-store`。

本功能没有导出、复制、详情、批量选择、编辑、删除、补采或 promotion 控件。排障时不得用临时 SQL、临时 HTTP route 或扩大数据库权限绕过这些边界。

## 2. 权限与实例范围

一次查询必须同时满足：

- 操作者是已认证、已启用的实名 `super_admin`；每个 POST 都重新验证 session 和 session-bound CSRF。
- `instance_id` 属于当前环境中已登记的单个 Relay Node。
- 该 Node 当前登记的 Driver contract 声明 `management_account_inventory_read` capability。
- 请求 body 小于固定上限，`limit` 为 `1..100`（默认 `50`），Provider、lifecycle、最后报告 basic status 和 email 都是封闭的精确筛选。
- Control 使用 runtime role 通过 `control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)` 有界读取，并在同一短事务提交 `account_inventory.view` 审计。

查询不支持跨 Node 聚合。页面 Node 选择器只展示具备 capability 的 Node；切换 Node 或任一 filter 后必须清空 cursor 历史并回到第一页。runtime role 只能执行批准的查询函数和既有审计写入边界，不能获得 `account_inventory`、Provider state、snapshot 或资产表的任意通用 SELECT/DML。遇到权限错误时修复 Migration、owner 或 grant 漂移，禁止向 runtime role 临时 `GRANT SELECT`、表 DML 或超级用户权限。

上线前可由受控 migrator/metadata 检查确认对象存在；只保留布尔结果，不输出函数参数、表行或身份值：

```sql
SELECT to_regprocedure(
  'public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)'
) IS NOT NULL AS query_function_present;

SELECT to_regclass('public.account_inventory_normalized_email_read_idx') IS NOT NULL
   AND to_regclass('public.account_inventory_basic_status_read_idx') IS NOT NULL
   AS query_indexes_present;
```

新二进制启动时还会检查函数、专用索引、runtime EXECUTE 和禁止直接表 SELECT 的组合；任一条件不成立都应在开放产品查询前 fail closed。

## 3. 页面状态的正确解释

- `basic_status` 是 Relay Node **最后一次报告**的基础状态，不等同于当前可调度性。`suspected_missing` 或 `missing` 行仍可能保留旧 basic status。
- `lifecycle` 是当前 `present|suspected_missing|missing|out_of_scope` 状态；查询和翻页不会推进 missing count。
- `provider_degraded=true` 表示最近 Provider 观测不完整；它可以与仍在 15 分钟窗口内的 `snapshot_freshness=fresh` 同时出现。
- active Provider 的 `provider_last_complete_at` 距数据库当前时间超过 15 分钟时为 `stale`；不要用浏览器时间或账号 `last_seen_at` 代替。
- `out_of_scope` 行的 freshness 固定为 `out_of_scope`。重新加入 Provider 后，只有下一次合格 promotion 中实际出现的账号才恢复 `present`。
- 缺少匹配 Provider current state、last-complete time 或出现非法 lifecycle/source 组合时，整页返回固定 `503`，不能用默认时间、空 Provider state 或部分结果继续展示。

## 4. Cursor 过期、重放与 key rotation

账号 cursor 是随机 nonce 的 AEAD ciphertext，使用 auth keyring 的独立 domain，并绑定环境、格式/key version、actor admin、instance、规范化 filter hash、after account key、签发时间和严格 15 分钟过期时间。cursor 不是授权凭据；翻页仍必须使用当前有效 session、enabled admin 和 CSRF。

以下任一情况都统一得到 `400 validation_failed`，服务不会说明具体原因：token 被篡改或截断、超过 1536 字符、格式/version/key未知、认证 tag错误、已满 15 分钟、旧 key 已移除、actor/instance/filter不匹配或跨环境重放。处置方式是丢弃页面内存中的整个 cursor 栈，从第一页重新查询；不得尝试解码、修补、延长或把 cursor 写入 URL/工单/日志。

keyring 轮换遵循 `administrator-access.md` 的完整策略，并额外满足：

1. 加入新 key version并设为 current，先保留旧 key。
2. 确认所有 Control 实例已经只签发新版本；滚动发布期间不能提前移除旧 key。
3. 从最后一个可能签发旧 cursor 的实例切换完成后至少保留旧 key 15 分钟。TOTP 或其他认证材料要求的保留期通常更长，以最长要求为准。
4. 在合成会话中验证新 cursor使用新 key、未过期旧 cursor仍可翻页、满 15 分钟后两者都按固定错误失效。
5. 只有满足管理员认证 Runbook 的旧密文/摘要保留条件后才移除旧 key。

若 key 泄漏需要紧急移除旧版本，可以接受所有相关 cursor立即失效并让管理员从第一页重试；这不会修改账号状态。不得为了保住 cursor而保留已确认泄漏的 key。

## 5. 固定 HTTP 错误语义

| HTTP | 固定 code | 含义与处置 |
|---:|---|---|
| `400` | `validation_failed` | body/filter/limit非法，或 cursor 任一验证失败。修正封闭输入或清空 cursor，从第一页重试；不要从错误文本推断 cursor细节。 |
| `401` | `unauthorized` | session缺失、过期、撤销或管理员已不可用。重新登录，不得复用旧 cursor作为授权。 |
| `403` | `csrf_invalid` / `forbidden` | CSRF缺失/不匹配，或主体无 super-admin权限。刷新受控会话或纠正授权，不得关闭 CSRF。 |
| `404` | `not_found` | 当前环境不存在该 instance。通过资产只读视图重新选择 Node，不要探测目标地址。 |
| `409` | `conflict` | Node 已登记但没有账号清单 capability。核对登记的 node type/Driver contract/capability，不要直接请求 Node。 |
| `503` | `temporarily_unavailable` | PostgreSQL、query/audit事务、commit、状态一致性或服务依赖不可用。按第 6、7 节恢复，不能返回缓存或部分 email。 |

所有错误只保留固定 code、HTTP status和 request ID。不要把 request body、cursor、响应 body或底层 `error.Error()` 粘贴到日志、工单或验收证据。重复一个已经成功但客户端未收到响应的请求可能形成另一条合法 view audit；不要删除或去重审计。

## 6. 审计失败与恢复

每一页（包括空结果、后续页和相同请求的重试）都必须在返回前提交一条实名 `account_inventory.view`。查询与 audit insert位于同一短事务；audit insert或 commit失败时返回 `503`，不得序列化任何 item/email。commit成功后即使客户端断开，审计仍保留。

审计 details 只允许：`instance_id`、四类 `*_filter_used` 布尔值、`cursor_used` 和 `result_count`；actor、source fingerprint、request ID和时间由审计固定列保存。details 不得包含 filter值、email、account key、cursor、filter hash或结果 identity。

发生审计故障时：

1. 暂停账号页面 rollout或导航入口；不要关闭审计后继续返回结果。
2. 只用 request ID、固定 `temporarily_unavailable` 计数和数据库健康状态关联故障，不采集请求/响应 body。
3. 检查 PostgreSQL连接池、事务/statement timeout、`audit_logs` owner/constraint/空间和 runtime允许的审计写路径。
4. 修复依赖后使用合成 identity执行空结果与非空结果查询，确认两者均先提交 audit再返回。
5. 对 commit结果未知的请求保守视为“可能已查看”；保留可能存在的审计，并由操作者发起全新请求。禁止删除审计来追求一一对应。

## 7. PostgreSQL 不可用或状态不一致

Control/PostgreSQL停止只会使账号管理查询 fail closed；Gateway与Relay Node既有模型流量不依赖该页面。页面不得建立内存真相、回退历史 snapshot或触发 Node补采。

恢复顺序：

1. 确认故障范围只在 Control管理面；若数据面也失败，按独立数据面 Runbook处理，不要修改账号表恢复流量。
2. 恢复 PostgreSQL主实例、连接池和受保护 runtime/migrator连接配置，检查磁盘、锁等待、连接耗尽和 statement timeout；不要输出连接串或 SQL参数。
3. 核对 Migration `00008`、版本化函数、两个专用索引、owner/grant以及 audit constraints。不要执行生产 down。
4. 若固定 `503` 来自状态不一致，先停止新的 poll/promotion写路径，使用受控聚合检查定位缺失的 Provider pointer/current source。禁止人工填充默认 state、修改 lifecycle或从历史 snapshot重建。
5. 恢复新版本后重新执行兼容检查和合成查询。每个新请求直接读取已提交 PostgreSQL当前状态；没有队列、cursor恢复或停机页补做。

普通应用回滚保留 additive forward schema、专用索引和所有 audit rows。受保护 down只适用于无后续依赖、没有 `account_inventory.view` audit的全新环境；生产状态不得用 down修复。

## 8. 脱敏诊断清单

允许保留的诊断信息仅限固定 operation/result/error code、HTTP status、request ID、布尔 filter-used、result-size bucket、聚合数量、耗时和工具 exit status。逐项确认：

- 请求方法是 POST，URL恰好为 `/api/account-inventory/query`，没有 query string、redirect或 `Location`。
- access/application log未记录 body；PostgreSQL参数日志保持关闭或脱敏。
- 响应和错误都有 `Cache-Control: no-store`；浏览器刷新、退出页面或登出后 email输入和 cursor栈消失。
- `localStorage`、`sessionStorage`、history、analytics、错误上报、audit details、指标标签和保留测试 artifact没有 email/account key/cursor canary。
- 查询网络只包含浏览器→Control和Control→PostgreSQL；Node、Gateway、Prometheus、模型数据面和任意外部 URL计数为 `0`。
- API响应字段不含 account key、poll/policy ID、Node version/commit、endpoint、Secret ref、原始计数/桶或 raw error。

不要保存数据库 dump、HTTP body、浏览器 trace/HAR、截图、Cookie jar、CSRF、keyring、真实身份或原始日志作为证据。必须使用 canary 时，值由受保护测试环境注入；scanner失败只能输出固定分类，不能回显命中值或文件内容。

## 9. 发布与验证

发布顺序固定为：先在隔离 PostgreSQL 18验证 forward Migration和权限矩阵，再应用 Migration `00008`，随后发布同时理解新函数、cursor domain、API和UI的新 Control，最后开放导航。旧二进制回滚时保留 forward schema和审计，不执行生产 down。

所有 Go/npm/Docker/Make命令都清除大小写 HTTP/HTTPS/ALL proxy；PostgreSQL只使用受保护 service/password文件和隔离测试库。不得把数据库口令、Cookie、CSRF、email或cursor放入 argv或证据：

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

make generate
make test
make build
go test ./...
go test -race ./...
go vet ./...
(
  cd web
  npm test
  npm run typecheck
  npm run build
)
npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-readonly-query --strict
npx --yes @fission-ai/openspec@1.10.0 validate --all --strict
git diff --check
```

只有最终候选提交上的实际 Migration/schema/Store/HTTP/UI/canary/容量/故障恢复、连续生成和CI结果都已记录到验收证据，且临时数据库、容器、Cookie、浏览器输出和身份材料均已清理后，才能完成该 change。

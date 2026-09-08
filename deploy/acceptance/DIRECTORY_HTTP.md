# Directory / Binding HTTP 验收

正式入口为 `python3 deploy/acceptance/directory_http.py --config <protected-file> [mode]`。
默认 `read`，只读取显式指定的 Node、Gateway 和 Account，不创建绑定或调用模型。
部署、备份和故障开关使用兄弟仓库 ops 的 `dev/devctl`；见
[本地运维说明](../../../ops/dev/OPERATIONS.md)。HTTP 工具不操作 Docker 或数据库。

## 配置与认证

以 [无 Secret 样例](directory-http.example.json) 为字段参考，在仓库外的持久私有
目录创建配置；目录权限 `0700`，配置、Cookie、密码、当前 MFA 验证码、token、
模型请求和 baseline 文件权限 `0600`。拒绝符号链接、工作区和临时/RAM 盘配置。
样例路径是占位符，不能直接作为有效验收配置运行。

`gateway_instance_id`、`node_instance_id` 必须是目标 UUID；`gateway_account_id`
必须是正十进制 int64 **字符串**，例如 `"9007199254740993"`。不能使用 JSON number。
工具按身份读取，不从列表首项猜测目标。Directory source v1 的 account ID 仍为
numeric JSON，由 Python 整数解析；这不改变 Control HTTP decimal-string 契约。

优先读取 `session_cookie_file` 中的 `name=value`；正常登录可配置 `login_name`、
`password_file`。需要 MFA 时通过 `mfa_code_file` 提供当前验证码；不自动提取
TOTP seed 或个人恢复码。可用 `session_cookie_name` 显式指定现有环境 Cookie 名。
登录成功后会话保存到私有 Cookie 文件；失效凭据应通过正常认证流程更新。

HTTPS 使用系统 CA 或 `ca_file`；Directory 可单独配置 `directory_ca_file`。
工具不使用环境代理，不跟随重定向，不关闭 TLS 验证。
每次请求 `timeout_seconds` 为 1–30 秒，响应上限 4 MiB；`wait-stale`/`wait-recovered`
还会把登录、每次轮询和响应 body 读取共同限制在单一 `wait_timeout_seconds` 总预算内。
等待预算 `wait_timeout_seconds` 为 1–900 秒，`poll_interval_seconds` 为 1–30 秒。

## 模式

| mode | 行为 |
| --- | --- |
| `read` | 认证后核对目标当前 Binding，默认无变更 |
| `bind` | 同一绑定只核验；空绑定执行一次 POST；不同绑定拒绝替换。已有或对账得到的 `binding_id` 必须是非空 UUID。响应丢失先读回对账，无法确认返回失败，不重放 |
| `baseline` | 将目标、环境、Binding ID 和成功观测时间保存到私有 `baseline_file` |
| `assert-stale` / `wait-stale` | 同一目标 stale/unknown，成功观测时间不推进；wait 有明确截止时间 |
| `assert-recovered` / `wait-recovered` | 同一目标 fresh/resolved，成功观测时间严格推进 |
| `directory-check` | 有效 reader 200、缺失/错误 token 401、POST 405、unknown internal 404、public 403，以及 JSON/no-store/source v1 结构 |
| `data-plane` | 只向显式绝对 `http(s)` `data_plane_url` 发送一次请求，使用独立 Bearer token；不会复用 Control session/cookie/CSRF，仅断言响应结构，不记录模型内容 |

Directory 检查使用样例中的三个显式 URL；无需启停服务。source 关闭导致读取失败时，
不能视为空集合或成功。`data-plane` 会使用已有 Gateway 额度，只有显式选择才执行。
完整自然过期/恢复演练由 `devctl directory drill --acceptance-config <file>` 持锁协调，
使用真实 540 秒 freshness 窗口和正常 180 秒采集槽，不修改数据库时间。

退出码 `0` 表示断言成功，`1` 表示读取、认证、输入或断言失败，`2` 表示 CLI 用法错误。
标准输出/错误仅记录 mode/result/固定 reason 或等待进度；不要把私有 baseline、
配置或服务原始响应复制进公开证据。失败不得冒充空列表，也不得通过重复 bind 修复。

## 工具验证

```bash
python3 -m unittest discover -s deploy/acceptance -p 'test_directory*.py' -v
```

真实 PostgreSQL 自然恢复专项为 `TestLocalDirectoryToolingNaturalRecovery`，需显式设置
`RELAY_DIRECTORY_TOOLING_E2E=1` 和既有隔离数据库 owner/runtime 测试 URL。
它创建隔离测试 schema，通过真实 Control HTTP 登录、绑定和轮询验收；不使用现有
本地开发栈的清理入口。运行命令和实际结果保存在当前 change 的 planning-validation。

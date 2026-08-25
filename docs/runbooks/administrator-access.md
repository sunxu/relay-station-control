# Control 管理员访问 Runbook

本文覆盖 `add-control-authentication-foundation` 的 bootstrap、auth keyring、管理员恢复、会话撤销、部署和回滚。所有命令中的路径、账号和 Secret 都必须使用当前环境的受控值；不得把实际值复制到工单、聊天、Shell history 或仓库。

## 1. 环境隔离与前置检查

每套 dev、staging、production 必须使用独立的 Control、PostgreSQL、bootstrap Secret 和 auth keyring。执行任何恢复前确认：

1. `environments.environment_id` 与本次目标环境一致。
2. 数据库已有可恢复备份，连接使用当前环境专用账号。
3. production 外部入口为 HTTPS，session Cookie 保持 `Secure`。
4. 操作期间不修改 Gateway 或 Relay Node；Control 下线不得影响请求数据面。

数据库必须使用两个独立身份：Migration owner 只由 Goose/受控变更流程使用；Control
产品进程使用环境专属 LOGIN，且该 LOGIN 仅继承固定的 `relay_control_runtime` NOLOGIN
能力角色。运行时账号不得拥有 schema、table、sequence 或 function，也不得有
`CREATEROLE`、`CREATEDB`、superuser、replication 或 bypass-RLS 属性。生产环境由 DBA
或 IaC 在 Migration 前创建角色和授权，密码仅来自 Secret Manager；仓库中的
`relay_control_app_dev` 及密码只适用于 tmpfs 开发数据库。

## 2. 生成和挂载 bootstrap Secret

bootstrap Secret 仅用于尚未完成初始化的数据库。使用密码学安全随机源生成至少 32 bytes，写入权限为仅容器运行用户可读的 Secret 文件。不要把 Secret 放入环境变量值、Compose 文件或命令行参数。

将文件以只读方式挂载并通过 `CONTROL_BOOTSTRAP_SECRET_FILE` 指向它。完成以下检查：

- Secret 文件不是仓库内路径。
- 容器内文件不可被其他用户读取或写入。
- 日志、metrics、Trace、API 响应和审计中搜索不到 canary Secret。

最终镜像固定以 UID/GID `65532:65532` 运行，不能直接读取常见的 root-owned `0400`
投影 Secret，也会拒绝 group/other-readable 的 `0440`/`0444` 文件。Kubernetes 必须用 root
init container 把投影 Secret 复制到 `emptyDir.medium: Memory`，对复制后的 bootstrap 与
keyring 文件执行 `chown 65532:65532`、`chmod 0400`，再由应用容器只读挂载该内存卷；
应用容器自身不得以 root 运行。Docker/Compose 使用宿主机 bind mount 时，应由部署流程在
挂载前把专用文件 owner 设置为 `65532:65532`、mode 设置为 `0400`，不得依赖 Docker Secret
默认的 `0444`。容器验收必须实际以 `65532:65532` 读取两个文件，并分别验证 owner 错误、
权限过宽或文件缺失时 production 拒绝启动。

首个管理员完成密码、TOTP 和恢复码保存后：

1. 验证 bootstrap 状态为 `completed`。
2. 使用原 Secret 重试 bootstrap，确认被拒绝并产生脱敏安全审计。
3. 从容器挂载和 Secret Manager 当前版本中移除 bootstrap Secret。
4. 重启 Control，再次确认 bootstrap 仍为 `completed`。

删除或轮换 bootstrap Secret 不能重新打开已完成的数据库。禁止通过修改数据库状态绕过这一约束。

## 3. auth keyring 创建、备份和轮换

`CONTROL_AUTH_KEYRING_FILE` 是环境独立的版本化 keyring。current master key 必须为 32 bytes 随机值，文件只通过 Secret 文件或 Secret Manager 挂载。production 缺少或格式非法时 Control 必须拒绝启动。

备份要求：

- keyring 使用与 PostgreSQL 备份不同的受控 Secret 存储。
- 备份保留所有仍被 TOTP 密文引用的 key version。
- 恢复演练只使用合成管理员；演练记录不得包含 key、TOTP Secret、恢复码或 session token。

轮换顺序：

1. 向 keyring 增加新版本并标记为 current，保留旧版本。
2. 重启或热加载 Control，确认新 token/密文使用新版本，旧 TOTP 仍能解密。
3. 使用受控重加密流程逐批把旧 TOTP 密文迁移到新版本，并核对剩余引用数。
4. 撤销或等待旧 session、challenge、activation token 和 recovery code 自然失效。
5. 只有数据库确认无旧 TOTP key version 引用后才移除旧 key。

提前移除旧摘要 key 会使相应会话和 token 失效，应按“全量登出”处理；提前移除仍被 TOTP 引用的 key 会导致管理员无法登录，禁止执行。

## 4. 创建和激活第二名管理员

生产环境完成 bootstrap 后应尽快建立第二名实名 `super_admin`，避免单人恢复风险：

1. 当前管理员完成密码和 TOTP 重新认证。
2. 输入唯一登录名、实名显示名和 10–500 字符原因。
3. 创建待激活管理员，将只显示一次的激活 token 通过受控带外渠道交给本人；禁止放入 URL、邮件正文、聊天记录或工单。
4. 新管理员在 24 小时内手工粘贴 token、设置密码、注册 TOTP 并保存 10 个恢复码。
5. 两名管理员分别登录并核对独立 actor ID 和审计记录。

token 过期或疑似泄漏时，原创建人重新认证并生成新 token；该操作必须同时撤销旧 token。

## 5. TOTP 丢失和恢复码处置

管理员可在密码验证后使用一个未消费恢复码完成 MFA。每个恢复码最多成功一次；成功后核对剩余数量并尽快重新生成完整批次。重新生成会原子撤销所有旧码。

当管理员既无 TOTP 又无恢复码时：

1. 由另一名有效 `super_admin` 核验本人身份。
2. 操作者完成重新认证并填写原因，执行目标管理员 MFA reset。
3. 系统撤销目标 TOTP、恢复码、challenge、activation token 和全部 session，使目标回到安全的待激活状态。
4. 生成新的单次激活 token，由本人重新设置 MFA。

禁止用重开 bootstrap、直接写数据库、共享他人 session 或临时关闭 production MFA 进行恢复。

如果所有管理员均失去 MFA/恢复码，这是安全升级事件：保持 Control 管理面 fail closed，保护数据库和 keyring 备份，按独立审批的离线恢复方案处理。当前 change 不提供绕过认证的 break-glass API。

## 6. 禁用管理员和全量撤销会话

禁用操作必须由另一名管理员重新认证并填写原因。系统应原子完成目标禁用、session/challenge/token 撤销和审计。禁止 self-disable，也不得禁用最后一个已激活且启用的管理员。

发生账号泄漏时：

1. 禁用目标管理员并确认其所有 session 已撤销。
2. 轮换目标密码和 MFA；若怀疑 auth keyring 泄漏，按第 3 节执行全环境 keyring 轮换。
3. 检查登录失败、MFA、重新认证、管理员变更和 session 撤销审计。
4. 若需要让所有管理员重新登录，执行受控的 session 全量撤销；不得删除管理员或重开 bootstrap。

## 7. Forward deployment

1. 备份 PostgreSQL 和 auth keyring。
2. 在同版本副本验证 OpenAPI/sqlc 生成物无差异和 Migration up/down/up。
3. 先执行 forward Migration，再部署新 Control 应用。
   Migration 使用 owner 连接；部署的 `DATABASE_URL` 必须切换为受限产品连接。上线前
   以产品连接验证 `audit_logs` 可 `INSERT/SELECT`，但 `UPDATE/DELETE/TRUNCATE` 均返回
   权限错误，同时确认表 owner 不是产品身份。
4. 验证 `/api/healthz`、production 配置 fail-closed、Cookie/CSRF、登录/MFA 和审计。
5. 验证停止 Control 或数据库后，Gateway 和 Relay Node 的既有受控冒烟请求仍成功。
6. 完成 bootstrap 和第二管理员流程后，才把后续管理 API 接入统一授权中间件。

## 8. 应用与数据库回滚

默认回滚只回退应用镜像并保留新增认证表和 `completed` bootstrap 状态。回滚前确认旧应用能忽略新表且不会重新暴露匿名管理入口。

不得在以下任一条件成立时执行 down Migration：

- bootstrap 已完成；
- 已创建管理员、MFA、恢复码、session 或审计；
- 后续 Migration 或功能引用认证表；
- 未完成数据库和 keyring 一致备份。

只有全新、未完成 bootstrap、确认没有依赖数据的环境，才允许人工执行 down。执行前后都必须核对 `environments` 单例保持不变。若生产恢复涉及身份或审计数据，优先从一致备份恢复到隔离实例验证，禁止用 down Migration 作为数据修复手段。

## 9. 验证与证据脱敏

每次演练至少保存以下非敏感结果：Migration 版本、测试用例名称、固定错误码、HTTP 状态、审计 action/result、容器 UID/GID、数据面冒烟成功与时间窗口。证据不得包含真实管理员登录名/显示名、IP、密码、TOTP Secret/验证码、恢复码、Cookie、CSRF、activation/bootstrap token、keyring、数据库连接串或未脱敏响应。

## 10. 自动化容器与浏览器验收

仓库提供 `deploy/acceptance/control-auth-e2e.sh`。运行目录必须是 workspace 外的显式绝对路径；脚本仅在该目录生成合成 bootstrap Secret、keyring、测试密码和一天有效的 localhost 自签证书。默认保留运行目录供人工安全删除；只有运行目录匹配脚本限定的 `/tmp/relay-control-auth-e2e.*` 格式且显式设置 `CONTROL_E2E_CLEAN_RUNTIME=1` 时才自动清理。

```sh
CONTROL_E2E_RUNTIME_DIR=/absolute/private/runtime \
  deploy/acceptance/control-auth-e2e.sh all
```

验收使用固定 digest 镜像、production 配置和本地 HTTPS 反向代理，检查：

- `linux/arm64` 镜像及 UID/GID `65532:65532`；
- bootstrap/keyring 文件在应用容器中为 `65532:65532`、`0400`；
- Secret 缺失、权限过宽、production 非安全 Cookie 时拒绝启动；
- 认证响应和 Web 认证壳为 `Cache-Control: no-store`；
- bootstrap、TOTP、恢复码、第二管理员创建/激活/禁用与进程重启；
- 报告关闭 trace、截图和视频，错误上下文只写到外部运行目录并在每次运行后删除。

数据面隔离测试不会创建、修改或销毁 Node 账号。只有显式提供现有 `PHASE0_RUNTIME_DIR` 时，脚本才调用 `ops/phase0/phase0ctl check`，先验证既有 Node 基线，再停止验收项目自身的 Control/PostgreSQL/TLS，复查同一基线，最后恢复验收项目。未提供时必须记录 `SKIP`，不能据此宣称数据面隔离已验收。

## Context

动机见 proposal.md。当前临时 `relay-directory-http.py` 混合登录、候选读取、bind、baseline、stale/recovery及AI冒烟，使用固定宿主路径、既有个人登录文件、单Node断言；比较timestamp使用字符串，账号输入来自另一份本地运行配置。部署脚本又各自读取Compose/环境、修改override、备份并更新服务，容易让不同入口选择不同镜像。

ops `dev/devctl` 已有runtime路径检查、Secret初始化/加载、Compose封装、版本检查、迁移和健康检查。Control `deploy/acceptance/control-auth-e2e.sh` 已有认证测试设施，但它的all/container模式包含独立栈初始化与volume清理，不得用于现有本地栈冒烟。旧临时脚本仅作为行为参考，不原样复制，更不复制运行数据。

## Goals / Non-Goals

**Goals:** 清除RAM盘依赖；将已验证行为参数化、可重复执行且默认无变更；将更新/开关的实际写入集中到devctl；让失败、未知结果和恢复动作可识别。

**Non-Goals:** 通用部署框架、任意服务插件、自动账号创建/导入、自动rebind/unbind、自动修复、自动DB还原、Secret轮换、新调度或产品API。无需新产品spec；CLI约束属于本工具设计。所有产品状态仍由Control DB与既有API提供。

## Decisions

### 1. 两个入口，各自复用既有职责

- Control：在 `deploy/acceptance/` 增加一个Directory/Binding验收入口，复用现有HTTP认证惯例及stdlib。可采用shell薄入口加Python3标准库；不引入第三方Python包，不建立通用客户端框架。
- ops：在现有 `dev/devctl` 增加 `update <control|gateway> <image>`、`backup <control|gateway>`、`directory <source|poller> <on|off>`、`directory drill --acceptance-config <protected-file>`、`recover <operation-id>` 的命令形态。实施前核对help/名称冲突后固定文档；不新增平行release CLI，不自动build/pull/fetch，不改变现有up的整栈版本检查。
- 验收脚本不能直接编辑Compose或docker重启；需要故障注入时调用devctl明确操作。配置和backup保留仓库外；不把临时脚本的prepare/fill移入常规部署，首次reader补填仍使用现有registrar SQL。

### 2. 显式配置与最小身份输入

使用仓库外protected配置指定Control URL/CA、会话或登录凭据文件、Gateway UUID、Node UUID、Gateway Account decimal-string ID；数据面配置与Directory reader凭据单独引用，仅对应模式读取。移除固定用户名、个人路径及“取列表首项/只有一个Node”的假设。基于显式UUID查询已有API，不根据名称或URL猜测身份。

Gateway Account ID输入必须是十进制字符串；拒绝numeric配置，保持HTTP read/write字符串，不经过浮点转换。合法domain与int64范围沿用既有API；四个大整数及overflow复用既有fixture，不扩大ID域。

输入文件必须普通文件、非symlink、owner-only、非空且有界；目录0700，私有会话/baseline/备份0600或更严。拒绝仓库内和RAM盘依赖路径作为永久部署配置；临时合成测试目录可另行使用。重定向、原始异常、shell trace及子进程输出均不得泄漏密码、token、cookie、TOTP seed、账号或raw body。Control登录HTTPS使用系统或显式既有CA验证，不设置全局不验证证书；禁用环境代理和重定向，限制HTTP/body/总等待时间。

复用已认证会话；过期或MFA required时走正常登录/MFA流程，支持从受保护文件读取当前验证码，不自动取用个人恢复码或重置MFA。禁止对任意401无限重新登录；一次受控登录后仍失败则返回固定认证失败。写请求保留CSRF、reauth及现有审计，不绕过认证。

### 3. 只读与显式操作分开

默认read检查不绑定、不关闭source、不部署，也不调用收费AI请求。显式bind模式先查目标当前Binding：完全相同则只核验；空时执行一次已有bind；不同则失败，不替换。超时/响应丢失后先read精确身份对账；无法确认返回unknown，不盲目重复POST，不自动撤销已有Binding。

Binding 成功判定同时要求非空、合法 UUID 的 `binding_id`；既有绑定和丢响应对账都不能接受缺少此字段的记录。等待模式的总 deadline 包含认证、每次 HTTP 请求和轮询间隔，每个子步骤只能消耗剩余预算。数据面验收仅接受绝对 HTTP(S) URL，并通过独立 Bearer token 请求；不提供指向 Control 会话客户端的相对路径分支。

baseline只记录该次测试的目标、Binding ID、成功观测时间与非敏感环境定位，保存在受保护文件中；跨目标或错环境拒绝复用，不以baseline覆盖API状态。timestamp解析为UTC时间比较，不做未经规范化的字典序比较。

显式recovery演练要求已有fresh/resolved Binding及启用的source/poller，保存原开关和baseline后关闭source。等待真实540秒窗口，自然查询stale/unknown且last_success不变、Binding ID和Account字符串完全相同；恢复source，等待正常180秒槽成功，断言同一Binding resolved/fresh且观测严格推进。使用有界等待与进度输出，不伪造时间或手动写run/snapshot。显式data-plane模式才发送一个有界模型请求，只断言HTTP与响应结构，不持久化模型内容。

### 4. 单服务更新与恢复

update检查选定仓库工作树干净、完整revision label匹配HEAD、目标镜像本地存在、有效Compose配置最终选中该镜像。不能只设置env而被旧override覆盖。除下述固定关联代理外，未选服务不重建，也不因其无关文档dirty阻止单服务更新；这种操作不得声称通过既有整栈up preflight。

仅支持既有本地Compose项目与显式runtime目录。所有devctl写操作共享同一runtime目录锁，包含up/down与新命令，拒绝并发而非互相覆盖；只读status不阻塞。复用macOS/Linux可用的原子文件系统锁，不引入守护进程或新依赖；锁中断恢复须验证所有者已终止，不能随意删除另一个运行实例的锁。

修改前保存原镜像与受保护配置，通过同目录临时文件+原子rename提交配置，保留未知字段、Secret挂载、原开关及其他服务。update允许变化的服务集合固定为：更新control时为 `control` 与 `control-tls`，更新gateway时为 `gateway` 与 `gateway-proxy`。只重建目标应用，随后重建对应现有代理以刷新上游解析；代理镜像digest和配置保持原值，不升级代理。恢复原应用时同样刷新该代理。两者的前后container ID单独记录；集合外全部服务的container ID、image digest、有效配置摘要必须不变。不得启动secret-init、数据库、Redis、Node或另一个应用。健康与关键HTTP检查失败时恢复原配置及已存在的兼容旧镜像，并再次检查；回滚失败单独报告，不能返回成功。

update在首次变更前复用backup入口完成选定数据库与匹配配置备份；dump通过 `pg_restore --list` 检查，备份失败则零变更。此检查仅证明归档可读，不冒充完整还原演练。无变更的同镜像重放只核验，不重建或重复备份。

本命令不显式执行migration或数据库down，不自动回退跨schema不兼容版本；以下准入检查全部通过后才允许启动新镜像。Gateway正常初始化在 `backend/internal/repository/ent.go` 调用内嵌迁移，因此仅省略迁移命令并不能避免schema变化。backup使用选定数据库的现有备份方式并保存匹配配置/镜像信息，成功后才标记完整；partial文件不能被报告为可恢复备份，不新增在线跨库一致性承诺。数据库还原仍是独立人工恢复操作。

recovery演练在EXIT/INT/TERM恢复原source/poller配置，正常完成也恢复原值；不自动解绑。SIGKILL不能保证执行trap，因此变更前保存一个最小未完成标记和原配置，后续写操作检测后停止，提供明确恢复入口；该标记只用于本地工具恢复，不是业务真相或任务调度器。恢复采用同一runtime锁，先核对环境及当前配置，外部修改冲突时拒绝覆盖并报告。

#### Schema准入：只接收同一迁移基线的更新

在持有runtime锁且任何服务/配置变更之前，以正在运行容器的实际image ID查旧镜像完整revision，并以本地新镜像ID查新revision。两个commit均须在选定本地仓库存在，新revision须等于干净HEAD；标签缺失、commit不可读或旧镜像已丢失返回 `schema_compatibility_unknown`，不fetch、不强制放行。后续启动及回滚按已检查的image digest固定，防止tag被重新指向其它镜像。

采用保守的相等检查，不设计跨版本兼容推理：

- Control：比较两commit的 `migrations/` 全部文件路径与Git blob ID；并核对 `cmd/control/` 非测试启动代码、`go.mod`、`go.sum` 未改变。新迁移、历史SQL变更、删除或迁移启动行为无法确认均拒绝走此入口。
- Gateway：比较 `backend/migrations/` 全部文件，以及 `backend/internal/repository/migrations*` 非测试文件、`backend/internal/repository/ent.go`、`backend/internal/setup/` 非测试文件、`backend/cmd/server/` 非测试文件、`backend/go.mod`/`go.sum`。任何差异返回 `schema_change_requires_full_release`。显式包含runner及启动路径，不能只比较最大迁移编号。
- 通过选定数据库的既有运维只读连接检查已应用迁移记录：Control Goose按每个version最新记录计算的当前应用集合须匹配相同SQL集合且无pending/额外版本；Gateway `schema_migrations` 文件名/校验和须精确匹配相同内嵌SQL集合，Atlas基线须存在且符合该版本runner期望。缺表、漂移、旧checksum兼容例外或无法完整解释的记录都拒绝；不在工具中复制宽松兼容规则。连接不进入产品容器，不增加runtime权限。

准入保存迁移集合摘要和DB记录摘要；目标启动后、回滚前重新核对DB摘要。发生意外变化时返回 `schema_changed_manual_recovery_required`，保留备份和未完成标记，禁止自动回退旧镜像或还原DB。工具锁不隔离外部数据库管理，故检测到外部变更同样停止。此机制假定镜像来自已评审、可追溯的本地构建，不宣称仅凭revision可证明任意镜像内容可信；可能有隐式数据迁移的发布不在此轻量入口范围内。

#### 中断与显式recover

一份每runtime未完成记录，不新增数据库或通用工作流。`recover <operation-id>` 是唯一允许处理已有未完成记录的写入口；普通up/down/init/update/directory/backup发现记录均停止。无记录的重复recover返回no-op；ID不匹配则拒绝。正常运行操作的内部收尾使用同一持锁过程，不递归取得第二把锁。

记录为版本1受保护JSON：operation_id、kind（update/directory/recovery-drill）、Compose project和规范化runtime定位、owner host/PID/进程启动标识、created_at UTC、允许变化的服务集合、旧/新image digest与revision、每个涉及配置文件的旧/新SHA-256及私有备份相对路径、原开关、迁移/DB基线摘要、phase。恢复只接受该runtime内的普通备份文件，拒绝路径穿越或symlink，不输出配置正文。旧、新配置摘要指有限参与文件，不把不断变化的业务数据作为配置冲突。

phase仅为 `prepared`、`applying`、`verified`：prepared在备份齐全且变更前原子落盘；applying在首次配置或服务变更前持久化；verified仅在最终健康/不变量检查后持久化。多文件更新逐文件原子rename，不能假设多文件事务；每个当前文件可匹配记录的旧或新摘要，任一两者都不匹配即 `recovery_conflict`，不覆盖。recover在prepared/applying均恢复原配置、原digest及对应代理，并核验健康与schema；verified只核对记录的最终状态，符合则完成清理，不再回滚成功更新。故障演练的最终状态为原开关已恢复，不是source关闭状态。

恢复不依赖phase推断容器是否已启动：实际inspect及文件摘要决定需要重建的步骤；镜像不属于记录的旧/新digest视为外部冲突。恢复前证明原owner不再存活，PID复用、跨宿主或不能可靠识别则停止，不自动抢锁。成功验证后原子完成记录移出pending并释放锁；崩溃发生在verified和清理之间时重复recover仍幂等。回滚过程中再次中断保留applying，允许重复显式recover；恢复失败保留记录并返回非零，不伪报成功。

`prepared/applying` 恢复允许固定目标应用或代理处于“Compose 查询成功但没有容器”的中间状态；先核对配置、备份、schema 与未选服务，使用记录的旧应用和代理 digest 重建，再严格核验健康与完整不变量。Docker 查询失败不等于容器缺失；现存容器使用非记录镜像或配置仍必须拒绝。`verified` 阶段缺失容器不满足最终状态，不得用回滚掩盖。

代理配置摘要覆盖容器 Config、稳定的 HostConfig、挂载和网络配置，包括端口绑定、挂载源/目标及模式、重启策略和网络连接；仅排除重建必然变化的容器 ID、动态 IP、endpoint 等运行标识。缺失新版摘要的旧恢复记录不得被视为通过完整配置核验。

演练所需多次source操作由一个持锁的 `devctl directory drill --acceptance-config <protected-file>` 演练协调过程完成，HTTP harness只执行read/baseline/断言子阶段；不让跨命令的pending标记阻止演练自己的合法恢复。该协调过程只组合固定source开关和已有HTTP验收，不泛化为任意任务执行器。INT/TERM退出先有界恢复，SIGKILL后由显式recover恢复；长等待期间输出固定阶段与耗时。

### 5. 持久证据和兼容

正式入口与运行文档不依赖 `/Volumes/DevRAM/tmp`。输出固定case/result/reason、耗时和允许公开的revision/image digest；真实身份、URL中凭据、响应正文只在必要内存/受保护baseline中。公开evidence与私有会话/backup分目录；canary同时检查stdout/stderr与公开证据文件。

新增文档建立旧脚本mode→新入口映射，并引用既有归档历史说明旧结果的时间与来源。归档原文和哈希不改，不追认正式脚本曾执行过旧验收；新版本需取得自己的结果。一次性OpenSpec同步脚本和含个人假设的inspect模式无需固化；不自动删除任何旧脚本或数据。

HTTP验收入口包含显式Directory-check模式：reader有效200、缺失/错误401、POST405、unknown internal404、public403、JSON/no-store及source v1 numeric ID形态。source关闭时按既有404契约核对，不把404当空accounts成功；Control读失败也不能转为空列表。该模式不启停服务、不创建Account或密钥，不重跑Gateway性能压力测试。

## Risks / Trade-offs

- 自然过期验收较慢 → 复用产品固定窗口，仅合成工具测试用可控时钟；真实联合验收保留自然时间，不缩短产品预算。
- 版本label不是源码可信证明 → 构建来源/clean commit由既有构建流程保证；本工具不支持拉取远端或重新标记旧镜像冒充新commit。
- 更新中断或外部运维修改配置 → 原子写、共享锁、原值/未完成标记和冲突检查；不做无条件覆盖回滚。
- 复用大而全e2e误删开发数据 → 新入口禁止调用旧all/container清理路径，mock命令断言不含down --volumes及数据删除。
- 将所有脚本搬入仓库增加维护负担 → 仅固化三类必要能力：HTTP验收、devctl更新/备份/开关、证据/文档映射，不增加框架。

## Migration Plan

0数据库migration，0 OpenAPI/sqlc/generated client变化。先实现无外部副作用的输入/命令构造/恢复测试，再在隔离Compose验证，最后经执行授权在现有本地栈完成非破坏更新与一次显式故障恢复。正式入口验证通过前保留临时脚本；退回旧工具只涉及运维文件，不能回退DB或覆盖账号。Control/ops独立提交，分别记录commit与命令；无需为工具固化重建或部署Gateway/Node产品代码。

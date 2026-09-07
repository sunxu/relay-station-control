## ADDED Requirements

### Requirement: Directory runtime SHALL 受显式开关和进程生命周期管理

Control SHALL 默认关闭 Directory runtime；显式启用后由产品进程运行既有调度、工作与恢复流程，并使用受限 runtime 数据库身份。关闭时 MUST 不发 Directory 请求、不新增运行或刷新观测。配置非法时 MUST 启动失败且不泄漏配置值；有效配置下单个 Gateway 的网络或凭据失败 MUST 进入既有 durable failure 流程，不停止其他 Control 能力或数据面。

#### Scenario: 默认关闭
- **WHEN** 未配置启用开关而 Gateway 已登记
- **THEN** 不产生 Directory 网络请求或 ingestion run，既有 snapshot 不被删除或刷新

#### Scenario: 正常启动与停机
- **WHEN** 显式启用后启动 Control，再发送停机信号
- **THEN** 启动固定 cadence 工作；停机停止领取新任务，取消并等待在途工作，未完成任务由已有 lease/reconciler 恢复

#### Scenario: 重启和多实例竞争
- **WHEN** Control 在 claim 后重启，或两个进程处理相同槽
- **THEN** 复用既有唯一槽和 fencing 规则，仅合法持有者可 finalize，不创建并行状态机或重复成功观测

#### Scenario: 单次调度遍历与合成工作项进度
- **WHEN** runtime先执行ReconcileTick再执行WorkOnce，列表前项缓慢或失败，后项合法
- **THEN** 通过每项RunGatewayOnce中的共同ScheduleCurrent调度，不额外执行ScheduleTick；后项继续处理并使用轮到时的DB当前槽，同槽不重复、不回填历史，取消和既有预算保持不变

### Requirement: Directory transport SHALL 直接支持HTTP和不验证证书的HTTPS

Directory SHALL 直接使用登记的HTTP/HTTPS endpoint，不检查origin白名单、CIDR/DNS许可或HTTP opt-in。HTTPS MUST 不验证证书链、有效期和主机名；此行为 MUST 与Control的Gateway/Node管理出站策略一致，不影响Control入站或数据面。客户端仍须解析HTTP/HTTPS请求URL，保留既有资产登记契约、Bearer token认证及响应完整性验证。

HTTP明文传输以及HTTPS不认证服务端的事实 MUST 记录在Runbook。TLS握手失败 MUST NOT 自动降级为HTTP；所有redirect MUST 拒绝；超时、响应大小及账号数上限不变。

#### Scenario: HTTP直接成功
- **WHEN** 已登记HTTP目标返回合法source且reader token有效，未配置任何transport许可列表
- **THEN** 正常完成fetch、验证和durable finalize，freshness按成功DB观测推进

#### Scenario: HTTPS证书不验证
- **WHEN** HTTPS目标分别使用自签名、未知CA、过期或主机名不匹配证书，TLS握手可完成且token/source合法
- **THEN** Directory请求成功，不因上述证书属性拒绝；Node管理客户端采用相同策略，范围外客户端保持原策略

#### Scenario: 无效URL或TLS握手失败
- **WHEN** 请求URL无法解析、协议不是HTTP/HTTPS，或TLS握手无法完成
- **THEN** 记录既有配置/请求失败，不刷新freshness，也不自动改用HTTP

#### Scenario: 双协议redirect拒绝
- **WHEN** HTTP或HTTPS目标返回任意redirect
- **THEN** 不跟随，不向新目标转发token，沿用原失败分类

#### Scenario: Secret丢失与轮换
- **WHEN** 两种传输中的reader token缺失/撤销，之后在同一引用恢复
- **THEN** 失败不刷新freshness，后续合法读取可恢复；日志/指标/审计无token或reference

### Requirement: Runtime integration SHALL 保持既有 Directory 和 Binding 契约

集成 MUST 保留 180s 槽、540s freshness、5s attempt、15s lease、2 attempts 和 scheduled_at+120s retry start deadline；全部时间以既有 DB UTC 契约为准。source v1 numeric ID→Go int64 和 Control/Web decimal string 不变；全量验证、changed/unchanged 去重、失败不推进 last_success_received_at 均不变。成功 ingestion MUST NOT 自动创建 Binding 或改变 Gateway/Node 调度。

#### Scenario: 相同内容和失败恢复
- **WHEN** 连续两次合法响应内容相同，随后一次读取失败，再成功恢复
- **THEN** 成功观测更新时间但复用内容 snapshot，失败不刷新 freshness，恢复仅按既有规则接受

#### Scenario: 显式关联验收
- **WHEN** 管理员取得 fresh Directory 中的精确 Account ID，并通过既有 bind API 发送 decimal string
- **THEN** existing read 显示 BOUND/resolved/current；没有管理员 action 时仍 unbound，Directory 失败不会自动解绑

#### Scenario: 数据面隔离
- **WHEN** Directory runtime 被关闭或其管理 TLS/token 不可用
- **THEN** Gateway→Node 的原生调用仍正常，Control 不修改 Account、Group、路由或 Node 凭据

### Requirement: 每次Directory attempt SHALL 独立限制执行与失败收尾

成功claim后Control MUST 为该attempt创建不超过5s且响应父context取消的context，覆盖Secret、fetch、响应处理与成功finalize；不得与整个Gateway遍历共享预算。超时后 MUST 停止attempt，不得用新context提交成功。失败记录 SHALL 使用仍有效runtime父context派生的独立最多5s收尾context及原fencing；不延长15s lease。父context取消、失去lease或写入结果未知时 MUST 交由既有reconciler恢复，不无界后台写入、不伪造成功或失败。

#### Scenario: 慢响应与慢body
- **WHEN** 合成source阻塞响应头或body超过5s
- **THEN** 请求在attempt截止时取消；真实PG中失败收尾使用有效context记录timeout，按既有重试次数进入retry_wait或failed，freshness不推进

#### Scenario: 成功finalize阻塞或提交未知
- **WHEN** finalize等待超过attempt截止或commit结果未知
- **THEN** 不在新context中重做成功提交；仅按原fencing/reconcile确认DB状态，不覆盖已经提交的成功

#### Scenario: 停机或失败收尾超时
- **WHEN** 父context取消、失败收尾超过5s或lease已失效
- **THEN** 有界返回且不启动脱离停机的后台任务；未确认状态由原lease/reconciler恢复

### Requirement: 未配置Gateway SHALL 不创建Directory run且不阻断有序工作项

所有ScheduleCurrent入口 MUST 在与首次补填共用的Gateway行锁内检查reader_secret_ref；NULL SHALL 返回no-work且不创建run、不解析Secret、不发请求。非NULL引用的Secret或认证故障 MUST 走正常durable failure，不得归类为未配置。单Gateway失败不得终止其他Gateway遍历；父context取消或共享DB不可用可以结束本轮。历史NULL run MUST 保留并由既有reconciler处理，不绕过首次补填的零历史限制。

#### Scenario: 合成未配置A与已配置B
- **WHEN** 合成有序工作项中A排在B之前，A返回NULL对应no-work，B返回成功；真实单Gateway PG分别执行NULL及已配置fixture
- **THEN** 合成遍历继续处理B；真实PG证明NULL零run/零请求且之后仍可首次补填，已配置正常成功并从当前槽开始、不回填历史；不要求或允许同库登记两个Gateway

#### Scenario: 首次补填与调度竞争
- **WHEN** NULL Gateway的调度和registrar补填并发，分别控制两个行锁获取顺序
- **THEN** 调度先时无run且补填成功；补填先时调度正常建run；不存在NULL reference新run

#### Scenario: 已配置但Secret失败
- **WHEN** 合成工作项A返回凭据失败，B合法；真实单Gateway PG另外执行文件缺失/token错误fixture
- **THEN** 合成遍历继续处理B；真实PG证明非NULL故障保留durable失败证据且不刷新freshness

### Requirement: Directory runtime SHALL 通过受限有效lease读取目标

Control runtime MUST 通过SECURITY DEFINER只读函数获取instance_id、management_endpoint和opaque reader_secret_ref，且仅在run/Gateway/fencing匹配、running、lease未过期及reference非NULL时返回。函数 MUST STABLE、固定search_path、migrator owner，只有runtime可EXECUTE。runtime MUST NOT 获得reader_secret_ref直接SELECT；数据只供采集及Secret解析。共同调度 SHALL 使用已有reader_secret_configured列判断NULL等价条件。

#### Scenario: 有效与无效lease
- **WHEN** runtime分别以有效、错误Gateway/token、过期或terminal run调用目标函数
- **THEN** 只有有效lease返回目标；其他调用零行，不能解析或发送Secret

#### Scenario: 直接读取和越权调用
- **WHEN** runtime直接SELECT reader_secret_ref，或PUBLIC/registrar角色调用函数
- **THEN** permission denied；既有列级读取权限不扩大

## MODIFIED Requirements

### Requirement: Control SHALL 使用固定 HTTP fetch contract

Control SHALL 以 `GET /internal/v1/api-account-directory` 通过 HTTP 或 HTTPS fetch Directory（无目标许可列表，HTTPS不验证证书），并 MUST 使用 `Authorization: Bearer token`，其中 token 由现有 `gateway_instances.reader_secret_ref` 经 `SecretResolver` resolve 后获得；reference 本身不得被当作 token/path。HTTP fetch MUST NOT follow redirects；single response body 的读取上限 MUST be 4 MiB；`accounts` 上限 MUST be 10,000；非 200、timeout、partial body、retryable failure 以及读取超限 MUST 先记录 attempt failure，再按 retryability 分类；raw body MUST NOT 被持久化。

#### Scenario: 正常 fetch
- **WHEN** Control 以HTTPS或HTTP对 `GET /internal/v1/api-account-directory` 发起带 Bearer token 的请求
- **THEN** fetch 继续进入验证流程

#### Scenario: redirect
- **WHEN** Gateway 返回 redirect
- **THEN** Control 不跟随 redirect，整轮 ingestion failed

#### Scenario: body 超限
- **WHEN** response body 超过 4 MiB
- **THEN** Control 停止读取并把整轮 ingestion 标记为 failed

#### Scenario: 账号数超限
- **WHEN** `accounts` 数量超过 10,000
- **THEN** Control 整轮 rejected/failed，不保存 raw body

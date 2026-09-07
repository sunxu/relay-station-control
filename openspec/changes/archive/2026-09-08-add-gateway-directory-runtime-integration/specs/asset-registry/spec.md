## ADDED Requirements

### Requirement: Gateway Directory reader reference SHALL 仅通过受控操作首次补填

Control SHALL 提供独立 registrar 操作，只为从未产生任何 Directory run/observation/snapshot/current state 且没有任何 Binding 历史的既有 Gateway 将NULL reader_secret_ref补填为合法非空opaque reference；MUST NOT修改management endpoint或替换非NULL reference。稳定 instance ID、环境身份和名称 MUST 保留，既有 register signature 与严格重放语义 MUST 不变。操作 MUST 以NULL为唯一可写前置值并以原子事务提交；必须防止与首次调度/绑定竞争，拒绝已有采集历史后的首次补填。上述零历史限制仅约束真实变更；授权调用的相同reference重放 SHALL 返回无写入、无新增审计的 no-op，即使其后已有历史。此能力 MUST NOT 成为 runtime 直接 UPDATE 或产品 HTTP/UI mutation。

#### Scenario: 未接入的既有 Gateway
- **WHEN** registrar 为无任何采集/绑定历史的 Gateway 为NULL reader reference提交合法非空reference
- **THEN** 原子补填reference，endpoint保持不变，保留 UUID、环境和名称；之后启用采集可获得 fresh Directory

#### Scenario: 有历史或并发竞争
- **WHEN** 请求将补填NULL reference且已存在成功、失败或未完成的 Directory run、任一 snapshot/observation/current state、任一 Binding 历史，或已配置Gateway已有调度记录
- **THEN** 补全操作拒绝且不修改资产；不能删除历史或失效证据来绕过条件

#### Scenario: 并发补全与重放
- **WHEN** 两个不同reference的补填竞争同一个NULL字段，或原操作被重放
- **THEN** 不允许丢失更新；已达到完全相同reference时返回幂等 no-op，不重复审计，非NULL且不同的reference请求拒绝

### Requirement: Gateway 配置补全 SHALL 保持最小权限与原子审计

新增操作 MUST 使用 migrator 所有的 SECURITY DEFINER 函数、fixed search_path、全限定对象名，撤销 PUBLIC 执行权，仅授予 registrar；runtime 不获得 endpoint/reference UPDATE 或函数 EXECUTE。成功变更与 append-only audit MUST 在同一事务；审计仅包含固定 action、管理员 actor、Gateway UUID、操作时间及非敏感变更标志。失败不得部分更新，审计不得包含 endpoint、reference、token 或其可逆派生值。

#### Scenario: Runtime 和 PUBLIC 拒绝
- **WHEN** runtime 或未授权角色调用补全函数或直接修改 endpoint/reference
- **THEN** permission denied，资产与审计均不改变

#### Scenario: 审计失败
- **WHEN** 合法补全过程的 audit 写入失败
- **THEN** 整个事务回滚；恢复后可安全重试，不出现没有审计的配置修改

#### Scenario: 非法目标与凭据泄漏
- **WHEN** 输入非法或空reference、错误UUID，或尝试替换非NULL reference
- **THEN** 操作拒绝，错误/日志/审计不回显输入，原身份及配置保留

#### Scenario: HTTP登记与补填
- **WHEN** registrar为既有HTTP endpoint补填reference
- **THEN** 资产补全本身不发网络请求；启用Directory后直接使用该endpoint，无额外HTTP许可配置

#### Scenario: NULL reference调度先取得锁
- **WHEN** 无历史Gateway尚未补填，scheduler先取得Gateway行锁
- **THEN** scheduler不建run；registrar随后仍能原子补填，不因启用runtime而锁死首次接入

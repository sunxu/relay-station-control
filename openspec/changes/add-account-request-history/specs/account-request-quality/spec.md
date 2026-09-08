## ADDED Requirements

### Requirement: Account Request History SHALL 受当前Inventory membership约束

Control SHALL仅从已有account_request_quality_events读取目标Node与非NULL account_key的最近7天具体事件。MUST先在既有current Inventory安全read model证明该账号存在，不能从events反推账号；无账号返回404，Inventory/DB故障返回503，禁止伪装empty。MUST NOT修改CLIProxy、collector、usage queue、event schema、retention、taxonomy或质量classification。

#### Scenario: 存在账号有事件或无事件
- **WHEN** Inventory目标账号存在
- **THEN** 有7天事件时返回该账号事件，无事件时成功empty，既有lifecycle集合不被改写

#### Scenario: 假账号与隔离
- **WHEN** 目标只有events而不在Inventory，或目标事件属于其它Node/账号/NULL身份
- **THEN** event-only目标404，其它Node/账号及unresolved事件不进入目标结果

#### Scenario: Gate故障与恢复
- **WHEN** Inventory read或DB失败后恢复
- **THEN** 失败503而非empty；重试只读取恢复后的真实数据

### Requirement: Request History SHALL 使用数据库时间及稳定有界分页

History SHALL固定DB statement timestamp最近7天，包含下边界并排除future，按occurred_at DESC、event_hash DESC keyset分页。GET `/api/topology/nodes/{instance_id}/request-history` SHALL接受required account_key与可选limit/cursor，无日期或其它过滤。limit默认25、最大100；opaque有界cursor MUST绑定Node/account/time/hash，错配或非法参数400，不能使用offset。response SHALL含instance_id/account_key/items/next_cursor，item仅Time/model/success/failure_class/duration_ms/request_id（Time JSON字段occurred_at）；hash仅在cursor内部，不作item字段。

#### Scenario: 七天边界与相同时间
- **WHEN** fixture有七天边界前后、future和相同timestamp不同hash事件
- **THEN** 只返回窗口内事件，按时间降序及hash降序分页，无重复漏行

#### Scenario: Cursor及limit
- **WHEN** cursor Node/account错配或时间/hash字段非法、limit不在1–100
- **THEN** 返回400，不读取其它账号或无界结果

#### Scenario: 单账号一万事件
- **WHEN** 查询10000事件账号的首25条、next page及limit100
- **THEN** 单次DB query、有界结果，复用现有index并记录本地耗时，不增加索引/缓存/rollup

### Requirement: History SHALL 保持只读授权与数据库ACL

API SHALL要求super_admin session、no-store、GET only及有界超时；无session401、非super_admin403。安全函数 SHALL为SECURITY DEFINER STABLE、固定search_path、migrator owner、PUBLIC revoke、runtime EXECUTE only；MUST NOT扩大direct SELECT或新增持久数据。

#### Scenario: 授权与方法
- **WHEN** 无session、非管理员或非GET请求
- **THEN** 401/403/方法拒绝且不执行history读取

#### Scenario: ACL及迁移回滚
- **WHEN** runtime执行function、直接读event表，或隔离执行Down/Up
- **THEN** EXECUTE可用、直接SELECT denied；Down只去掉新function、Up恢复，原表/索引/数据/函数不变

## MODIFIED Requirements

### Requirement: Driver 以白名单解析并正确判定 inventory mode

CLIProxyAPI Driver MUST 只投影批准的账号观察字段，并 MUST 在边界丢弃路径、project/auth/token/account/name及所有未知字段；仅对Antigravity runtime source=file允许有界解析status_message中的批准错误标识符，输出安全availability枚举后立即丢弃原文，不读取access/refresh Token值。非空响应全部 `source=file|memory` SHALL 判为 runtime；非空响应全部缺少 source 且符合固定磁盘形态 SHALL 判为 disk fallback；空数组 SHALL 保守判为 disk fallback；混合缺失、未知 source 或未知形态 MUST 使契约无效。

#### Scenario: file 与 memory runtime 响应
- **WHEN** 非空 `files` 的每条记录都有 `source=file|memory`，包括两者混合
- **THEN** Driver 返回 `inventory_mode=runtime`，且不得把 `source=file` 判为磁盘降级

#### Scenario: 磁盘降级或空数组
- **WHEN** 全部记录缺少 source 且符合固化磁盘形态，或 `files` 为空
- **THEN** Driver 返回 `inventory_mode=disk_fallback` 和 degraded 结果，不宣称完整 Provider 快照

#### Scenario: 来源形态混合或未知
- **WHEN** 同一非空响应混合带/不带 source、出现未知 source，或不符合任一固化形态
- **THEN** `contract_valid=false`、mode 为空，Driver 不按多数记录猜测

#### Scenario: 上游包含敏感或未知字段
- **WHEN** 记录包含 status message、路径、项目 ID、认证索引、Token、account/name 或新增未知字段
- **THEN** Driver丢弃这些字段原文；仅Antigravity file模式的批准错误标识符可转为固定安全枚举。合法白名单字段仍可解析，禁用原文不进入DTO、日志、指标、Trace或错误

#### Scenario: Safe runtime projection without changing inventory truth
- **WHEN** Antigravity file账号有明确active/disabled/unavailable及可选错误语义
- **THEN** 仅附加file_active/file_disabled/file_error/file_unavailable/file_unknown与固定auth子原因；缺字段为未证明，不改变现有basic_status、completeness、promotion或Duplicate eligibility

#### Scenario: Unknown error text does not poison collection
- **WHEN** status_message过大、结构过深或未匹配白名单
- **THEN** 认证原因保守为other，原文丢弃；不因扩展解析破坏原合法Inventory采集，也不调用新端点

## Planning Status

2026-09-07：原三项契约已APPROVED；P-READ也已获用户确认Final APPROVED，**本轮提交已批准Architecture Contract；生产实现仍暂停**。本文件替代上一版“规划完成即可进入实施”的说明。OpenSpec artifacts齐全不代表架构批准、代码完成或验收通过。

## Revision Outcome

- Duplicate current membership与historical evidence involvement分开；History新增独立read query，旧instance_id filter不变，不复制history truth。
- Provider snapshot freshness与latest health正交，保留fresh+degraded；明确不修改Duplicate Ownership eligibility。
- 已采用用户Architecture decision：source v1 numeric JSON→Go int64→persistence不变；Control/Web所有read/candidate/detail/write/generated TS/state统一decimal string。
- Topology移除候选submit、bind/rebind/unbind和expected_binding_id职责；当前没有独立Binding管理UI，不创建或链接虚构入口。
- 本change保持0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。独立安全函数、Provider完整并集、缺state not-yet-observed和ACL/Up/Down已定义并获Final Approval；本轮没有创建migration或实现读取。
- Breaking forward correction明确记录；只确认源码内消费者，仓库外兼容承诺仍需发布前清点。

## Self Review

以下逐项区分**修订后契约**与**当前未实施代码事实**。

| # | 问题 | 回答 |
| --- | --- | --- |
| 1 | B从current membership删除后能否看到RESOLVED history？ | 新契约可以：evidence EXISTS；D2要求A/B History均包含，B current不包含。生产新history query尚未实现。 |
| 2 | 是否新增第二套duplicate membership history truth？ | 否；直接查既有append-only evidence，不建表、不复制membership。 |
| 3 | Fresh complete snapshot + latest degraded能否同时表达？ | 可以；snapshot/health两个结构、badge和时间，P2明确验收。 |
| 4 | 是否修改Duplicate Ownership eligibility？ | 否；health_degraded仍与eligibility独立，P7禁止UI/read变更改变ownership。 |
| 5 | Account ID >2^53−1是否可无损round-trip？ | 新契约要求四个边界值全部无损，I1/I2覆盖真实生成客户端链路；本轮未实施或运行端到端测试。 |
| 6 | TS中是否仍存在Gateway Account ID number？ | **当前生产generated TS仍存在number**，因用户禁止本轮实现而保留；修订契约禁止，列为入口暴露前必须修复的兼容性前置任务，不报告已消除。 |
| 7 | 是否存在Number/parseInt转换？ | 新契约禁止Number/parseInt/parseFloat/一元+及numeric JSON中转。当前生成客户端仍用JSON.parse读取numeric wire值，本轮未修代码，I6要求实施后专项检查所有identity路径。 |
| 8 | read/write是否使用同一identity representation？ | 修订后Control/Web全部string，包括candidate/detail/嵌套response；source v1 numeric属于用户明确保留的Go-only边界，不是Web双重契约。现有生产read/write numeric尚未改。 |
| 9 | Topology是否仍read-only？ | 是；不导入或发mutation、不新建管理UI；existing Binding transport fix单独列为compatibility prerequisite。 |
| 10 | migration决策是什么？ | 0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。本轮没有创建或执行migration。 |
| 11 | Provider 0-account时是否仍可见？ | 新契约可以；集合为当前Node监控策略Providers与全部held provider states的并集，不依赖account rows；P-READ A/F/G覆盖0 account和缺state。尚未实施。 |
| 12 | health_scheduled_at/reason是否来自安全read contract？ | 是；九字段独立SECURITY DEFINER函数明确返回最新health时间、bool和既有固定reason枚举，不读raw poll。当前生产函数尚未创建。 |
| 13 | runtime是否仍无provider_states direct SELECT？ | 是；设计仅授予新函数EXECUTE，不扩大表ACL，P-READ D要求直接SELECT permission denied。本轮未改权限或运行DB测试。 |
| 14 | 是否只允许query-access而非persistence migration？ | 是；Up仅新函数/owner/revoke/grant，Down仅DROP该uuid函数；禁止新表/列/索引/materialized view及修改既有account query或数据，P-READ K验收。 |

## Validation

- `openspec validate add-node-centric-topology-ui --type change --strict --no-interactive`：通过；只验证规划格式和结构。
- `git diff --check`：通过。另以git diff --cached --check检查暂存的完整Architecture Contract，文档内容经人工审阅与OpenSpec strict检查。
- 未运行业务实现测试、generate、build、迁移、数据库操作、容器或部署；本轮仅提交Architecture Contract文档；未修改生产OpenAPI/生成物/Go/TS/SQL。

## Git Scope

本次提交仅包含本change目录的Architecture Contract及OpenSpec元数据，不包含实现或migration。提交前已同步proposal/design/tasks与本文件的Final APPROVED状态，仅勾选两项已完成的架构评审任务，所有生产任务仍未执行。

提交前此目录为untracked；暂存后使用git diff --cached --check核验实际提交内容。既有三个规范文件修改`.agents/skills/openspec-apply-change/SKILL.md`、`AGENTS.md`、`openspec/config.yaml`不在提交范围，本轮未触碰。已知旧全仓失败仅作为未来实施基线参考，未在本轮重跑或豁免。

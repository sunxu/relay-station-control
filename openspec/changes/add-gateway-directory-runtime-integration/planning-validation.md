# Planning and acceptance evidence

状态：Architecture Contract Re-review APPROVED（2026-09-07），P1=0、P2=0；待实施。仅创建OpenSpec artifacts；以下实现验收均未执行。未更新canonical spec或已归档Topology change，未改生产代码/migration/runtime配置。

## Acceptance matrix

| ID | Fixture / command family | 必须证明的断言 | 当前结果 |
| --- | --- | --- | --- |
| R1 | runtime配置与fake HTTP/HTTPS计数 | disabled零请求/零run；invalid enabled配置fail closed | 待实施 |
| R2 | 真实PG+进程启动/退出 | 固定cadence/budgets、受限pool、停止领取与bounded shutdown | 待实施 |
| R7 | 多Gateway慢/失败前项、跨槽及取消fixture | runtime无重复ScheduleTick；后项继续、按当前DB槽调度、同槽去重、不回填 | 待实施 |
| R3 | 多进程/lease/unknown commit | 单槽唯一、旧fence不得finalize、restart可恢复 | 待实施 |
| R4 | 真实PG+慢header/body/finalize、timeout收尾 | 独立5s attempt；失败写入另有最多5s且不延长成功/lease；取消/unknown commit恢复 | 待实施 |
| R5 | A NULL/B正常、多tick、补填与调度双顺序 | A零run/零请求且仍可补填；B成功；无NULL新run | 待实施 |
| R6 | A非NULL但凭据失败/B正常、历史NULL run | A正常durable failure，B成功；历史保留且reconcile，不绕过补填条件 | 待实施 |
| T1 | HTTP与自签名/未知CA/过期/错误主机名HTTPS | 无许可列表均可采集；Node同策略、范围外客户端不变；public Directory拒绝 | 待实施 |
| T2 | 无效URL/非HTTP协议、TLS握手失败、双协议redirect | 正常失败、不降级、不转发token、不刷新freshness | 待实施 |
| T3 | reader token缺失/错误/轮换 | 失败不刷新freshness；同引用恢复后成功 | 待实施 |
| A1 | registrar+空历史真实PG | UUID不变，合法补全；reference精确no-op不新增audit | 待实施 |
| A2 | failed/running/succeeded run、snapshot、observation、Binding history | 任一历史均拒绝真实配置变更；无清表绕过 | 待实施 |
| A3 | 两个补全及首次schedule/Binding竞争 | NULL条件CAS、串行化、endpoint不变且不覆盖已有reference | 待实施 |
| A4 | runtime/PUBLIC/registrar与audit失败 | 最小ACL、成功audit同事务、失败完整rollback，无敏感值 | 待实施 |
| A5 | migration Up/Down/应用rollback | 不删除资产/审计/采集数据，保留forward兼容 | 待实施 |
| E1 | 真实source v1 changed/unchanged/失败/恢复 | int64无损、去重、last_success_received_at只随成功更新 | 待实施 |
| E2 | 已有HTTP候选/bind/read | decimal string精确ID、BOUND/resolved/current，未action不自动绑定 | 待实施 |
| E3 | Directory暂停/故障与数据面请求 | Binding保留、stale→unknown、恢复resolved，数据面独立 | 待实施 |
| O1 | metrics/logs/配置dump canary | 无token/reference/账号身份/原始响应输出 | 待实施 |
| N1 | Node HTTP/各类不可信HTTPS合成fixture | 无许可及证书检查；健康/账号/版本观察遵守固定接口 | 待实施 |
| N2 | 普通DNS变化、混合结果、特殊地址fixture | 不按地址类别或DNS重绑定拒绝；不访问真实元数据 | 待实施 |
| N3 | 旧DNS/CIDR/CA变量缺失/非法/残留 | 不阻止启动、不读取CA、不影响新策略；rollback前恢复旧条件 | 待实施 |
| N4 | Secret/无代理/redirect/预算/响应及范围外配置 | 原有认证和数据验证保持，入站/数据面不变 | 待实施 |

## Planning validation

仅运行本change strict、全部OpenSpec strict和git diff检查。change strict通过；全部strict为14 passed / 0 failed；diff检查通过，新增文件另行检查行尾空白。不运行昂贵构建、浏览器或PG业务验收。真实本地账号、凭据、配置文件内容不进入本change。实现任务全部保持未完成；架构复审结果见下文；实现验收仍未执行。

## Current transport decision

用户明确要求移除HTTP origin白名单和HTTPS证书验证，替换此前HTTPS-only及精确origin opt-in草案。Directory直接支持HTTP/HTTPS；不新增目标许可或证书配置，只保留enabled和secret mapping。HTTPS不验证证书链、有效期或主机名，作用于Control的所有Gateway/Node管理出站transport。HTTP明文与HTTPS不认证服务端的影响已记录在design。

URL解析、既有资产登记契约、专用token、响应schema/identity验证、预算与禁止redirect不变；Node客户端采用相同传输策略。MODIFIED Requirements覆盖固定fetch契约；canonical与归档内容不改。资产只补填NULL reader reference，复用Gateway行锁并保留原子audit/ACL。

本轮仅文档修订，未修改或启用生产transport。用户已决定上述安全边界，整份change已完成Architecture Contract Re-review；全部实现验收保持未完成。

## Scope expansion

用户明确批准扩大到Control所有Gateway/Node管理出站调用。新增统一能力与Node MODIFIED requirements，覆盖原SSRF、TLS及回滚陈述；Node版本仅读取现有响应头，不增加接口。旧网络许可/CA变量退役；两类客户端策略一致，范围外连接不改。仅规划文档，验收尚未运行。

## Architecture review revision

修订P1：现有ExecuteAttempt透传ctx且client无Timeout，设计现明确每次claim后5s执行截止与独立最多5s失败收尾，不能声称原实现已满足。修订P2：共同ScheduleCurrent在Gateway行锁内检查NULL，返回no-work；同步修正原先调度先行必然产生run的错误假设。R4–R6为新增验收，尚未执行。修订随后经主评审与独立复核确认，见下文；不代表实现完成。

## Architecture Contract Re-review result

2026-09-07：APPROVED，P1=0、P2=0。主Agent及独立review_transport_contract复核一致：5s attempt/有界失败收尾、NULL no-work/共同行锁/其他Gateway继续采集契约闭合。用户随后授权继续提交架构基线。tasks仅1.1、1.2、6.1评审项完成，全部实现与发布验收仍待执行。

提交前清理三份文档EOF多余空行；重新执行change strict、all strict及working-tree/cached diff检查。未运行业务测试、未修改生产代码、未部署或归档。

## Implementation simplification review

用户授权在当前change精简并amend架构提交。只读代码确认WorkOnce→RunGatewayOnce→ScheduleCurrent已覆盖逐Gateway调度，故runtime改为ReconcileTick→WorkOnce。针对性核对：共同NULL行锁入口保留、reconcile先行、当前DB槽/不回填和取消预算不变；R7明确慢/失败前项及后续进度验收，尚待实施证明。标准拨号、SQL模板、fixture复用和Runbook合并属于实现组织调整；原P1/P2修复与全部验收保留。

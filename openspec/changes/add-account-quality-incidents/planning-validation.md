# Planning validation

## Baseline
2026-09-08 main d0090db，Control工作树干净。现有00020/21/22、安全Inventory/auth与canonical Quality/Topology/Jobs已检查。事件schema、collector、retention均复用不改。active-only是用户允许的MVP分支，不计算或承诺recovered。

## Acceptance matrix
| ID | Required fixture / assertion | Result |
|---|---|---|
| I1 | 3 auth active/2不生成、四类独立、unknown/success不计、success不解除active | pending |
| I2 | current Inventory、NULL/event-only/otherNode排除、超过101账号、15m含下边界/future排除 | pending |
| I3 | provider/failure filters、time DESC/key ASC/class ASC与cursor分页 | pending |
| I4 | API400/401/403/404/503/GET only/no-store、完整绑定/NULL | pending |
| I5 | runtime EXECUTE/direct SELECT拒绝、STABLE/owner/path/PUBLIC、DownUp原数据/函数/index不变 | pending |
| U1 | loading/empty/unavailable/populated/Active与filters/pages | pending |
| U2 | Account打开既有History、Node取消/迟到隔离、no mutation | pending |
| P1 | 100accounts10000events active/provider/failure/pagination，单DB query/latency | pending |
| V1 | targeted PG/API/race、frontend/typecheck/build、make test build | pending |
| V2 | strict/diffcheck/runbook/commits/git status | pending |

## Scope review
Incident只描述重复失败，History为请求证据，Quality为窗口表现。无新增truth、表/index/worker、事件字段、采集逻辑、lifecycle/Binding/Duplicate改变或自动行为。详细设计见design.md。

# Planning validation

## Baseline

2026-09-08 main `9e22c21`，工作树干净。已读canonical account-request-quality/node-centric-topology-ui，migration00020/21和Inventory00008，现有11 event字段及account index全部复用。Scope仅control本地实现，无部署/Node/collector更改。

## Acceptance matrix

| ID | Fixture / assertion | Evidence |
|---|---|---|
| H1 | Inventory存在有event/无eventempty；不存在和event-only404；Inventory/DB失败503 | 待验证 |
| H2 | NULL身份、其它账号、其它Node排除；固定DB7天含下边界/排除future | 待验证 |
| H3 | time DESC/hash DESC、同timestamp、keyset25/100、无offset | 待验证 |
| H4 | cursor Node/account mismatch400、invalidlimit/hash/time、401/403/GET only、no-store | 待验证 |
| H5 | runtime EXECUTE、PUBLIC revoke/direct SELECT denied、Down/Up数据/index/旧函数不变 | 待验证 |
| U1 | unselected/loading/populated/success/failed/empty/unavailable与404 | 待验证 |
| U2 | pagination/换账号/Node清除取消迟到隔离/no mutation | 待验证 |
| P1 | 1账号10000events首25/next/100，one query/latency/existing index | 待验证 |
| V1 | targeted PG/API/race、frontend tests/typecheck/build、make test build | 待验证 |
| V2 | change/all strict、diffcheck、docs/commits/status | 待验证 |

## Frozen invariants

Inventory集合与identity不变；质量只聚合、历史只解释；不修改classification、lifecycle、Binding、Duplicate、CLIProxy、collector、queue、event schema/retention/taxonomy。无新index/table/cache/rollup，未扩scope到raw/详情页/统计token/动作/quota/inspection/automation/Prometheus/Grafana。

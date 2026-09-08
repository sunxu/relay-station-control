# Planning and validation

## Baseline

2026-09-08 main `c2e3cfa`，开始前git status --short为空。既有quality8统计字段、两窗口与NULL语义已由canonical spec及00020确认。旧change完成全套验证，见archive/2026-09-08-add-account-request-quality-monitoring/final-release-validation.md；不将旧PASS当本change final PASS。

## Acceptance matrix

| ID | Fixture / assertion | Command / result |
|---|---|---|
| Q1 | 有请求good，95/94/80/79边界 | 待实现/验证 |
| Q2 | Inventory有账号无event→0/null/unknown | 待实现/验证 |
| Q3 | NULL身份和event-only账号不产生/污染行 | 待实现/验证 |
| Q4 | provider/quality filters；跨101 Inventory chunk后命中 | 待实现/验证 |
| Q5 | keyset顺序、limit、cursor绑定filters | 待实现/验证 |
| Q6 | 401/403/400/404/503及恢复，不伪造empty | 待实现/验证 |
| Q7 | runtime EXECUTE，表SELECT denied，PUBLIC revoke，down/up | 待实现/验证 |
| U1 | loading/populated/unknown/empty/unavailable | 待实现/验证 |
| U2 | 15m/1h/provider/quality/page/Node切换/no mutation | 待实现/验证 |
| P1 | 100accounts 两窗口与filters，DB往返数和latency | 待实现/验证 |
| V1 | targeted Go/PG、frontend test/typecheck/build | 待验证 |
| V2 | make test build、race、strict/all、diff check | 待验证 |

## Self review

完成后逐项补证：1单账号视角；2无请求仍显示；3unknown/unavailable分开；4无网络N+1并披露内部调用；5unresolved不污染；6classification仅read；7Inventory/lifecycle未改；8无mutation；9无history；10无Prometheus/Grafana；11APIbounded；12分页deterministic；13UI只读；14最小change且无采集/事件存储/retention/taxonomy变更。

## Test Contract Coverage Review

状态：`PASS`。本矩阵在产品实现前冻结；本 corrective 的 Browser 为 `NO`，仅保留一个已有 Browser-owned duplicate-submit proof 的 owner 记录，不在本轮扩展或运行 Browser。

| Requirement | Risk | Owning layer | Concrete proof | Input/state setup | Expected result | Negative / race cases | Browser required |
|---|---|---|---|---|---|---|---|
| Node-first lock order | lifecycle/dispatch race | PostgreSQL/store | existing Node-first race tests plus focused admission tests | Node row and prepared operation | Node lock precedes admission decision | Retire-first / dispatch-first / unrelated Node | NO |
| lifecycle freshness | stale resolver authorizes dispatch | PostgreSQL/store | focused stale lifecycle admission test | resolver-positive, durable Node retired | failed `node_retired`, no native work | state changes before/after admission | NO |
| monitoring freshness | stale monitoring eligibility | PostgreSQL/store | focused monitoring admission test | current activation cancelled/expired | failed `node_monitoring_ineligible` | monitoring transition races admission | NO |
| inventory-read capability | stale capability snapshot | PostgreSQL/store | focused capability admission test | capability absent at durable boundary | failed `node_management_unavailable` | resolver says allowed | NO |
| Provider policy freshness | stale active policy | PostgreSQL/store | focused policy admission test | provider becomes inactive/out-of-scope | failed `unsupported_provider` | policy transition races admission | NO |
| same-account serialization | duplicate account mutation | PostgreSQL/store | existing same-account blocker matrix | dispatched/outcome_unknown peer | failed `account_operation_in_progress` | same-account override and unrelated account | NO |
| dispatch prepared transition | at-most-once native mutation | PostgreSQL/store | admission transition integration tests | prepared operation | exactly one committed dispatch admission | concurrent prepared calls | NO |
| noop prepared transition | no-op terminalization race | PostgreSQL/store | focused noop admission tests | fresh snapshot already desired | one `remote_noop` receipt, no native mutation | stale eligibility and concurrent noop | NO |
| concurrent noop replay convergence | state mismatch leakage | store/service/API | two concurrent exact same-command Execute/HTTP tests | both observe prepared/no-op | both return same terminal result; one op/receipt/audit | second caller after first commit | NO |
| Retire blocker exact error code | API contract drift | API/OpenAPI | focused Retire blocker HTTP test | same-Node dispatched/unresolved op | 409 `account_operation_in_progress` | lifecycle override path remains distinct | NO |
| Replace blocker exact error code | API contract drift | API/OpenAPI | focused Replace blocker HTTP test | same-Node unresolved op | 409 `account_operation_in_progress` | no generic `conflict` | NO |
| duplicate-submit Browser fact | UI duplicate request | Browser | existing retained Duplicate Submit flow | repeated user submit | one UI HTTP mutation | backend at-most-once remains lower-layer proof | YES (existing only) |
| native at-most-once | remote side effect duplication | store/service/adapter | counting native fake plus concurrent service/store tests | prepared dispatch/no-op | native mutation count bounded | replay/unknown never redispatch | NO |

`TEST_COVERAGE_CONTRACT_GAP: NONE`。

### Readiness checklist

- [x] 每个 frozen MUST/MUST NOT 有 owning layer 与 concrete proof。
- [x] lifecycle、monitoring、capability、Provider policy 都包含 stale-before-admission 场景。
- [x] durable operation state 与 native side effects 的边界明确。
- [x] exact error code/status 不使用宽松接受。
- [x] concurrent noop 的 store/service/API owner 明确，Browser 不承担数据库并发证明。
- [x] historical migrations 不修改；new SQL 仅由新 forward migration 提供。

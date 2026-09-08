# Planning Validation

2026-09-09，baseline f62c64c，起始工作树干净。用户明确接受合并到Topology且不保留旧/account-inventory链接。仅control；不push/deploy/archive。

## Acceptance Matrix

| 范围 | 断言 | 结果 |
| --- | --- | --- |
| 路由 | Topology唯一入口；旧路径无alias/redirect/旧页面 | 待验收 |
| 筛选 | email/basicStatus/provider/lifecycle/window/quality/page size显式提交且reset cursor | 待验收 |
| Node | 深链接/选择自动默认首页，StrictMode一次，无Node不查，慢响应与popstate隔离 | 待验收 |
| 分页 | 25/50/100，上一页/下一页，body-only email/cursor | 待验收 |
| 诊断 | capacity手动读取、超限原因、失败/401 | 待验收 |
| 观察 | Unknown/Unavailable独立，recent/详情/Incident保持，无mutation | 待验收 |
| 边界 | 无API/DB/generated/collector修改 | 待验收 |

## Baseline

已读现有canonical、页面及API wrapper；已有共同accountList读取无需重新开发SQL/API。旧Page默认50与Topology默认25统一为25（均符合既有bounded API）。先strict验证后实施。

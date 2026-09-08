# Planning Validation

2026-09-09，baseline f62c64c，起始工作树干净。用户明确接受合并到Topology且不保留旧/account-inventory链接。仅control；不push/deploy/archive。

## Acceptance Matrix

| 范围 | 断言 | 结果 |
| --- | --- | --- |
| 路由 | Topology唯一入口；旧路径无alias/redirect/旧页面 | PASS |
| 筛选 | email/basicStatus/provider/lifecycle/window/quality/page size显式提交且reset cursor | PASS |
| Node | 深链接/选择自动默认首页，StrictMode一次，无Node不查，慢响应与popstate隔离 | PASS |
| 分页 | 25/50/100，上一页/下一页，body-only email/cursor | PASS |
| 诊断 | capacity手动读取、超限原因、失败/401 | PASS |
| 观察 | Unknown/Unavailable独立，recent/详情/Incident保持，无mutation | PASS |
| 边界 | 无API/DB/generated/collector修改 | PASS |

## Baseline

已读现有canonical、页面及API wrapper；已有共同accountList读取无需重新开发SQL/API。旧Page默认50与Topology默认25统一为25（均符合既有bounded API）。先strict验证后实施。

## Final Evidence

规划strict PASS后本地提交 `9188e22`；实现提交 `6e09a50`。仅control前端及现行runbook；未修改internal、OpenAPI、migration、generated、collector、account identity或数据面。移除两个独立页面文件、旧专用样式和重复useTopologyAccountList，复用AccountList与AccountDetailsDrawer；UI实现与测试合计净减441行。

| 验收 | 实现/fixture/断言 | 结果 |
| --- | --- | --- |
| 路由 | App.account-inventory.test：无菜单，旧裸路径/带instance_id不渲染旧页或Topology，无alias/redirect；现有management fallback | 3 PASS |
| Node与筛选 | TopologyConsolidation.test：StrictMode深链接一次、默认present/15m/25；输入email/provider不请求，提交规范化body；无Node不查 | PASS |
| 请求隔离 | 同文件通过未完成A/B Promise、popstate切Node及清空Node验证AbortSignal，旧结果不可覆盖 | PASS |
| 分页 | basicStatus、50/100显式提交；next→next→previous使用准确cursor；修改页大小回首页 | PASS |
| 错误恢复 | 400/403/404/409/503逐类断言，不伪装Empty、不自动重试，手动重试成功 | PASS |
| 容量 | AccountInventoryCapacity.test：手动刷新、loading、9-of-8原因、全部预算/评估时间、ready/disabled、成功→503隐藏旧结果→恢复、401退出且不泄露raw error | 4 PASS |
| 质量/详情/关联 | 原TopologyView 23项保留Unknown/Empty/Unavailable、Provider独立失败、Bound/Unbound/context、历史涉及、质量过滤、详情和Incident联动 | 23 PASS |
| HTTP边界 | account-list-transport.test：25/50/100、clear lifecycle字段省略；email/cursor只在POST body，CSRF/no-store/signal不变，URL/history/localStorage/sessionStorage不变 | 4 PASS |

命令：

```sh
cd web
npm test -- --run src/App.account-inventory.test.tsx src/App.test.tsx
npm test -- --run src/pages/TopologyView.test.tsx
npm test -- --run src/pages/TopologyConsolidation.test.tsx src/pages/AccountInventoryCapacity.test.tsx
npm test -- --run src/api/account-list-transport.test.ts
npm run typecheck
cd ..
make test build
openspec validate consolidate-account-inventory-into-topology --type change --strict --no-interactive
openspec validate --all --strict
git diff --check
```

最终make test build退出0：Go测试PASS（按现有测试环境，未单独启用真实PG专项），frontend 20 files / 125 tests PASS，typecheck PASS，Web生产构建与Go构建PASS。日志 `/private/tmp/consolidate-account-final-make.log`。Go module stat cache写权限出现非致命警告，命令仍退出0；jsdom伪元素getComputedStyle能力警告不影响断言。首次新增测试因Ant Design两个中文字按钮自动插入空格未正确匹配，调整测试按可访问名称空白匹配后通过，未改变产品行为来规避测试。

## Self Review and Delivery

独立路由/整体review在发现实际TopologyPage未传capacity客户端后要求修正；主Agent已补接generatedAccountInventoryApi并重新核对。最终独立review PASS。容量在无Node时也可用；不增加新查询/计算。Provider精确输入不依赖Provider读取成功，Provider自身面板仍显示Unavailable。

筛选只留内存；Node变化/离开取消旧请求；查询按钮在任何页均重置首页。默认present且可查看全部/缺失；Unknown不等于Unavailable；Inventory状态不受质量影响。只有一个账号工作入口，不新增业务mutation。浏览器旧/account-inventory有意失效，HTTP /api/account-inventory/*契约不受影响。归档历史与canonical spec暂不改，待正式archive同步delta。

change strict PASS、all strict 18/18 PASS、diff check PASS。5/5任务完成。本地提交，未push、未deploy、未archive；当前运行环境仍为合并前版本，等待Architecture + Implementation Final Review。

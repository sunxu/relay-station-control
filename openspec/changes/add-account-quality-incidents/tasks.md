## 1. Contract
- [x] 1.1 检查Quality/History/Inventory/事件/Jobs及读取惯例，冻结active-only和字段窗口。
- [x] 1.2 proposal/design/spec/planning strict通过后开始实施。
## 2. Backend
- [x] 2.1 00023 readonly function/store，完整Inventory集合、15m四类阈值与混合keyset。
- [x] 2.2 GET OpenAPI/generated/wiring/cursor/auth/errors及tools契约清单。
- [x] 2.3 PG阈值/四类/排除/时间/过滤/分页/第101账号之后/失败测试。
- [x] 2.4 PG ACL/direct SELECT/DownUp及100accounts10000events单query性能。
- [x] 2.5 API401/403/GET/404/503/cursor绑定/limit/显式NULL测试。
## 3. Web
- [x] 3.1 Incidents七列、filters/pages与现有History入口，无mutation。
- [x] 3.2 四态/active/filter/page/History/Node取消隔离前端测试。
## 4. Validation
- [x] 4.1 targeted PG/API及relevant race通过。
- [x] 4.2 frontend tests/typecheck/build与make test build通过。
- [x] 4.3 change/all strict、diffcheck、runbook/evidence reconciliation。
- [x] 4.4 分阶段本地commit、git status、等待Final Review；不push/deploy/archive。

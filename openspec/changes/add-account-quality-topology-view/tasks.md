## 1. Contract

- [x] 1.1 检查baseline及既有Inventory/Quality/Topology/auth契约，明确非目标。
- [x] 1.2 完成design/spec/planning-validation，实施前strict validate。

## 2. Read API

- [x] 2.1 最小readonly composition function及store adapter，复用现有Inventory和Quality函数。
- [x] 2.2 GET OpenAPI/handler/wiring，bounded cursor绑定filters、super_admin、503。
- [x] 2.3 make generate更新Go/TS，无手改generated code。
- [x] 2.4 真实PG覆盖classification、unknown/unresolved、filter/page/order、ACL/down/up/error。
- [x] 2.5 HTTP测试覆盖defaults/validation/401/403/503/cursor与response。

## 3. Topology UI

- [x] 3.1 Node detail Account Quality表格、window/provider/quality与pagination。
- [x] 3.2 loading/empty/unknown/unavailable、取消与Node切换、无mutation测试。

## 4. Validation

- [x] 4.1 100accounts两窗口及filters的query count/latency evidence。
- [x] 4.2 targeted Go/PG、frontend unit/typecheck/build。
- [x] 4.3 make test build、relevant race、change/all OpenSpec strict、git diff --check。
- [x] 4.4 runbook/planning-validation证据与14项self-review，本地分阶段commit、git status；不push/deploy/archive。

## 1. Contract

- [x] 1.1 核对现有11字段、Inventory gate、索引、API/auth/UI；记录基线与非目标。
- [x] 1.2 完成design/spec/planning-validation并在实施前strict通过。

## 2. Backend

- [x] 2.1 新00022 readonly function和store adapter，单query gate+7天keyset；真实PG验收。
- [x] 2.2 GET OpenAPI/handler/wiring及generated Go/TS；HTTP契约测试。
- [x] 2.3 PG覆盖当前账号/不存在404/empty/NULL与跨账号Node隔离、7天边界、同timestamp排序与分页。
- [x] 2.4 runtime ACL/direct SELECT拒绝、Down/Up保留表/index/data、失败与取消专项。
- [x] 2.5 API覆盖cursor绑定/limit/401/403/GET only/503/404/显式NULL与无hash字段。

## 3. UI

- [x] 3.1 Account Quality View History、独立六列表与分页；选择identity=account_key。
- [x] 3.2 五态、换账号/Node取消隔离、no mutation；前端专项。

## 4. Validation

- [x] 4.1 10000events首25/下一页/limit100，单query+latency+existing index evidence。
- [x] 4.2 targeted Go/PG/API/race和frontend tests/typecheck/build通过。
- [x] 4.3 make test build、change/all strict、git diff --check通过。
- [x] 4.4 runbook/planning-validation reconciliation、本地分阶段commit及git status，等待Final Review。

## 1. Contract

- [x] 1.1 完成本地CPAMP/Control调查和OpenSpec设计，strict通过后实施

## 2. Read Model and API

- [x] 2.1 新additive v3安全函数组合Inventory/Quality/recent，PG验证过滤、10条/7天/identity/ACL/Down-Up
- [x] 2.2 additive OpenAPI/Go read与cursor参数，生成客户端并通过HTTP授权/400/503/兼容测试

## 3. Shared UI

- [x] 3.1 共享账号列表接入两入口，保留深链/手动筛选/容量与分页，专项前端测试通过
- [x] 3.2 最近请求条与History/采集详情抽屉，验证真实success/failure/空/不可用与Node隔离

## 4. Validation

- [x] 4.1 真实PG性能100账号/10000events及相关race记录query count/latency
- [x] 4.2 frontend tests/typecheck/build、make test build、strict/all/diff通过并记录证据与runbook
- [x] 4.3 独立审查并修复普通问题，对账任务与git状态，等待Final Review不push/deploy/archive

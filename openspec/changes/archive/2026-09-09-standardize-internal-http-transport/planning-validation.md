# Planning Validation

## Current phase

Implementation underway。Control、Gateway、Ops 的当前版本契约与实现已完成本地修改；未 commit、未 push、未 deploy。

## Review checklist

- [x] 已列出仍要求 internal TLS 的 canonical specs、代码、Compose/ops 配置。
- [x] 已证明内部路由不经公网暴露，Directory 认证和精确 method/path 授权保留。
- [x] 已证明 browser/external ingress HTTPS 与 Secure Cookie 不受影响。
- [x] 已确认不修改数据面调度、Gateway Account/Group routing 或 CLIProxyAPI credential/scheduler。
- [x] 已确认默认 0 database migration；删除 TLS proxy/cert 属部署变更，不删除历史数据。

## Evidence commands

实施阶段按仓库运行受影响的 Gateway/Control/deployment acceptance；最终运行相关 OpenSpec strict validation 与 `git diff --check`。本阶段只记录本地静态调查，不宣称实现测试通过。


## Control implementation evidence

- HTTP-only validator: `internal/drivers/gatewaydirectory/client.go`, `internal/drivers/cliproxyapi/target_policy.go`.
- HTTPS endpoint rejection occurs during client construction; no request is issued.
- Targeted tests: `go test ./internal/drivers/gatewaydirectory ./internal/drivers/cliproxyapi -count=1` PASS.
- `openspec validate standardize-internal-http-transport --type change --strict --no-interactive` PASS; `git diff --check` PASS.

## Gateway implementation evidence

- `openspec/config.yaml` 与 `openspec/specs/api-account-directory/spec.md` 已改为 restricted/private network 上的 HTTP-only Directory；保留独立 service token、精确 GET 和 public `/internal/v1/*` 拒绝。
- Targeted tests: `go test ./internal/directory ./internal/server/routes -count=1` PASS（在 `gateway/backend`）。Gateway 未修改数据面代码。
- `openspec validate --all --strict` PASS（3/3）；`git diff --check` PASS。

## Ops implementation evidence

- 系统设计、概要设计、ADR-0002、部署 README 和 DEPLOYMENT 已统一 Control-managed management-plane internal HTTP，并保留 private network、public ingress isolation、browser HTTPS/Secure Cookie。明确排除 Gateway→Relay Node AI、Gateway generic Account/upstream、Sub2API `base_url` 和 Gateway generic HTTP client。
- `python3 -m unittest discover -s dev -p 'test_http_topology.py' -v` PASS（3 tests，1 container test skipped）。
- 全量 `python3 -m unittest discover -s dev -v` 仍有既有 `operation_state` 临时路径 fixture failures；该失败与本 change 无关，本 change 不修改该测试。

## Cross-repository validation limits

- Control `make test build` PASS；Control targeted transport race PASS。
- Gateway 全量 `go test ./...` 因 `/Volumes/DevRAM` 磁盘空间不足而未完成；Directory targeted tests 已 PASS。
- 尚未宣称完整跨仓 acceptance PASS；待人工 Implementation Final Review 处理上述非阻塞环境/既有 fixture 问题。

## Final Review reconciliation

- Architecture Final Review：PASS；Implementation Final Review：PASS。
- P0/P1 = 0；本轮 P2 文档问题已清理。
- Control targeted transport/race 与 `make test build`：PASS。
- Gateway Directory/routes targeted：PASS；Gateway full `go test ./...` 因 `/Volumes/DevRAM` 空间不足未完成，不宣称 full PASS。
- Ops HTTP topology tests：PASS；Ops full unittest 存在既有 `operation_state` fixture failure，不宣称 full PASS。
- Gateway `backend/**`、`frontend/**` 零修改；Node 产品代码零修改。
- 当前无产品 blocker；未 deploy、未 archive。

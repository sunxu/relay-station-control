# Account JSON Import Validation

IMPLEMENTATION = COMPLETE / PASS
OWNING_ACCEPTANCE = PASS
OpenSpec archive, push and deployment were not performed.

## Accepted runtime candidate

- Source SHA: `2f3195b256d3d75c0ab5ed2de531d21ac8c4a293`.
- Control local image ID: `sha256:aa9972476111ebf62f3e15abc6fd88823e0431ce5f5e24b8478cc6153a6c70e9`.
- OCI revision matches candidate; running Control image identity verified by
  the repository-owned runtime harness.
- Reused Node source: `0b34a22fcaec392d39f710f3a8418595b491607d`.
- Node local image ID: `sha256:06a2e730ee5420b2f02b77a8e231bb5a384d81d312d6f0d8709b6b5aa94a27a1`.
- Node platform RepoDigest: `sha256:0f968ee25552df8eca55bc3b4bf27ea7d63af1b651513fff44806cb15ad03ce3`.
- Node version/commit observations match the published workflow's full source
  SHA; the Docker image ID and RepoDigest are recorded as distinct identities.
- Browser: Playwright bundled Chromium, approved host execution; no system
  executable override or custom Chromium security flags were introduced.
- Command: `EXPECTED_SHA=<candidate>` with explicit Control image ID and Node
  identities, `deploy/acceptance/runtime/run.sh upload`.

## Proof results

| Gate | Result |
|---|---|
| Full `make test build` in Linux non-root validation container | PASS |
| Frontend unit/component | PASS — 48 files / 367 tests |
| Typecheck / build | PASS |
| RESOURCE_PARITY / REFERENCED_KEY_COMPLETENESS / TRANSLATION_SOURCE_AUDIT | PASS |
| Accounts desktop and import Browser | PASS — 6/6 |
| Real PostgreSQL account operations HTTP integration | PASS |
| Real Control/PostgreSQL/pinned-Node upload Browser | PASS — 1/1 scenario, 3 independent uploads |
| Single JSON identity derived without email input | PASS |
| Sub2API proxy association and Node persisted proxy | PASS |
| Missing batch disabled defaults false; explicit true retained | PASS |
| Independent commands, partial failure and unknown outcome | PASS — component and controlled Browser |
| Node/session/file generation isolation | PASS — focused component proof |
| HMAC byte preservation, exact replay, proxy/disabled conflict | PASS — service and real PostgreSQL HTTP proof |
| Replace Existing disabled rejection | PASS |
| Secret DOM/URL/storage/history/console / runtime artifact scan | PASS |
| Same-origin transport, CSRF, no automatic retry/replace/probe | PASS |
| OpenSpec strict / git diff check | PASS |
| Generated drift | NONE |

Authoritative runtime markers were observed: `SECRET_ARTIFACT_SCAN=PASS`,
`UPLOAD_NEW_BROWSER=PASS`, `UPLOAD_NEW_CONTROL=PASS`,
`UPLOAD_NEW_POSTGRES=PASS`, `UPLOAD_NEW_NODE=PASS`,
`UPLOAD_BATCH_PROXY_DISABLED=PASS`, `UPLOAD_NEW_NATIVE_MUTATIONS=3`.
The batch also checks all registry, operation, receipt and audit records and
the actual Node credential files' proxy URL and disabled booleans. No mock
substitutes for the owning Control/database/Node chain.

## Reconciliation and final review

Historical failures and environmental details are retained in
`validation-progress.md`; that file describes the initial candidate, not the
final disposition. Harness corrections aligned Phase 10 Settings/Dashboard
and Accounts locators and replaced regex extraction with structural JSON
parsing. No product contract was weakened to pass acceptance.

The final five additional unit cases cover exact size/count/identity bounds,
native extension preservation, normalized duplicate identities and invalid
batch disabled values. They and this evidence do not change the accepted
production artifact or runtime harness. The full Make run executed all 367
tests with those cases present.

Review checked JSON identity, whole-file rejection, proxy/disabled mapping,
credential non-reflection, partial outcomes, 401 stopping, stale generations,
replay and Replace compatibility, localization, stable interaction test IDs,
and scope. A separate reviewer subagent was unavailable with the required
`gpt-5.6-luna` model; no other model was substituted. The main agent performed
the final source/spec/test reconciliation.

P0 = 0
P1 = 0
P2 = 0

Backend change = narrow reviewed account-command credential validation only.
OpenAPI / database / migration changes = NO.
Gateway / Node source changes = NO.
Real user credential samples were not added to Git.
Acceptance Compose resources were cleaned up.

## 2026-10-12 项目 ID 优先级修订

用户明确决定批量导入使用 project_id：两 project ID 字符串不同不再拒绝。
project_id 缺失、null 或空字符串时继续回退 antigravity_project_id；其余
字段类型校验、代理、disabled、命令/安全契约均保持不变。

- TDD RED：新增不同 project ID 的真实 parser test 在旧逻辑下以
  invalidAccount 失败；其余 37 个 parser tests 通过。
- TDD GREEN：取消一个冲突拒绝分支后，parser 与上传组件 2 文件 / 65 测试通过。
- 全量 make test build：在已有 Linux 非 root 验证工具容器执行，PASS；
  前端 48 文件 / 370 测试，typecheck、前端 build、Go tests/build 均通过。
- OpenSpec strict 与 git diff --check：PASS；generated drift：NONE。
- 用户指定的本地文件仅在内存解析：20 个账号全部 PASS，project_id 优先。
  与旧 parser 在内存中移除冲突备用字段后的完整生成 JSON 对照一致，
  证明代理/凭据/disabled 未受影响。来源文件未改，未复制到仓库，未发起上传。

本轮 production diff 仅删除 parser 的双 project ID 冲突拒绝分支；测试与
planning artifacts 同步。没有 backend/OpenAPI/database/migration/Node/Gateway
修改。此前正式 runtime evidence 保持为原候选的历史证据，不冒充本次新 artifact
验收；本次没有重新运行 Browser/full-chain acceptance、发布镜像或部署。
既有 deploy-v-test-5f27476 tag/image 没有被移动或更新，不包含本轮修订。

## 2026-10-12 只读账号操作指纹密钥兼容修复

用户确认实际环境密钥为 mode=400、owner=65532:65532、可读且 32 字节。
原读取函数将权限不等于 0600 的文件拒绝，导致上传接受前的 service_unavailable。
本次修复仅允许恰好 0400 或 0600，继续拒绝其它权限、非普通文件、符号链接、
缺失、不可读和错误长度；读取不写入、chmod、生成或轮换密钥。

- TDD RED：0400 读取与跨权限 HMAC regression 均在旧代码下失败。
- TDD GREEN：Store/accountadmin tests PASS；覆盖 0400/0600 与
  0000/0200/0500/0700/0640/0644/0660/0666，以及空路径、missing、directory、
  symlink、0/31/33 bytes。相同密钥/凭据在 0600/0400 下的 fingerprint/key version
  完全一致，读取不改权限。
- 真 PostgreSQL HTTP：TestAccountOperationsHTTPPostgreSQLIntegration PASS，
  完整既有上传/替换/receipt/audit/CSRF/replay/conflict 流程使用 0400 fixture。
  0640/0644 和 31 bytes 仍返回 503；registry、operation、receipt 和 native
  mutation 均为零。只使用隔离 fixture，不调用实际部署的账号操作 API。
- 真 PostgreSQL production wiring：
  TestProductionAccountNodeResolverUsesMigratedCapabilitySchema PASS，使用
  0400 fixture 及部署控制的密钥路径，无 request-controlled key path。
- make test build：Linux 非 root 独立工具容器 PASS；前端 48 文件 / 370 测试，
  全量 Go/tool tests、typecheck、前端 build 与 Go build PASS。
- OpenSpec strict / git diff --check：PASS；generated drift：NONE。

实际 production diff 仅扩展一个权限判断，不改变 HMAC、key version、
命令 intent/replay/receipt/audit、数据库 schema 或 API schema。
Ops Secret provisioning 与只读挂载保持不变；Gateway/Node source 不变。
隔离测试资源已清理，不读取/修改/重新生成用户部署的密钥。
本轮未执行 Browser/full-chain artifact acceptance、push、镜像发布或部署；
已有 deploy-v-test-80636c3 不包含本轮修复，需要新 artifact 才能应用。

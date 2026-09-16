# Runtime Acceptance Harness

这是 repo-external acceptance 编排入口，不是 production runtime。`ACCEPTANCE_RUNTIME_DIR` 必须是 repo 外的显式绝对路径；不得把 runtime、storage-state 或证书写入 Git 工作树。

## Modes

`run.sh` 的 focused mode 只负责一个已批准的 Browser acceptance 场景：`upload`、`disable`、`enable-fixture`、`enable`、`replace`、`replace-discovery`、`remove`、`override` 和 `security-replay`。`auth` 只准备认证组合，`startup` 只验证启动与就绪。

`internal` 运行 `internal/store` 生命周期 self-check，不执行 Browser acceptance。为兼容既有调用，`all` 仍保留为 `internal` 的兼容别名，并会明确打印该语义；它不是某个 Phase 的产品 acceptance gate。当前没有内置 `core` runner；Core acceptance 是外部对已独立批准 focused modes 的组合，不能依赖跨 case 的可变业务状态。

模式选择后才校验该模式需要的运行时输入。Playwright spec 可以在没有 acceptance runtime 环境变量的普通 shell 中被 list/discover；实际执行在 fixture/setup 阶段缺少必需变量时，以 `MISSING_REQUIRED_ACCEPTANCE_ENV` 明确快速失败。缺少配置不会被空值、假凭据或静默跳过掩盖。

## Environment contract

调用方必须提供 `EXPECTED_SHA` 对应的 clean candidate source，以及显式不可变的 Node identity：`CONTROL_E2E_NODE_IMAGE`、`CONTROL_E2E_NODE_DIGEST`、`CONTROL_E2E_NODE_VERSION` 和完整的 `CONTROL_E2E_NODE_COMMIT`。可选的 `ACCEPTANCE_IMAGE` 也必须通过 image revision 和 digest/image-ID 校验；不能 fallback 到任意本地 image。`ACCEPTANCE_RUNTIME_DIR` 是必需的 repo-external 运行目录。

runner 生成或导出的 runtime 目录、端口、认证材料、fixture identity、storage-state 和 mode-specific Browser 变量仅用于本次执行；secret 只能写入 repo-external 受保护文件。发现阶段不需要这些执行期变量，执行阶段仍必须 fail closed。

建议使用具体 focused mode 证明单一行为；使用外部组合运行 Core acceptance；使用 `internal` 检查 harness/store 生命周期。不要把 `all` 当作产品 acceptance 的同义词。

## Buildx

`build-image.sh` 为每次构建创建 `/private/tmp` 下的临时 `BUILDX_CONFIG`，验证 `org.opencontainers.image.revision` 和 platform 后退出。它不修改 `~/.docker/buildx`。使用 `EXPECTED_SHA=<candidate> IMAGE=<tag> ./build-image.sh`。

启动前 harness 必须验证 image 的 `org.opencontainers.image.revision` 与当前 expected SHA 完全一致，并绑定已解析的本地 image ID；缺少 label、label 不匹配、image 不存在或显式指定的 stale image 都会 fail closed。正式 candidate 要求 source worktree clean；不会使用 dirty source 构建，也不会 fallback 到任意本地 image。Control 默认 image tag 为 `relay-station/control:acceptance-<short-sha>`，Node 必须由调用方显式提供 `CONTROL_E2E_NODE_IMAGE`、`CONTROL_E2E_NODE_DIGEST`、`CONTROL_E2E_NODE_VERSION` 和完整 `CONTROL_E2E_NODE_COMMIT`。

## Auth

先由现有 acceptance composition 生成 runtime secrets，再运行 `run.sh auth`。bootstrap secret、password、TOTP URI、cookie、CSRF 和 storage-state 都是 Secret；storage-state 只写到 `ACCEPTANCE_STORAGE_STATE` 指定的 repo-external 文件，权限必须为 `0600`。完成后删除 ephemeral runtime；不要删除无关目录。

## Lifecycle

`tools/acceptance/lifecycle-fixture/run.sh` 只包装 test-only fixture 流程。Availability 必须经 production `Reconcile()`，Duplicate 必须经 production `Evaluate()`；fixture 禁止直接调用 `EnqueueTx`。fixture DB、job IDs、payload/hash 和 evidence 不能写入仓库。

## Restart / rollback

调用方负责启动 isolated compose、停止 worker、等待 `pending`/`retry_wait`/running lease 状态、重启同一 DB，并通过 `OLD_REVISION` 显式传入 rollback binary revision。harness 不硬编码任何 Phase 5 SHA；old revision 必须由 runbook/审批提供，并验证 fail-closed 与当前 candidate 恢复。

## Safe evidence and cleanup

允许 evidence：safe UUID、固定状态、attempt/count、固定 error code、bounded timestamps、hash。禁止 webhook/token/signing secret、private key、signed URL、raw sensitive logs、password、TOTP URI、storage-state。所有 compose、browser、DB、证书私钥和 runtime 文件由调用方按 ownership cleanup。
## Recovery runner identity

`run-recovery.sh` is fail-closed: it requires `HEAD` to equal `EXPECTED_SHA`
and the worktree to be clean. It never assembles a temporary candidate from
dirty or untracked files. The Docker image gate verifies the candidate source
revision label; it does not mean that the recovery scenarios run inside that
Docker image. The scenarios run as `go test ./cmd/control` child processes,
which invoke the production `main()` helper from the same clean committed
source. The runner reports both identities explicitly.

The runner provisions isolated PostgreSQL, runs migrations, executes all three
focused recovery tests twice, and removes the compose project, volumes, and
repo-external runtime files after each run.

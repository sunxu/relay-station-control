# Runtime Acceptance Harness

这是 repo-external acceptance 编排入口，不是 production runtime。`ACCEPTANCE_RUNTIME_DIR` 必须是 repo 外的显式绝对路径；不得把 runtime、storage-state 或证书写入 Git 工作树。

## Buildx

`build-image.sh` 为每次构建创建 `/private/tmp` 下的临时 `BUILDX_CONFIG`，验证 `org.opencontainers.image.revision` 和 platform 后退出。它不修改 `~/.docker/buildx`。使用 `EXPECTED_SHA=<candidate> IMAGE=<tag> ./build-image.sh`。

启动前 harness 必须验证 image 的 `org.opencontainers.image.revision` 与当前 expected SHA 完全一致。缺少 label、label 不匹配或显式指定的 stale image 都会 fail closed 为 `candidate_image_mismatch`；不会 fallback 到任意本地 image。默认 image tag 为 `relay-station/control:acceptance-<short-sha>`。

## Auth

先由现有 acceptance composition 生成 runtime secrets，再运行 `run.sh auth`。bootstrap secret、password、TOTP URI、cookie、CSRF 和 storage-state 都是 Secret；storage-state 只写到 `ACCEPTANCE_STORAGE_STATE` 指定的 repo-external 文件，权限必须为 `0600`。完成后删除 ephemeral runtime；不要删除无关目录。

## Lifecycle

`tools/acceptance/lifecycle-fixture/run.sh` 只包装 test-only fixture 流程。Availability 必须经 production `Reconcile()`，Duplicate 必须经 production `Evaluate()`；fixture 禁止直接调用 `EnqueueTx`。fixture DB、job IDs、payload/hash 和 evidence 不能写入仓库。

## Restart / rollback

调用方负责启动 isolated compose、停止 worker、等待 `pending`/`retry_wait`/running lease 状态、重启同一 DB，并通过 `OLD_REVISION` 显式传入 rollback binary revision。harness 不硬编码任何 Phase 5 SHA；old revision 必须由 runbook/审批提供，并验证 fail-closed 与当前 candidate 恢复。

## Safe evidence and cleanup

允许 evidence：safe UUID、固定状态、attempt/count、固定 error code、bounded timestamps、hash。禁止 webhook/token/signing secret、private key、signed URL、raw sensitive logs、password、TOTP URI、storage-state。所有 compose、browser、DB、证书私钥和 runtime 文件由调用方按 ownership cleanup。

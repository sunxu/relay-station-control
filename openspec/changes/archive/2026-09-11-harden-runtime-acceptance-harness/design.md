## Context

Harness 复用 `deploy/acceptance/compose.yaml`、`control-auth-e2e.sh`、`web/e2e/authentication.spec.ts` 和 `internal/store` 已验证的生命周期测试语义。Harness 只编排测试，不成为 Control 请求数据面的一部分。

## Design

- `build-image.sh` 在 `/private/tmp` 创建临时 `BUILDX_CONFIG`，构建后用 image label 与 platform 校验 candidate；不写 `~/.docker`。
- `auth-session.mjs` 要求显式 absolute repo-external runtime dir，bootstrap/TOTP 值仅在内存使用，storage-state 使用 `0600` 写入该目录，并在启动前拒绝 repo 内路径。
- `lifecycle-fixture` 只允许从 test-only adapter 调用 production `Reconcile()` / `Evaluate()`；adapter 不直接调用 `jobs.EnqueueTx`、`enqueueNotificationTx` 或 `notificationEnqueueRequest`。
- durable-job recovery orchestration 不属于本 change。pending/retry_wait/running-lease 与 old-binary runtime recovery 记录在 future-work 中，不能用 focused tests 冒充 self-contained runtime orchestration。
- evidence 只允许 safe IDs、状态、计数、固定 error code 和有界时间；cleanup 默认删除 ephemeral runtime，不删除用户目录或无关容器。

## Compatibility and rollback

这些文件不改变数据库 schema、API 或 production behavior。删除 harness 文件即可回滚；已产生的 runtime 必须由调用者显式 cleanup。

## Deferred recovery scope

`DEFERRED — separate Recovery Harness Hardening follow-up`：后续 change 才实现 process-level pending restart、retry_wait restart、running lease SIGKILL/expiry/reconciler takeover，以及可配置 old-binary runtime compatibility orchestration。本 change 不对这些场景给出 PASS。

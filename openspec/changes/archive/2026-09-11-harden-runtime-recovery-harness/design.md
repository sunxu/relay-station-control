# Design

复用 `deploy/acceptance/runtime/`、`cmd/control/dingtalk_deployment_readiness_test.go` 的真实 main starter/controlled TLS fixture，以及 `internal/jobs` 的持久 lease/fencing 断言。

Shell 仅负责 repo-external runtime、数据库/Control 生命周期、受控 endpoint 与 cleanup；Go test 负责通过 production lifecycle 创建 durable job、读取安全字段并验证 identity、payload hash、预算与 lease/fence。禁止直接 UPDATE job status、伪造 lease expiry 或用 `EnqueueTx` 替代生产 lifecycle。

所有场景必须使用 controlled endpoint，runtime secret 与 raw response 只存在内存或 repo-external 临时目录，evidence 只保存 safe IDs/status/counts/hash。

# Antigravity account availability

Control 对 CLIProxyAPI 的 Antigravity auth-files 只读结果进行保守判定。它不读取或保存 Token，不请求 Google，也不修改 CLIProxyAPI、Inventory lifecycle 或账号状态。

只有 `TOKEN_INVALID`、`ACCOUNT_BLOCKED` 和带 fresh runtime error/unavailable 旁证的 `FORBIDDEN` 会形成只读 ACTIVE/RESOLVED occurrence。`UNKNOWN` 永远不告警；`other`、runtime-only unavailable 和人为 `DISABLED` 也不创建故障 occurrence。普通 403 在 runtime 仍 active 时保持 `UNKNOWN/pending_confirmation`。

认证失败必须由不同的非空 `request_id` 提供独立请求证据；同一 request_id 的不同 event_hash 只算一次。没有 request_id 时，多个 event_hash 不能单独确认，必须有同账号 fresh runtime error/unavailable 交叉证据。重复 pop、重试、重启不会重复创建 ACTIVE occurrence。

可用性分支复用 Inventory 成功 finalize 后的生命周期回调和固定 20 秒 reconcile，不启动第二个 Node/Google 请求。失败与取消只记录组件错误，不阻塞原 Inventory、Duplicate 或数据面。HTTP usage queue 的 destructive-pop/no-ACK 丢失窗口仍然存在，因此 `AVAILABLE` 不是 Google 可用性 SLA。

回滚时停止 availability 分支并恢复旧 Control 镜像，保留已写入的 additive schema 与 occurrence 历史；生产不执行 migration Down。仅隔离测试库可验证本 change 新增对象的 Down/Up。

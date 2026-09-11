# Proposal: Harden Runtime Recovery Harness

为 Acceptance Harness 增加基于真实 Control main、隔离 PostgreSQL 与受控 HTTPS executor 的 durable-job crash/restart 验收能力。该 change 只增加 test-only orchestration 与 evidence，不改变 production behavior，不发送真实 DingTalk。

范围限定为 pending restart、retry_wait restart、以及 running lease 在 SIGKILL 后的 lease expiry/reconciler takeover 三个场景。

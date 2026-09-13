# Relay Control compatibility gate

本手册描述 Phase 6 的外置 `relay-control-compat-gate` 和受支持启动 wrapper。gate 属于 Control application rollback unit 之外的部署 artifact；release operator 单独发布它、签名信任根和 wrapper。本文不包含真实 signing key、manifest signature、artifact digest 或数据库凭据。

## Gate contract

gate 接受 Ed25519 signed manifest v1。manifest 是 JSON envelope，签名覆盖紧凑 UTF-8 payload `[1,"sha256:<64 lowercase hex>",compatibility_class]`，字段为 `version`、`control_artifact_digest`、`compatibility_class` 和 base64 `signature`。公钥必须从受保护的只读文件读取；release signer 私钥不进入 runtime host。裸 Control artifact 按文件内容计算 SHA-256，gate 只接受与签名 digest 完全一致的 regular file。

当前 class 为 0（pre-Phase 6）、1（Gateway-aware）和 2（Node lifecycle/cancellation-aware）。class 大于 2、manifest 缺失或字段非法、签名/公钥非法、digest 不匹配均以退出码 `78` fail closed。数据库连接、查询或 protected floor 读取失败以退出码 `75` fail closed。子进程启动失败使用 `126`；已启动 Control 的退出码由 wrapper 传递。

gate 通过 `DATABASE_URL`（或 wrapper 指定的受保护环境变量名）只读联合检查 Goose migration identity 与 `public.control_runtime_compatibility`。仅当已应用 migration version 不超过 32 且 marker 不存在时才表示 floor `0`；migration 33 要求有效 floor 至少为 1，migration 34 要求 floor 恰为 2。marker table/单例缺失或 malformed、marker 与 migration identity 不一致，均以退出码 `78` fail closed。有效 marker 必须有 `singleton_id=1`、`schema_version=1`，floor 只能为 `0`、`1` 或 `2`。gate 不写数据库，也不从环境变量推断 class/floor。

## Supported deployment paths

Compose 使用 application rollback unit 外的 `deploy/compatibility/relay-control-compat-compose-wrapper.sh` 和 `deploy/compatibility/compose.compatibility.yaml` overlay。`CONTROL_IMAGE` MUST 是 `repository@sha256:<64 lowercase hex>`；mutable tag 会在 Docker 启动前被拒绝。`CONTROL_COMPOSE_FILE` 指向产品 Compose 文件，`CONTROL_COMPAT_COMPOSE_FILE` 指向受保护的 compatibility overlay；wrapper 固定把该 overlay 放在最后，只接受可选 `--detach`，不接受可追加 image override 的任意 Compose 参数。外置 wrapper 将 OCI image manifest digest 与签名 manifest 比对、读取 DB floor，全部通过后才执行 Docker Compose；overlay 把完全相同的 digest-qualified `CONTROL_IMAGE` 交给 Docker，因此被验证与被启动的 image manifest identity 相同。不得改回 container 已创建后才校验镜像内 pathname 的 entrypoint 方案。

systemd/Linux bare-binary 使用 `deploy/compatibility/relay-control-compat.service`，其 `ExecStart` 固定为 `/usr/local/libexec/relay-control-compat-wrapper.sh`。gate 只 open selected artifact 一次，校验 regular/executable file 与权限、hash 该已打开对象，并以 Linux `execveat(AT_EMPTY_PATH)` 执行同一 file description；pathname 在校验后被替换不会改变被执行对象。缺少 descriptor exec 支持的平台 fail closed。`/etc/relay-station/control-compat.env` 只引用受保护的 gate、artifact、manifest、公钥路径和 `DATABASE_URL`，不把凭据写进 unit 或命令行。

支持的 rollout 顺序是：先停止旧 Control 和自动重启；部署支持目标 class 的 gate、信任根和 wrapper；确认 Compose/systemd 只能经 wrapper 启动；执行 forward migration 写 floor；用同一 wrapper 校验选定 artifact 和 floor；通过后才启动 Control。rollback 仍使用同一 wrapper；floor 为 1 时 class 0 被拒绝，migration 34 把 floor 提升到 2 后 class 0 和 class 1 都会在 HTTP、Directory worker 或其它 Control loop 启动前被拒绝，只有 class 2 artifact 可启动。该保证覆盖受支持的 Compose/systemd deployment path，不声称阻止 host root 手工绕过部署路径。

## Verification

```bash
deploy/acceptance/relay-control-compat-gate.sh
```

该脚本使用临时测试 artifact 和内存生成的 Ed25519 key 运行可重复的签名、篡改、digest、class、floor reader 单元测试，并检查 Compose/systemd wrapper shell 语法。Linux acceptance 还验证 pathname swap 后只能执行已验证 FD；Compose acceptance 验证 mutable tag、错误 OCI digest 均不会调用 Docker，正确签名 digest 启动完全相同的 digest-qualified image reference。测试不会生成或保存生产 key、真实 digest、数据库凭据或 raw external response。生产发布前，release acceptance 还必须使用实际签名 manifest、受支持的 wrapper、isolated PostgreSQL 18 floor fixture，并记录 class 1 rollback 在 floor 2 下未启动 Control、class 2 正常启动、marker/migration mismatch fail closed 的证据。

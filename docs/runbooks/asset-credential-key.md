# Asset credential K2 运行手册

本文适用于 Stage 0 的 Node management credential 与 Gateway directory credential。K2 是环境级、进程外的 32 字节原始密钥；PostgreSQL 只保存密文和不可逆的 identity commitment，不保存 K2。

## 支持边界

部署路径是：空数据库 → 执行完整 migration chain → provision 一个 K2 → 启动 Control。项目支持 fresh install / forward-only；不支持旧部署数据库升级、legacy `reader_secret_ref` 导入、rolling cutover、K2 rotation、hot reload、multi-key 或 KMS/Vault 集成。

## Provision

使用项目提供的 provisioning 入口，不要手工 `echo`、复制密码或把 key 放入环境变量：

```sh
CONTROL_ASSET_CREDENTIAL_KEY_FILE=/absolute/private/control/asset-credential-key \
  make provision-asset-credential-key
```

该命令使用 OS-backed CSPRNG 生成恰好 32 个 raw bytes。目标文件不存在时只创建一次；文件已存在且有效时输出 preserved 并保留原 bytes。已有文件尺寸、owner、类型或权限不符合 contract 时失败，不覆盖、不截断、不替换。允许的最终权限是 `0400` 或 `0600`；文件必须是指定 owner 的 regular non-symlink file。

输出只包含状态，不包含 key、hex、base64 或 commitment。重复执行不会轮换 K2。

## 部署与启动

生产-like Control 只接收路径：

```text
CONTROL_ASSET_CREDENTIAL_KEY_FILE=/run/control-secrets/asset-credential-key
```

K2 应由宿主或 Secret 投影预先 provision，再以 read-only bind mount / memory volume 提供给 UID/GID `65532:65532` 的 Control 容器。不要把 K2 放入 Dockerfile、image layer、build arg、Compose literal 或 `ENV` 值。Control 不会在缺少文件时生成 K2；必须先 provision，再启动。

首次启动会在空数据库中初始化 commitment。相同 DB 与相同 K2 重启后仍可用；K2 文件后来补上或被纠正不会被热加载，必须重启 Control。

## 缺失、错误和无效状态

- 启动时缺少 K2、K2 文件无效或 commitment 不匹配：Control 保持运行，但 credential capability unavailable。
- unavailable 期间 Set 新 credential、认证出站等操作 fail closed；Keep、Clear、Retire、未配置 Replace 和 credential-free 功能继续工作。
- commitment 不匹配时不得改写 commitment、自动重新加密或采用新 K2。
- commitment 缺失且数据库已有 sealed credential 是 invalid DB state；不得用任意新 K2 采纳该状态。应恢复匹配的 DB/K2 恢复单元。
- DB 中仍有 sealed credential 而 K2 永久丢失时，现有 credential 不可恢复。commitment 不能重建 K2。

## Backup、restore 和迁移主机

恢复单元始终是：

```text
PostgreSQL backup + exact same K2 bytes
```

K2 必须在 PostgreSQL 之外以受限权限单独备份。备份和演练不得打印或记录 key、hex、base64、commitment、密文或 plaintext credential。

恢复流程：

1. 恢复 PostgreSQL backup。
2. 安全地恢复相同 K2 bytes，并保留合法 owner/mode。
3. 以 `CONTROL_ASSET_CREDENTIAL_KEY_FILE` 指向容器内 read-only 路径。
4. 启动 Control，确认 commitment match 与 credential capability available。
5. 通过受保护读取与 production opener 确认恢复后的 Node、Gateway credential 可 Open；再用 Gateway Directory fenced authenticated outbound 做受控出站冒烟。Node authenticated outbound 由 Gate 4 owning proof 覆盖。

使用错误 K2 或不提供 K2 时，Control 应继续运行但保持 unavailable，且 authenticated outbound 必须为零；数据库 commitment 与 sealed blobs 不变。纠正文件后重启 Control，再执行恢复验证。

迁移到另一台主机时可以改变路径，但必须传输完全相同的 K2 bytes。删除并重建容器不会影响恢复，只要 DB 与宿主 K2 都保留；K2 不得存放在容器 ephemeral writable layer。

## 验证与脱敏

只记录固定状态、错误分类、migration version、容器 UID/GID 和验证结果。不要把 K2、commitment、sealed blob、plaintext credential 或敏感本机路径写入日志、audit、metrics、evidence 或工单。Gate 6 不引入 rotation；任何轮换需求必须另行评审。

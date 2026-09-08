# Planning validation

## Implementation evidence — 2026-09-08

用户已授权实施及当前本地环境更新。当前实现不修改产品 API、schema、generated client、
UI 或 migration；不修改既有归档。任务状态以 tasks.md 为准，尚未宣告 Final Review。

### Commands and results

- Control：`python3 -m unittest discover -s deploy/acceptance -p 'test_directory*.py' -v`
  → 25 tests PASS，10.495s；真实 loopback HTTP/TLS，持久私有路径 fixture。
- Ops：`python3 -m unittest discover -s dev -p 'test_*.py' -v`
  → 45 tests PASS，2 Docker tests 明确跳过，3.114s。
- Ops：`RELAY_DEV_CONTAINER_TEST=1 python3 -m unittest discover -s dev -p test_container_operations.py -v`
  → 2 tests PASS，25.501s；随机隔离 Compose 项目、真实 PostgreSQL、好/坏本地构建镜像，
  目标与固定代理更新/回滚，peer container ID/image/config 不变；不操作开发栈 volume。
- `bash -n dev/devctl` → PASS。
- 两个实际本地数据库通过 Docker exec/psql `BEGIN READ ONLY` 运行
  `schema_gate.capture/recheck` → Control/Gateway 均 ledger exact match PASS。
- 正式 `devctl backup control` 与 `backup gateway` 已完成私有 custom dump，
  `pg_restore --list` PASS；这只证明归档可读，不等同完整恢复演练。
- 正式 `devctl directory drill --acceptance-config <private-config>` → PASS：
  baseline、自然 stale/unknown、恢复 source、同一 Binding fresh/resolved 且观测推进；
  最终 HTTP 工具真实 PG/main 专项仍在运行，本地受控更新与单次 AI 冒烟尚未完成。

### Acceptance matrix

| ID | Implementation / fixture | Assertion / result |
| --- | --- | --- |
| L1 | `directory_http.py` / `test_directory_config_paths.py`、HTTP fixture | 显式 UUID/account，拒绝 numeric/overflow、仓库/RAM/symlink/宽权限输入；PASS |
| L2 | HTTP session / TLS fixture | 正常密码/MFA/CSRF；只有 session 401 才一次登录回退；Cookie 保存复用、redirect/不可信 CA/超限/slowdrip 拒绝；PASS |
| L3 | `test_all_int64_values_are_read_and_request_serialized_as_strings`、lost response 两分支 | 四个大整数 read/write 逐字字符串，overflow 拒绝；成功精确对账和 unknown 均只一次 POST；PASS |
| L4 | baseline / stale/recovery tests | UTC 比较、错目标拒绝、失败不改 baseline，同一 Binding freshness/resolution 和观测推进；PASS |
| L5 | Directory/data-plane/main canary tests | 200/401/405/404/403、JSON/no-store/source v1 numeric；显式模型请求仅留结构；stdout/stderr 无 canary；PASS |
| L6 | `test_update_admission.py`、`test_update_runtime.py`、real Docker | 缺 label/dirty HEAD/revision mismatch 零 mutation；最终 override 使用已检查 digest；同镜像 no-op；PASS |
| L7 | `test_local_operations.py`、backup failure、real Docker | 私有 backup、dump 可读才写 metadata、失败不更新、未知字段保持；PASS |
| L8 | `test_operation_state.py`、`test_devctl_lock.py`、directory signal tests | 继承锁/并发/legacy pending guard；真实 INT/TERM 恢复原开关；存活/PID复用/未知 owner 拒绝；PASS |
| L9 | real bad image / runtime failure tests | 启动失败恢复旧 digest/config，固定代理刷新；peer/proxy 冲突保留 pending 并明确失败；PASS |
| L10 | `TestLocalDirectoryToolingNaturalRecovery`、正式本地 drill | 正式本地 drill PASS；最终隔离 PG/main 专项运行中 |
| L11 | `DIRECTORY_HTTP.md`、ops `OPERATIONS.md` / protected config tests | 正式仓库入口及 mode 映射；拒绝临时部署配置；未删除旧临时文件；归档哈希复核一致 |
| L12 | `schema_gate.py`、9 schema tests / admission tests | 迁移新增/删除/改旧SQL/启动路径差异拒绝；Goose 最新记录精确集合、Gateway checksum/Atlas；启动后 drift 禁止回滚；PASS |
| L13 | state SIGKILL / directory recovery tests | prepared/applying/verified SIGKILL 保留记录；逐文件 old/new、恢复再失败可重试、verified 只清理不回滚；路径穿越/symlink/外部冲突写前拒绝；PASS |
| L14 | peer/proxy invariant tests / real Docker | control/control-tls 或 gateway/gateway-proxy 固定集合；其它 container ID/digest/config 不变；PASS |

公开证据不保存真实身份、Secret 或响应正文。运行输入、会话、baseline、backup 和
六个账号/volume 保留校验仅放在已存在的仓库外私有 runtime。

## Planning baseline (historical)

以下为规划阶段记录，不能解读为实现结果；实施证据以上方为准。

## Design review revision

- 原P1：不能用“不调用迁移命令”保证Gateway启动无schema变化。现冻结旧/新commit迁移集合、runner/启动路径及DB已应用记录检查，未知或差异停止；按image digest启动，启动后或回滚前DB漂移停止自动回退。
- 原P2：恢复入口未定义。现提供显式recover及prepared/applying/verified三阶段，覆盖逐文件原子写中断、配置冲突、存活owner、恢复再中断及完成前清理；演练在同一devctl持锁过程内协调，避免pending阻止自身恢复。
- 原P2：代理范围开放。现固定control/control-tls与gateway/gateway-proxy，代理镜像/配置不得改变；其它服务ID/digest/配置全部保持。
- 本次仍仅修订既有规划文件；所有实施验收保持未执行，不把设计修复或strict PASS当成工具运行成功。

复审结果：原1项P1及2项P2均已在设计与任务中闭合。独立复核覆盖schema准入、代理集合和recover状态机；没有运行实现测试，任务1/15，仅评审项完成；本轮保存为规划基线提交。

归档 SHA-256（排序的相对路径 + NUL + 文件内容）：
`6b335397204b484ed9c414e8574a0412398aa66222d824f1d164e09a5e9368c2`，实施前后相同。

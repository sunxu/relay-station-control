# Planning validation

## Implementation evidence — 2026-09-08

用户已授权实施及当前本地环境更新。当前实现不修改产品 API、schema、generated client、
UI 或 migration；不修改既有归档。Final Review 的 1 项 P1、4 项 P2 已修复并完成专项验证；任务 15/15，Final Re-review 已通过，按用户指令执行标准归档；不 push。

### Final Re-review

2026-09-08：对 Control `a4b9843`、Ops `2363cf3` 及对应专项证据完成只读复审。
原 1 项 P1、4 项 P2 全部闭合，未发现新的确定性 P1/P2。Change strict、all strict
均通过（15 passed / 0 failed），Control/Ops/Gateway 工作树干净；Node 仅保留
原有 AGENTS.md 修改。用户随后授权继续标准归档；本轮不部署、不 push。
本 change 声明 skip_specs，CLI 确认没有 delta specs，canonical 产品 spec 无需同步。

### Final Review corrections — completed

- R1 / P1：应用或代理容器缺失时，恢复前校验阻止重建。补缺失 app/proxy/both 的恢复验收，并保留查询失败、外部冲突与 verified 阶段严格拒绝。
- R2 / P2：代理摘要遗漏端口、挂载和网络配置。补稳定运行配置摘要及漂移负例，真实隔离重建验证排除项不会造成误报。
- R3 / P2：等待 deadline 未覆盖完整登录和请求。补认证/轮询/响应体的剩余预算验收。
- R4 / P2：相对 data-plane URL 忽略独立 token。拒绝相对路径，验证绝对 URL 的 Bearer 认证且不携带 Control Cookie。
- R5 / P2：缺失 Binding ID 仍可成功。补合法 UUID 断言及缺失/畸形响应，不重放 bind。

### Correction validation

R1/R2：已修复并验证，Ops commit `2363cf3`。

- `cd ../ops && python3 -m unittest -v dev.test_update_runtime` → 16 tests PASS。
  逐个或同时缺失 app/proxy 可恢复；缺一容器时另一现存容器的外部镜像/挂载冲突
  仍被拒绝；Docker 查询失败不等于缺失；verified 缺失不回滚；旧摘要版本拒绝；
  端口、挂载、网络漂移被检测，动态 IP/endpoint/MAC 变化不误报。
- `cd ../ops && python3 -m unittest discover -s dev -p 'test_*.py' -v`
  → 57 tests PASS，3 Docker tests 明确跳过，3.073s。
- `cd ../ops && RELAY_DEV_CONTAINER_TEST=1 python3 -m unittest discover -s dev -p test_container_operations.py -v`
  → 3 tests PASS，40.393s。新增真实子进程在 applying 后移除随机 fixture 的两个
  目标容器，再 SIGKILL；父进程确认退出码、pending、容器缺失，使用正式 recover
  恢复原 app/proxy digest 和完整 proxy identity，peer ID 不变，schema/health 通过，
  pending 清理。未操作现有开发栈。
- `bash -n dev/devctl`、Ops `git diff --check`、commit 前 cached diff check → PASS。
- 旧 pending 若缺少 `proxy.version=2`，须使用旧工具先收尾；不得伪造新版摘要。

R3/R4/R5：已修复并验证。

- `python3 -m unittest discover -s deploy/acceptance -p 'test_directory*.py' -v`
  → 30 tests PASS，13.993s。
- `Client.call` 正常/HTTPError 响应的连接、headers 和 body 共用同一个请求计时器；
  请求预算取单请求上限与等待剩余时间的较小值。真实 session 响应延迟 1.6s，
  wait 总预算 1s、单请求上限 30s，必须以 wait_timeout 在 1.5s 内结束。
  错误响应先延迟 headers，再分块慢送 body，仍不重置总 deadline。
- 合成 data-plane 请求使用已登录的 Control Client 作为输入，断言实际请求携带
  独立 Bearer token、没有 Control Cookie；相对 URL 在读取 token/发请求前被拒绝。
- read/已有 bind 对 None、空字符串、畸形字符串、numeric Binding ID 均拒绝，
  没有 POST；丢响应后的畸形 Binding ID 保持 mutation_unknown，严格一次 POST。
  合法合成 fixture 改用真实 UUID，四个 int64 大整数契约测试继续 PASS。
- 慢响应 fixture 显式处理客户端超时断开，避免预期 BrokenPipe 干扰测试日志；
  `python3 -m unittest discover -s deploy/acceptance -p test_directory_http.py -k deadline -v`
  → 3 tests PASS，2.528s。

本轮仅重跑直接相关 Python/隔离 Docker 专项，未重跑自然 540s、完整构建、Chrome
或全量 PostgreSQL；没有产品实现/API/schema/generated/UI 变化。本轮也未重新部署
或再次调用模型。下方先前部署与自然恢复证据仍对应原先明确列出的 commit/image，
不会将旧运行结果改写为本轮修复后的部署结果。

以下记录为修复前已实际执行的结果，仍是历史运行证据，不能替代上述修复的专项验证。

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
  最终 HTTP 工具真实 PG/main 专项 PASS（714.33s，Go package 715.057s）；
  本地受控更新及单次 AI 冒烟 PASS，详见下方发布证据。

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
| L10 | `TestLocalDirectoryToolingNaturalRecovery`、正式本地 drill | 正式本地 drill 与最终隔离 PG/main 专项均 PASS；同一 Binding、Account decimal string、观测失败不推进/恢复推进 |
| L11 | `DIRECTORY_HTTP.md`、ops `OPERATIONS.md` / protected config tests | 正式仓库入口及 mode 映射；拒绝临时部署配置；未删除旧临时文件；归档哈希复核一致 |
| L12 | `schema_gate.py`、9 schema tests / admission tests | 迁移新增/删除/改旧SQL/启动路径差异拒绝；Goose 最新记录精确集合、Gateway checksum/Atlas；启动后 drift 禁止回滚；PASS |
| L13 | state SIGKILL / directory recovery tests | prepared/applying/verified SIGKILL 保留记录；逐文件 old/new、恢复再失败可重试、verified 只清理不回滚；路径穿越/symlink/外部冲突写前拒绝；PASS |
| L14 | peer/proxy invariant tests / real Docker | control/control-tls 或 gateway/gateway-proxy 固定集合；其它 container ID/digest/config 不变；PASS |

公开证据不保存真实身份、Secret 或响应正文。运行输入、会话、baseline、backup 和
六个账号/volume 保留校验仅放在已存在的仓库外私有 runtime。


### Real PG / HTTP command

先按现有隔离 PostgreSQL 测试说明设置 `CONTROL_DATABASE_TEST_URL` 与
`CONTROL_RUNTIME_DATABASE_TEST_URL`；使用本地 55432 测试服务的 migrator/runtime
连接，测试创建自己的隔离 schema。真实凭据不写入公开证据。

```bash
RELAY_DIRECTORY_TOOLING_E2E=1 go test ./cmd/control -run '^TestLocalDirectoryToolingNaturalRecovery$' -count=1 -timeout=25m -v
```

最终运行 PASS：正常密码 HTTP 登录和 CSRF bind，目标 Account
`9007199254740993`；source 失败后等待自然 540s，断言相同 Binding 的 stale/unknown
且 last_success 不变；恢复后等待正常 180s 采集槽，同一目标 fresh/resolved 且时间
严格推进。没有通过修改时间、写入 snapshot/run 或预置 session 制造通过；
Go 进程和 HTTP CLI 输出同时检查合成凭据/身份 canary 不泄露。

### Local deployment evidence

正式配置位于既有仓库外私有 runtime 的 `acceptance/directory.json`。
下列 `<runtime>` 代表该持久私有目录，不依赖旧临时脚本；实际账号、UUID、token、
Cookie 和 URL 凭据不记录在公开文档。

```bash
# 在 ops 仓库
RELAY_DEV_RUNTIME_DIR=<runtime> ./dev/devctl directory drill --acceptance-config <runtime>/acceptance/directory.json
RELAY_DEV_RUNTIME_DIR=<runtime> ./dev/devctl backup gateway
RELAY_DEV_RUNTIME_DIR=<runtime> ./dev/devctl update control relay-station/control:172704f

# 在 control 仓库
python3 deploy/acceptance/directory_http.py --config <runtime>/acceptance/directory.json read
python3 deploy/acceptance/directory_http.py --config <runtime>/acceptance/directory.json directory-check
python3 deploy/acceptance/directory_http.py --config <runtime>/acceptance/directory.json data-plane
```

以上均 PASS。一次模型请求通过已有 Gateway → Node 路径，结果仅保留结构断言。
更新前由正式入口完成 Control custom dump 与匹配配置备份，包含镜像/revision metadata；
独立 Gateway backup 最终版本也 PASS。Control 与 TLS 代理最终健康，迁移/DB baseline
无变化，其余服务 container ID/image/config 保持；同镜像第二次 update PASS，额外核对
全部 container IDs 不变且 backup 目录集合不增加。

- Control 实现 commit：`172704ffa30ed5de118c264cf35801da545a9a84`。
- Ops 实现 commit：`69c4832`。
- 构建命令：`docker build --pull=false --label org.opencontainers.image.revision=172704ffa30ed5de118c264cf35801da545a9a84 --build-arg VERSION=172704ffa30ed5de118c264cf35801da545a9a84 -t relay-station/control:172704f .` → PASS。
- 实际 Control image：`sha256:40967ea1bb9e8bf97fc479ff95f1075e9640ab50faafadc18166cbb5c7c33870`。
- 私有 before/after 校验：6 个账号文件集合与内容 SHA-256 全部一致；7 个 volume
  名称集合一致；Node container ID 不变；source/poller 均恢复原 true；pending 不存在。
- Gateway 和 Node 产品镜像未更新；未删除数据，未执行 migration/down 或 DB 还原。

### Final validation

- Change strict 与 `openspec validate --all --strict` → PASS（15 passed / 0 failed）。
- Control/Ops `git diff --check`、每次 commit 前 `git diff --cached --check` → PASS。
- API/schema/generated/UI 均未修改，generate 不适用。已运行直接相关真实 PG/HTTP、
  Docker 和 Python/bash 专项；本地 Control 镜像构建 PASS，Web 层命中缓存。
  按本 change 4.4 的工具范围不重复 make 全量业务测试、全量 PG 或 Chrome。
- 暂存清单仅仓库脚本、合成测试和文档；私有账号、Secret、session、baseline、backup
  未暂存。没有改写历史归档或任何 canonical spec。

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

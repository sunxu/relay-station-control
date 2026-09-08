## Planning validation

### Scope

- [x] 仅修改 Control Web 的用户可见时间格式，不修改 API、数据库、migration、generated code、collector 或 CLIProxy
- [x] 浏览器系统时区是显示时区；服务端、PostgreSQL、HTTP 和计算仍使用既有 UTC/ISO instant
- [x] 目标格式为 `YYYY-MM-DD HH:mm:ss`，缺失或非法值为 `—`

### Evidence plan

- [x] 记录所有实际显示调用点和共享 formatter 替换结果
- [x] 使用至少两个运行时系统时区验证同一 instant 的本地显示和零填充格式
- [x] 验证 API/DB 请求字符串、排序、窗口、cursor 和 freshness 计算未改变
- [x] 验证登录/会话、资产、任务、Topology、Inventory、Provider、Quality、History、Incidents、详情和容量的用户可见时间
- [x] 完成前端测试、typecheck、build、OpenSpec strict 与 `git diff --check`

### Release and rollback

本 change 无 migration、无外部数据变更。实现已按用户授权提交并部署本地；随后完成独立 Final Review。失败时回滚 Web bundle/commit，保留现有 API 与数据库数据。

## Implementation evidence — 2026-09-09

基于本地 main `8cbc0d6` 完成；实现验证完成；用户随后授权本地提交与部署。验证时尚未部署，部署结果以本地发布记录为准；未 push、未 archive。

- 共享实现：[time.ts](../../../../web/src/time.ts)，以 Date 本地字段格式化，零填充至秒；无时区配置和缓存。空值/非法值为 `—`。
- 登录、会话和一次性材料：LoginPage、ManagementPage、OneTimeMaterialPage。
- 资产/任务：AssetRegistryView、JobRegistryView（请求筛选的 `requestTime().toISOString()` 保持原样）。
- Topology：TopologyView 中 Provider、Binding、Duplicate 当前/历史与 Evidence 时间；AccountList 最近请求 tooltip/最近失败；AccountDetailsDrawer 采集信息；AccountRequestHistorySection、AccountQualityIncidentsSection；AccountInventoryCapacity 的评估槽和评估时间。
- [runbook](../../../../docs/runbooks/node-centric-topology-ui.md) 明确浏览器系统时区和服务端 UTC instant 的边界。

### Validation

| 命令 / 检查 | 结果 |
| --- | --- |
| `TZ=UTC npm test -- src/time.test.ts --reporter=dot`（web） | PASS，3/3 |
| `TZ=Asia/Shanghai npm test -- src/time.test.ts --reporter=dot`（web） | PASS，3/3 |
| `TZ=America/New_York npm test -- src/time.test.ts --reporter=dot`（web） | PASS，3/3 |
| `make test build` | PASS，Go tests/build、前端 21 files / 128 tests、typecheck、build |
| change strict | PASS |
| `openspec validate --all --strict` | PASS，18/18 |
| `git diff --check` | PASS |

formatter 测试用本地构造的 `2026-09-08 18:40:40.987` 对应 instant 断言逐字 `2026-09-08 18:40:40`；纽约夏季/冬季 UTC 午夜分别断言上一日 20:00:00/19:00:00，上海分别为当日 08:00:00。独立进程 TZ 运行证明不固定 UTC 或 Asia/Shanghai，且普通测试不限制开发者的系统时区。

首次完整验证因 Incident 测试的宽泛日期选择器同时匹配 first_seen/last_seen 失败；已分别按两个字段的格式化值精确断言，重新执行完整验证通过。最终日志：`/Volumes/DevRAM/tmp/control-system-time-make-final.log`。Go 输出 module stat-cache 写权限提示，但 `make` 退出码为 0；未修改 GOCACHE/GOTMPDIR 默认配置。

### Final scope review

独立只读复核未发现 blocker。扫描确认产品展示代码无 `utc()`、`toLocale*()`、固定 `timeZone` 或 UTC 后缀残留；唯一 `toISOString()` 是任务查询的既有请求转换。API、generated files、Go、数据库、collector、CLIProxy、时间窗口与数据面均无 diff；没有持久化变更或新的配置。实现阶段工作树仅包含本 change 的 Web、测试、OpenSpec 和 runbook 修改。此段为当时的实现自查；后续独立 Final Review 见下。

## Final Review and release reconciliation — 2026-09-09

- 独立 Agent Architecture Final Review：PASS；Implementation Final Review：PASS；code blockers：none。主 Agent 复核通过。此次为用户授权继续执行流程后的独立评审，不冒称用户另行给出人工 APPROVED。
- 顺序如实记录：实现 `7be7b7652f01e15fa4c24b6415a025279fbf55d2` 先按用户授权提交并部署本地，随后完成本次 Final Review，属于 review-after-local-deployment。没有把部署前的实现自查追认为 Final Review。
- 标准部署 `devctl backup control` / `devctl update control relay-station/control:7be7b76` 均 PASS；备份 `20260908T185752Z-control-88d0cdb9`。镜像 digest `sha256:3e9a0459348ec8810f1d52e3b3e36ed00700a25406575d38afc3f1f138a179f3`，容器 revision 精确对应实现提交，复核状态 healthy。未重新部署其它服务。
- 本轮重新执行三个 TZ formatter 专项均为 3/3 PASS，change strict 与 all strict（18/18）PASS，git diff check PASS。复用实现提交的完整 `make test build` 证据；后续 HEAD 仅增加独立 acceptance 测试修改，不涉及本 change 产品实现。
- 11/11 tasks 完成。用户授权的后续步骤为标准 CLI archive、canonical 同步、文档提交及 push main；无新增功能，无需再次部署本次归档文档。

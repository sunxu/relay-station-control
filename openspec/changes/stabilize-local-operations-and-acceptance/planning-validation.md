# Planning validation

2026-09-08：仅完成规划，已按设计评审REQUEST CHANGES修订；主Agent及独立复审未发现剩余P1/P2 blocker，用户已授权继续确认并提交架构基线。任务1/15（仅1.1架构评审完成）；CLI的planning complete表示文档齐全，不表示实现通过。产品行为不变，明确skip_specs；没有新增或修改canonical spec。

## Acceptance matrix

| ID | 场景 | 预期证据 / 对应任务 | 实施结果 |
| --- | --- | --- | --- |
| L1 | 无个人路径、多个Node、非法/缺失protected输入 | 合成配置与HTTP选择测试，2.1 | 未执行 |
| L2 | 正常登录/MFA/CSRF、会话过期、redirect/TLS失败 | 认证与网络负例，2.1 | 未执行 |
| L3 | 四个大整数逐字无损、overflow、bind未知结果不重放 | 精确HTTP请求/读取断言，2.2 | 未执行 |
| L4 | baseline跨目标拒绝、UTC比较、unavailable不冒充空 | baseline/等待专项，2.3 | 未执行 |
| L5 | source认证与路由矩阵、公开输出无canary | Directory/data-plane合成fixture，2.4 | 未执行 |
| L6 | 选定服务revision、override最终值、其它服务不变 | fake Docker与隔离Compose，3.1/3.4 | 未执行 |
| L7 | 备份先于变更、partial拒绝、原子配置与权限 | backup/文件故障注入，3.2 | 未执行 |
| L8 | 并发锁、信号中断、未完成恢复与外部冲突 | 多进程/恢复测试，3.3 | 未执行 |
| L9 | 健康失败回滚及回滚失败明确输出 | 隔离失败镜像与原配置核验，3.4 | 未执行 |
| L10 | 自然stale/unknown→fresh/resolved，同一Binding，原开关恢复 | 真实PG/HTTP与本地授权演练，4.1/4.2 | 未执行 |
| L12 | 迁移新增/删除/改旧SQL、runner改变、未知revision、DB漂移 | 更新前零mutation；启动后漂移禁止镜像回滚，3.1/3.4 | 未执行 |
| L13 | prepared/applying/verified中断及恢复再次中断 | 显式recover、逐文件旧新状态、外部冲突拒绝、幂等清理，3.3 | 未执行 |
| L14 | 固定代理集合 | control/control-tls或gateway/gateway-proxy；其它容器ID/digest/配置不变，3.4 | 未执行 |
| L11 | 无RAM盘依赖、档案保持、旧脚本替代可复现 | 文档引用/临时目录隔离检查，4.3 | 未执行 |

## Validation result

- `openspec validate stabilize-local-operations-and-acceptance --type change --strict --no-interactive`：PASS。
- `openspec validate --all --strict`：15 passed / 0 failed。
- `git diff --check`：PASS；新文件另检查行尾空白。
- 未运行部署、故障注入、业务测试或完整构建。未修改Control/ops实现、现有运行配置、账号、canonical或归档文档；本轮仅提交规划基线。

## Design review revision

- 原P1：不能用“不调用迁移命令”保证Gateway启动无schema变化。现冻结旧/新commit迁移集合、runner/启动路径及DB已应用记录检查，未知或差异停止；按image digest启动，启动后或回滚前DB漂移停止自动回退。
- 原P2：恢复入口未定义。现提供显式recover及prepared/applying/verified三阶段，覆盖逐文件原子写中断、配置冲突、存活owner、恢复再中断及完成前清理；演练在同一devctl持锁过程内协调，避免pending阻止自身恢复。
- 原P2：代理范围开放。现固定control/control-tls与gateway/gateway-proxy，代理镜像/配置不得改变；其它服务ID/digest/配置全部保持。
- 本次仍仅修订既有规划文件；所有实施验收保持未执行，不把设计修复或strict PASS当成工具运行成功。

复审结果：原1项P1及2项P2均已在设计与任务中闭合。独立复核覆盖schema准入、代理集合和recover状态机；没有运行实现测试，任务1/15，仅评审项完成；本轮保存为规划基线提交。

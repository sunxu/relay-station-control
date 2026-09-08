## 1. Scope and contract review

- [x] 1.1 评审proposal/design，冻结Control验收与ops devctl分工、显式操作/恢复边界；用旧脚本mode映射核对没有遗漏必要能力或复制一次性工具。
- [x] 1.2 核对Control/ops各自仓库约束及当前API/Compose，固定help、protected配置字段和退出分类；交付无Secret样例，确认0 API/schema/generated/UI变更。

## 2. Control acceptance tooling

- [x] 2.1 实现受保护输入、显式Gateway/Node/Account身份和正常会话/MFA/CSRF处理；合成HTTP验证权限/过期/redirect/TLS/超限及多Node不选首项，默认检查不发mutation或AI请求。
- [x] 2.2 固化read及显式bind模式，精确核对同一Binding；测试相同no-op、不同拒绝、响应丢失后对账/unknown不重放、四个大整数逐字round-trip及overflow拒绝。
- [x] 2.3 固化baseline/stale/recovery断言，解析UTC时间、校验目标与环境、设置有界等待；测试错baseline、失败不推进时间、无可用读取不得当空/成功及恢复精确身份。
- [x] 2.4 固化Directory-check与显式data-plane模式；合成矩阵验证认证/路由/public/JSON/no-store/source v1、模型响应只留结构结果，并以canary扫描stdout/stderr/公开证据无敏感内容。

## 3. Ops tool integration

- [x] 3.1 复用devctl配置/Compose/版本逻辑增加selected-service update与独立source/poller开关；fake Docker验证完整revision、最终override选中镜像、无变更no-op；核对旧/新revision迁移及启动路径和真实DB账本，新迁移/改旧SQL/缺label/未知版本/账本漂移均在首次mutation前拒绝。
- [x] 3.2 实现受保护backup与原子配置更新；验证权限、dump可读、partial不成功、backup失败零变更、未知字段/挂载/开关保持且不执行migration/down/数据删除。
- [x] 3.3 为写操作接入同一runtime锁和最小中断标记；并发、INT/TERM、prepared/applying/verified各阶段SIGKILL、逐文件混合旧新状态、恢复再次中断、verified清理前崩溃、存活owner/PID复用及外部配置冲突测试证明不会覆盖其它操作。
- [x] 3.4 实现更新失败恢复原兼容镜像/配置与独立回滚失败结果；fake服务失败及真实隔离容器fixture核对目标恢复、control/control-tls或gateway/gateway-proxy固定集合的变化与其余容器ID/digest/配置不变；启动后DB账本变化拒绝自动回滚，禁止自动DB还原。

## 4. Integrated verification and documentation

- [ ] 4.1 串联显式故障演练与devctl，验证成功/失败/中断均恢复原开关、保留Binding且不自动解绑；真实隔离PG/HTTP等待自然540秒stale并在180秒槽恢复，录制实际命令与精确断言。
- [ ] 4.2 取得本地执行授权后完成一次受控更新与Directory/Binding/AI最小冒烟，保留既有账号、volume与开关；不能用mock结果或旧归档PASS代替本次运行证据。
- [x] 4.3 交付正式Runbook、配置样例和旧脚本mode映射，验证清空临时脚本依赖后仍可从仓库入口复现；只做路径隔离验证，不实际删除用户临时文件，既有归档哈希保持不变。
- [ ] 4.4 运行bash语法、Python标准库测试及新增工具专项；按实际实施范围执行项目要求的生成/构建检查，若无生成/API/schema/Web变化则记录不适用，不为本工具重复全量PG/Chrome验收。
- [ ] 4.5 将evidence矩阵关联实现、fixture、命令、断言及实际结果；运行change/all strict、diff检查，检查敏感文件未暂存，按Control/ops独立提交并等待Final Review，不自动归档。

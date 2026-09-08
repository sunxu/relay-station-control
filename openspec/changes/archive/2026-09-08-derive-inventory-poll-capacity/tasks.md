## 1. Contract review

- [x] 1.1 完成容量公式、旧50/10替代、超限整轮拒绝和HTTP字段的Architecture Review；记录对P1/P2修订的工程审查结论后实施（用户已授权本轮推进至归档）。
- [x] 1.2 核查安全计数读取的现有ACL并选定复用query或一个additive function；以现有scheduler eligibility逐项对照记录无差异。

## 2. Capacity and scheduler

- [x] 2.1 删除人工MAX_NODES输入并统一内部容量来源，保留旧变量固定警告；验证缺失/1/无效旧值、C1/2/6/7/10、等号、无可行值和硬上限测试。
- [x] 2.2 精确分类capacity_exceeded并保留原退避/恢复；真实PostgreSQL验证N与N+1、拒绝零新增poll、已运行任务不受影响、监控恢复后正常槽重启调度。
- [x] 2.3 完成50Node/C25慢Driver和恢复专项，断言实际并发不超过25、grace内dispatch、fencing和retry边界不变；失败停止并回到设计评审。

## 3. Read model and UI

- [x] 3.1 实现安全计数与UTC快照读取；验证policy/capability/activation边界、0账号Node、并发注册及scheduler同槽eligibility一致性，必要migration仅新增函数并测试ACL和Down范围。
- [x] 3.2 新增管理员只读HTTP诊断并通过make generate更新Go/TS；测试401、503、关闭、超限、恢复和禁止缓存，不泄露原始SQL或账号身份。
- [x] 3.3 账号清单展示容量及处理建议；测试未选Node仍可见、失败不显示ready、不自动查询账号、不触发mutation，以及刷新清除已恢复的超限提示。
- [x] 3.4 增加固定低基数错误观测并验证capacity与DB failure分离，禁止identity标签；重启后从真实配置/数据库恢复诊断。

## 4. Deployment and evidence

- [x] 4.1 更新Control Runbook及关联Ops清理说明，核对Compose/private override旧变量移除和旧镜像回滚参数；Ops修改独立评审提交，不夹带运行数据。
- [x] 4.2 执行make test build及上述真实PostgreSQL容量/ACL专项，记录command、fixture、assertion、result；不以mock或公式替代容量门禁。
- [x] 4.3 完成实现/evidence reconciliation、OpenSpec strict和git diff --check，确认数据面与旧API不变；发布另行授权；本轮用户已授权在验收完成后archive。

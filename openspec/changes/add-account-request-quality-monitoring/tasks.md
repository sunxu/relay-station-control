## 1. 本地契约调研与身份契约

- [x] 1.1 核实本地CPA HTTP client、normalizer、hash、collector和license；文件行号记录于source-investigation.md
- [x] 1.2 核实Control transport、canonical identity、runtime和单实例假设；记录现有auth_index映射缺口
- [x] 1.3 按Architecture Review冻结nullable account_key：direct/unique resolved，missing/deleted/duplicate/conflict unresolved；更新spec和design，不新增历史身份系统


## 2. 最小采集实现

- [x] 2.1 复用现有management transport增加唯一HTTP queue method并移植必要CPA normalization/hash与MIT notice；测试正常/空/非法payload、timeout和取消
- [x] 2.2 实现最小domain、canonical identity resolver和五类classifier；测试成功、五类失败和身份mapping，不引入第二套identity
- [x] 2.3 新增最小PostgreSQL additive table/受控访问/幂等写入；真实PostgreSQL验证重复忽略、事务失败及runtime权限
- [x] 2.4 接入单实例每Node一个collector、batch100/poll1s/backoff30s和shutdown；测试DB失败保留批次、不继续pop、重启重复安全和取消
- [x] 2.5 添加DB时间七天有界删除operation并接入collector周期；真实PostgreSQL验证期限内保留及到期删除
- [x] 2.6 实现单账号15m/1h查询；真实PostgreSQL验证窗口、p95、无请求unknown、DB失败返回错误

## 3. 验收与阶段交付

- [x] 3.1 在真实PostgreSQL写入约100000 synthetic events并检查15m/1h查询和执行计划；记录实际耗时，不引入rollup/partition
- [x] 3.2 运行make test build、专项PostgreSQL采集闭环和必要生成检查；将原14项及新增identity/unresolved测试逐一对应fixture/assertion/result
- [x] 3.3 对账文档、自查、OpenSpec strict与git diff --check，按实际diff分阶段commit；停止等待Implementation Review，不push/deploy/archive

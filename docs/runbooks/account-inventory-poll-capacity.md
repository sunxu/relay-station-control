# Inventory 采集容量诊断

Control 使用并发和时间预算推导唯一容量，不再从 `CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES` 读取最大Node数。旧变量存在时每次启动输出固定弃用警告，不输出原值。

## 预算与边界

容量为1..50中满足 `(ceil(N/C)-1)*(T+F+L)+N*Q+M<G` 的最大N：C实际并发；T请求timeout；F落库timeout；L固定30秒生命周期回调；Q固定1秒claim；M dispatch余量；G启动grace。默认T15秒、F10秒、M10秒、G120秒时，并发1/2/7/10/25分别支持2/4/14/20/50个Node。额度覆盖完整Worker流程，不能把HTTP结束当作额度释放。

这是正常及时起槽、外部操作遵守超时的首attempt准入预算，不承诺故障重试全部成功。CPU投影/进程调度开销由dispatch余量覆盖；grace、lease和fencing仍为真实运行约束，数据库故障不会被预算证明成正常。

## 查看与恢复

账号清单页面无需选择Node即可读取容量诊断；点击“刷新容量”重新读取。也可使用已有管理员session调用 `GET /api/account-inventory/poll-capacity`。它仅评估当前数据库UTC槽和当前进程配置，不证明最后一次采集成功，不触发账号查询或账号查看审计。

- ready：当前eligible数不超过容量，采集结果仍到Inventory中核实。
- capacity_exceeded：当前规模超过容量，整轮新调度拒绝；提高并发（遵守现有上限）或通过既有监控管理流程减少监控Node。不会自动修改监控或Gateway。
- disabled：采集开关关闭。
- unavailable/503：数据库、策略一致性或读取不可用，不能当作0个Node或ready。

计数包含0账号但符合资产能力/监控/Provider策略的Node。监控变化按当前slot生效条件评估，界面展示evaluated_slot/evaluated_at；grace已过时恢复须等待下一个正常5分钟槽。已存在任务按原lease/fencing结束，不伪造补采，Provider freshness/health维度不变。

## 发布和回滚

默认并发10的推导容量为20，原先依赖MAX_NODES=50/C10的部署不能原配置直接升级。先确认eligible计数，必要时显式提高并发，例如50 Node使用并发25；数据面资源预算需一并复核，不自动更改运行配置。

00019仅增加安全readonly function，不新增表/列，也不扩大runtime直接SELECT。隔离测试覆盖ACL与Down只删除函数；生产不执行Down。先迁移、再同版发布backend与Web；清理Ops Compose和持久override旧变量并备份配置。旧变量过渡期被忽略，不形成双容量来源。回滚旧镜像必须同时恢复其MAX_NODES和并发配置，数据和新只读函数保留。

本变更未部署到运行中的本地环境。发布需明确任务授权。

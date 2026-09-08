# Account Request Quality 最小采集

这是单实例 Control 的可选观察能力。CLIProxy 不修改；唯一事件来源是
`GET /v0/management/usage-queue?count=100`。队列是 destructive pop，
不得用该 endpoint 做无副作用健康探测，也不得与 CPA Manager Plus 或其它消费者同时消费同一个 Node。

## 启用条件

- 先由既有 migrator 流程应用 additive migration `00020_account_request_quality_events.sql`。
- 配置已有 CLIProxy management driver 和 Secret mapping，Node 处于 active monitoring 且支持 inventory capability。
- 确认单 Control 进程、每个 Node 的 queue 只有本 collector 消费。
- 设置 `CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED=true`。默认 false，不新增配置面板。

本次交付只实现和测试，未部署、未开启本地真实 Node 消费。运行命令需按独立发布任务执行。

## 固定运行行为

batch 100，正常poll 1s，错误指数退避最大30s；复用现有management HTTP预算，外层DB/操作预算30s。
每批前通过已有auth-files读取当前auth_index/provider/email，内存lookup不写历史表。
每30s重新读取当前active Node；停用/配置变更先取消旧loop，再允许新loop。目标读取失败停止现有loop。

数据库写入失败保留已规范化批次，提交前不再pop。重启后重放已提交事件会被
`(node_id,event_hash)` 去重。没有ACK/requeue，因此pop后HTTP响应丢失、或提交前进程崩溃
仍可能丢失事件；这是source限制，不能把统计当成无缺口计费账本。

每小时运行七天保留清理，DB时间判定，每次1000条，连续清理总预算30s。
如果到期数据量过大导致预算耗尽，剩余记录留待下轮；没有rollup或partition。

## 身份与错误

- event带充分provider/email：沿用inventory canonicalization。
- 仅auth_index：只有当前同Node快照exactly-one不同account_key且无冲突才归属。
- missing/deleted/ambiguous/conflicting：`account_key=NULL`。不使用first/latest wins或文件名fallback。
- Account Quality排除NULL；Node/Provider保留事件，并提供`unresolved_request_count`。
- 合法事件provider无法证明时为`unknown`，仍可纳入Node统计。
- malformed单项记录warning及数量，保留同批合法事件；HTTP/DB错误不转为空数据。

失败类别只有auth/quota/rate_limit/upstream/unknown。成功事件failure_class=NULL。

## 最小读取

Go repository：

```go
repo.AccountQuality(ctx, nodeID, accountKey, 15*time.Minute)
repo.AccountQuality(ctx, nodeID, accountKey, time.Hour)
repo.NodeProviderQuality(ctx, nodeID, "openai", time.Hour)
repo.NodeProviderQuality(ctx, nodeID, "", time.Hour) // 全 Node
```

也可使用已有runtime数据库身份执行受控只读函数（参数使用实际Node和既有canonical key）：

```sql
SELECT * FROM public.control_query_account_request_quality_v1(
    '00000000-0000-4000-8000-000000000001'::uuid,
    'openai:example@example.invalid', NULL, interval '15 minutes');

-- Node/Provider aggregate，包括unresolved_request_count
SELECT * FROM public.control_query_account_request_quality_v1(
    '00000000-0000-4000-8000-000000000001'::uuid,
    NULL, 'openai', interval '1 hour');
```

只允许15m/1h。零请求的success_rate/p95/最后事件字段为NULL，表示unknown；
DB失败为error。p95使用有duration的事件和percentile_cont(0.95)，缺失duration不作0。
无HTTP API/UI/Grafana，无quota/inspection/automation/Node patch。

## 停止与回滚

关闭该flag并重启Control，保留forward schema及已提交事件。不要为回滚运行破坏性Down。
停采期间Node队列受其原有保留/容量限制，恢复不保证补齐停采期间全部请求。

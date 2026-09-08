# Planning validation

## Current phase

仅规划，未实施、未写migration、未运行业务测试、未commit/push/deploy/archive。原A1已由用户决策关闭；Architecture契约复核通过，不将其等同于实施授权。用户明确要求本轮停在Architecture Review。

## Acceptance matrix（以下均为未来实施验收，不是已执行PASS）

| ID | Fixture / assertion | Verification |
| --- | --- | --- |
| A1 | provider=antigravity + runtime source=file；memory/unknown/其他Provider不误纳入 | Driver + PG/UI |
| A2 | 两个独立401/invalid_grant → TOKEN_INVALID Critical；一个失败+runtime error可确认 | normalize/evaluator PG |
| A3 | account_deactivated/disabled/suspended/blocked精确code → ACCOUNT_BLOCKED；普通403 → FORBIDDEN Warning | 白名单positive/negative |
| A4 | 单次401/403+active只pending UNKNOWN；不同reason/同hash回放不凑数 | PG |
| A5 | stale/最新degraded/不完整/Node管理401/采集失败 → UNKNOWN，不新告警 | PG/API |
| A6 | fresh disabled → DISABLED，无告警；不自动resolve已有ACTIVE | PG/UI |
| A7 | success严格新于最后故障→RESOLVED；同timestamp或更新冲突不恢复 | PG |
| A8 | 无流量两次相邻5分钟槽完整active才恢复；同source100次不凑数；中间失败/停机跳槽打断 | PG/race |
| A9 | A/B同account_key完全隔离；unresolved/event-only不造账号 | PG/API |
| A10 | 并发/restart/retry/未知commit最多一个ACTIVE；失败不推进checkpoint | 真实PG/race |
| A11 | 15m滑窗/7天retention/current poll外键NULL不虚假恢复或重复计数 | PG |
| A12 | 恢复水位以前旧event不重开；后续新确认故障有新UUID | PG |
| A13 | success/no-requests原taxonomy和Quality分类不变；auth子原因NULL legacy不回填 | normalize/PG兼容 |
| A14 | Token/raw/status_message canary不进入DTO/SQL参数/DB/API/log；只在边界丢弃原文 | parser/serializer/integration |
| A15 | runtime EXECUTE成功/direct SELECT拒绝/PUBLIC无EXECUTE；测试Down/Up保留旧数据与函数 | PG ACL/migration |
| A16 | 401/403授权、400cursor/limit、404Node、503DB与Unknown/Empty分开 | API/UI |
| A17 | Availability/Reason/Since六态，系统时区；Node切换取消，不加mutation | frontend |
| A18 | 100账号/10000events batch有界、>100账号不会永久饥饿，无N+1 | PG性能/调用计数 |
| A19 | lifecycle回调超时/cancel不影响Inventory、Binding、Duplicate、Gateway数据面 | wiring/race/regression |
| A20 | other/runtime unavailable持续15分钟以上/100次reconcile/restart仍UNKNOWN且零告警；旧ACTIVE遇UNKNOWN不重发、不升级、不虚假resolve | PG/race/UI，未来实施验收 |

## Planning commands

- `openspec validate add-antigravity-account-availability-monitoring --type change --strict --no-interactive`：PASS。
- `openspec validate --all --strict`：18/18 PASS。
- `git diff --check`：PASS；新文档另检查行尾空白。
- 本轮不运行PG/race/make test build；这些只列为批准后实施验收，未宣称已通过。
- `git status --short`：仅本change目录untracked，无产品或migration文件修改。

## Architecture Review

原A1冲突已解决。用户明确：“保持 UNKNOWN 永远不告警，第一版不做 other/runtime_unavailable 告警。”已同步proposal/design/spec/tasks及A20验收，明确三种occurrence reason与固定严重度，不保留UNKNOWN告警例外。

原独立Architecture Agent与主Agent复核除A1外未发现实质blocker。本轮主Agent逐项复核：UNKNOWN长期/重复/重启不创建、重发或升级告警；other不能进入故障确认计数；仅明确三reason有occurrence；历史ACTIVE与当前UNKNOWN分开，不篡改已确认历史或伪造恢复。正常化中的other安全枚举保留，但不是告警类型。runtime不可用仍可为明确认证失败提供旁证，不可单独产生故障。

## Review outcome

Architecture：PASS；blockers：none；Implementation：NOT STARTED。用户决策已落入规范，规划strict验证不代替业务实现测试。3项规划任务完成，所有实施/PG/race/make test build任务仍未执行。仅修改本change文档，不commit/push/deploy/archive；按本轮原始约束停在Architecture Review。

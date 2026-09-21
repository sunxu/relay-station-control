## MODIFIED Requirements

### Requirement: Node operations SHALL remain explicit and isolated

Control SHALL 仅提供 Node Health / Connection Test、immediate Monitoring Enable/Disable，并继续复用既有 CLIProxyAPI Driver、受保护 Control API、安全、审计和 metrics contract。Phase 10 SHALL 将这些 Node operation 的主要 presentation owner 从历史 Asset Registry 重组到 `/nodes` Relay Nodes surface；该 presentation migration MUST NOT 新增 Node/Gateway lifecycle、binding/account/credential/OAuth mutation、scheduled monitoring product API/UI、arbitrary HTTP client、scheduler、health-history subsystem或data-plane participation。

`/nodes` 页面 SHALL 继续要求所有 Health / Connection Test / Monitoring action 由管理员显式触发；page mount、reload、Sidebar navigation、Dashboard mount、Monitoring mount 与 Control startup MUST NOT 自动调用 Node Health / Connection Test，也 MUST NOT 新增后台 health polling。`/assets` 在 Stage 3B 完成后 MUST NOT 保留第二套可执行 Node operation controls，可仅保留 navigation-only Node 入口。

#### Scenario: Relay Nodes 页面加载与 Control 启动
- **WHEN** 用户进入/刷新 `/nodes`、打开 Dashboard/Monitoring，或 Control 启动
- **THEN** 不自动调用 Node Health / Connection Test，不新增后台健康任务，不修改 monitoring
- **AND** Gateway/CLIProxyAPI 数据面不受影响

#### Scenario: 显式 Node observation
- **WHEN** 管理员在 `/nodes` 明确点击 Health 或 Connection Test
- **THEN** 仅调用既有对应 Control API / Driver.Probe contract
- **AND** 不产生额外 scheduler、health history 或 data-plane responsibility

#### Scenario: 显式 monitoring action
- **WHEN** 管理员在 `/nodes` 明确执行 Monitoring Enable 或 Monitoring Disable
- **THEN** 继续使用既有 protected route、CSRF、command identity、receipt、audit 与 concurrency semantics
- **AND** Phase 10 不改变 underlying lifecycle state machine

#### Scenario: Asset auxiliary route has no duplicate Node actions
- **WHEN** Stage 3B 已完成且用户进入 `/assets`
- **THEN** `/assets` MUST NOT 同时提供 Health、Connection Test、Monitoring Enable/Disable 或 Node lifecycle mutation 的第二套执行控件

#### Scenario: 越界输入
- **WHEN** 请求包含 endpoint/path/method/headers/body/credential/schedule/cron/timezone/effective_from/expected_revision 等既有 operation contract 未声明字段
- **THEN** API 继续按既有规则拒绝且无 outbound 或 monitoring mutation

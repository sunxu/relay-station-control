## MODIFIED Requirements

### Requirement: 只读资产页面处理空状态与故障

Control SHALL 为已认证管理员提供受控的资产管理 presentation，展示 Environment、Gateway、Node、Driver/capability、当前 Provider Policy 和 monitoring state。Phase 10 MAY 将 presentation ownership 拆分为 `/assets` 辅助资产与运行配置入口和 `/nodes` Relay Node 主要页面，但所有页面 MUST 只调用 Control 的生成客户端，不得直接调用 Gateway、Node 或其他外部服务。

`/assets` SHALL 继续承载 Environment identity、Gateway lifecycle management、Driver catalog 与 Current Provider Policy；Gateway Stage 1 change 冻结的 Register/Edit/Retire/Replace/history contract MUST 保持。

`/nodes` SHALL 成为 Node lifecycle / monitoring 的唯一主要 presentation owner，并 MUST 保留 Stage 2/3 已冻结的 Node Register、Edit、Retire、Replace、retired history/lineage、Health、Connection Test、Monitoring Enable、Monitoring Disable、`expected_revision`、credential configured-state 与错误语义。Stage 3B 完成后 `/assets` MUST NOT 保留第二套可执行 Node lifecycle / health / monitoring 控件；可仅保留指向 `/nodes` 的 navigation-only 入口。

Health 与 Connection Test 均仅显式点击执行，各自独立展示安全结果且不写 browser storage；页面 mount/reload MUST NOT 自动触发任一 observation、不建立 health history、不后台轮询。Monitoring mutation 后丢弃旧 cursor 并重新读取 current detail/list；already_enabled/already_disabled 明确提示，future 冲突指向受控运维流程，retired 冲突刷新历史，command 冲突不得自动换 UUID 重做。无 future schedule picker、credential/account editor 或新 router framework。

`/assets` 与 `/assets/` 直达及 reload MUST 继续渲染 Asset Registry auxiliary surface；`/nodes` 与 `/nodes/` MUST 通过同一 SPA fallback 与 frontend route normalization 渲染 Relay Nodes surface。API router 与 `/static/` namespace 行为保持。

#### Scenario: 首次进入资产页面
- **WHEN** 已认证管理员打开资产页面
- **THEN** 页面按需加载 Environment、Gateway、Driver catalog 与 Current Provider Policy，并通过 Control API 获取数据
- **AND** 浏览器不向已登记 endpoint 发出直接请求

#### Scenario: 首次进入 Relay Nodes 页面
- **WHEN** 已认证管理员打开 `/nodes` 或 `/nodes/`
- **THEN** 页面展示 Node lifecycle / monitoring surface，并继续使用既有 Control generated client、CSRF、revision 与 credential configured-state contract
- **AND** 浏览器不直接请求 Node endpoint

#### Scenario: 注册表为空
- **WHEN** Environment/Gateway/Driver/Policy 或 Node API 返回合法空状态
- **THEN** 对应页面展示可区分的未登记、未配置或空状态，而不是构造默认资产或伪造成功状态

#### Scenario: API 读取失败
- **WHEN** 任一资产 API 返回可重试故障
- **THEN** owning 页面显示不含内部错误或 Secret 的失败状态和显式重试入口，不将旧数据冒充当前状态

#### Scenario: Gateway管理入口
- **WHEN** 管理员在 `/assets` 操作 Gateway
- **THEN** 复用既有生成客户端、CSRF、revision、command 与 credential contract
- **AND** Phase 10 不新增第二套 Gateway mutation owner

#### Scenario: Node lifecycle mutation 控件范围
- **WHEN** 管理员在资产页面发起 Node Register、Edit、Retire 或 Replace
- **THEN** 可执行控件位于 `/nodes` owning surface，并继续使用既有 lifecycle、revision、transaction 与 audit contract
- **AND** Stage 3 operations 通过独立的已声明路径执行，不改变这些 lifecycle 流程

#### Scenario: retired 历史详情导航
- **WHEN** 管理员在 `/nodes` 查看一个 retired Node 的详情
- **THEN** 页面显示其 predecessor/successor lineage 与 retirement metadata
- **AND** 不提供任何使其复活为 current 的操作

#### Scenario: explicit probe remains user-triggered
- **WHEN** 管理员仅打开或刷新 `/nodes`
- **THEN** 页面 MUST NOT 自动执行 Health 或 Connection Test
- **AND** 只有管理员显式点击对应操作才调用既有 probe/action endpoint

#### Scenario: Stage 3 operations additive surface
- **WHEN** active Node 管理员通过受保护的 Stage 3 路径执行健康或立即监控操作
- **THEN** 仅发生该 operation 规定的结果，Stage 1/2 lifecycle、安全、history 和分页语义均保留，retired 无可执行 operation

## ADDED Requirements

### Requirement: Account Quality SHALL 默认展示当前账号并允许生命周期过滤

Node Topology Account Quality UI MUST 默认请求 lifecycle=present，提供全部及现有四种生命周期选择。GET account-quality MUST 接受可选 lifecycle；省略参数 MUST 保持既有全部 Inventory 账号语义。生命周期过滤 MUST 在quality筛选和有界keyset分页之前作用于 Inventory 账号集合，不在浏览器分页后隐藏行。cursor MUST 绑定生命周期；改变筛选 MUST 重置页面cursor。不改变Quality分类、Inventory truth、History、Incidents、collector或数据面。

#### Scenario: 默认当前账号与零请求

- **WHEN** 打开某Node Account Quality，当前Inventory包含present与missing账号
- **THEN** 默认只查询present，其中零请求账号仍显示Unknown

#### Scenario: 显式查看缺失或全部

- **WHEN** 管理员选择missing或清空生命周期
- **THEN** 分别查询missing或全部Inventory账号，cursor重置，分页前过滤且不会漏页

#### Scenario: 兼容既有调用

- **WHEN** HTTP调用未传lifecycle或使用旧v1数据库读函数
- **THEN** 保持原全部Inventory账号行为；新v2仅新增安全只读查询权限，不赋予runtime直接SELECT

#### Scenario: 非法筛选或游标不匹配

- **WHEN** lifecycle非法或cursor绑定的lifecycle与当前请求不同
- **THEN** 返回400，不执行查询；数据库失败仍返回503，不伪装Empty或Unknown

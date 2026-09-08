## Context

现有两个页面均调用generatedTopologyApi.accountList并复用AccountList/AccountDetailsDrawer。AccountInventory额外提供email/basicStatus/page size和capacity；Topology提供Provider/Binding/Duplicate/Incident。仅删除重复展示层。

## Goals / Non-Goals

唯一/topology入口，保留所有有用筛选、容量诊断、详情与关联观察。非目标：新API、数据库、采集、身份、mutation、路由兼容、数据面变化。

## Decisions

- 移除App lazy旧页面及AuthRoute旧分支/导航/Topology旧链接；旧/account-inventory按现有未知路径规则进入通用管理或登录状态，不识别其instance_id，不重定向Topology。不新增404系统或alias。后端同名API保留。
- Topology账号区使用统一POST和AccountList，provider/email/basicStatus/lifecycle/window/quality/25|50|100每页；默认present/15m/All/25。筛选编辑清空结果、cursor和选择，点击查询后提交；支持上一页/下一页。Node选择或/topology?instance_id深链接加载该Node默认首页，StrictMode仅一次；没有Node不发账号查询。popstate同样按Node隔离，初始URL不覆盖后续选择。
- 敏感email和cursor仅组件内存及POST body；避免在TanStack query key/cache、URL、localStorage/sessionStorage持久保留。复用现有mutation与AbortController，离开/Node切换取消旧请求并清空详情；不让慢响应串Node。失败无自动重试，手动恢复保留当前筛选/页。
- 保留容量诊断的独立手动刷新、ready/capacity_exceeded/disabled及unavailable/401语义；这是当前环境而非所选Node独占容量。抽出小组件复用现有hook与projection，不重做计算。
- 保留Unknown与Unavailable区分、provider latest health与snapshot freshness、生命周期与质量独立、Incidents点击account_key进入同一抽屉。只读，默认present但允许missing等；错误类型仍区分400/401/403/404/409/503。
- 无后端事务/幂等/时间规则变化；UTC显示和既有审计保持。只读POST不是业务mutation。认证和CSRF继续通过现有API，不新增secret、metrics或日志。

## Risks / Trade-offs

- 有意移除旧书签，现行导航/runbook改为Topology；归档历史保持原样。
- 统一显式筛选改变Topology原下拉即查的交互，避免每次邮箱输入产生审计请求；自动加载只在Node进入/切换。
- 删除旧页测试前将有价值能力迁入Topology/容量组件/路由测试。保留API客户端兼容测试。
- 不重新运行无关PG/数据面验收；前端专项/typecheck/build以及仓库要求make test build覆盖最终交付。

## 1. Architecture review gates

- [x] 1.1 proposal/design/specs与acceptance matrix已获Architecture Contract Final Approval；原三项契约批准保持有效。
- [x] 1.2 P-READ已获Architecture Contract Final Approval：design第3节函数、Provider并集、缺state投影、ACL及Up/Down范围已冻结；已另获实施授权，不以架构批准代替实现验收。
- [x] 1.3 清点实际Control Binding HTTP消费者并记录breaking forward correction发布约束；验证内置Web之外是否有稳定兼容承诺，发现时停止并回到架构评审。

## 2. Existing Binding transport correctness prerequisite

- [x] 2.1 统一OpenAPI六个Account ID字段及所有嵌套read/write引用为GatewayAccountId string，保留nullable语义；用schema/examples检查证明不存在Control read=string/write=number。
- [x] 2.2 在既有Binding handler实施int64输出format与decimal string严格parse，store/DB/audit JSONB不变；用现有HTTP契约测试证明非法/overflow无store/action/成功审计，不改action/CSRF/事务语义。
- [x] 2.3 从统一OpenAPI重新生成Go API与TS client，调整已有consumer/fixtures；验证Account ID TS全部string，无Number/parseInt/parseFloat/一元+中转，不手改生成物。
- [x] 2.4 完成design I1–I6端到端精度矩阵：四个边界值穿过实际generated client、string UI state和existing Binding request逐字不变；overflow与非法表示被拒绝，不为测试新建生产Binding管理表单。
- [x] 2.5 验证I7–I9：旧numeric请求明确breaking、API/Web成对升级回滚、source v1 numeric→Go int64→persistence保持现状且Web输出string；确认identity修正不新增persistence migration且source版本/canonicalization零改动。

## 3. Duplicate historical involvement read path

- [x] 3.1 新增明确命名的history store read query/method，以现有evidence EXISTS匹配occurrence/Node，occurrence层去重分页；SQL验收D1/D2证明B移出current后A/B History仍有RESOLVED记录。
- [x] 3.2 定义并接线只读history HTTP DTO/operation与generated client，保持原instance_id current filter；契约测试验证involvement标记、status、独立cursor、400/503/no-store/认证及非super_admin拒绝。
- [x] 3.3 运行D3–D7合成DB场景：空current集合、多条evidence、absence/degraded历史涉及、source poll清理和重启；验证不新增membership/history表、索引或migration，不把historical involvement当owner proof。

## 4. Provider independent safe read model

- [x] 4.1 在Final Approval及实施授权后新增一个最小additive query-access migration；按最新序列编号，Up仅函数/owner/revoke/grant，Down仅DROP该uuid签名，不修改任何持久化对象或existing account v1 query。
- [x] 4.2 实现design第3节独立Provider函数：同一statement timestamp取当前monitored policy providers UNION全部held states，LEFT JOIN state；验收零账号、out_of_scope、缺state not-yet-observed与真正空集合，禁止依赖account rows。
- [x] 4.3 使用runtime连接及sqlc安全函数read接线独立Provider HTTP endpoint；九字段allowlist、共同observed_at、400/404/401/403/503与超时契约，不返回raw信息或失败providers=[]。
- [x] 4.4 验证SECURITY DEFINER/STABLE/fixed search_path/migrator owner/PUBLIC revoke/runtime EXECUTE；隔离DB验收runtime direct SELECT仍permission denied，Up/Down只影响新函数。
- [x] 4.5 完成P1–P8与P-READ A–H/J–L矩阵：双badge、两个时间、固定reason、fresh/stale与normal/degraded独立、缺state unknown及read unavailable；ownership eligibility/owners/occurrence保持不变。

## 5. Readonly Topology UI

- [x] 5.1 新增只读/topology导航、Node分页选择与稳定UUID深链接，复用assets与既有read APIs；App测试验证登录/401、直接访问、前进后退、未知Node，页面无Binding mutation imports或candidate submit。
- [x] 5.2 实现Inventory evidence、Binding truth、四态resolution、current duplicate/History四区；组件测试验证来源独立、last-known明确、Account ID为string、未绑定Node保留及无总健康红绿灯。
- [x] 5.3 接线Current/Resolved/History不同读取与独立evidence分页；组件验收D2显示B current为空但History有记录，affected_nodes仍标作current集合。
- [x] 5.4 实现独立loading/empty/unavailable/retry和过期响应丢弃，刷新/重启只重读；测试局部503、A→B乱序、UTC时间、内存清理，不存在mutation replay。
- [x] 5.5 保留既有账号清单的Node-only deep-link，确认当前无Binding管理UI时不展示链接；R1网络/组件验收证明无bind/rebind/unbind/其他业务写入或数据面调用。
- [x] 5.6 完成桌面/390px/键盘与身份安全验收，记录合成截图；两个Provider badge可辨识，account_key只管理员文本展示，不进入URL/storage/logs/metrics，不泄露Secret。

## 6. Implementation acceptance

- [x] 6.1 实施获准后运行D/P/P-READ/I/R矩阵和对应隔离DB/HTTP/浏览器验证，捕获证据；按实际结果勾选。
- [x] 6.2 实施获准后运行项目要求的完整make test build及适用检查，核实既有失败基线；仅记录专项通过不得代替完整验收，生成物可复现且source不变且migration仅为获准的P-READ query-access范围。

## 7. Evidence and reconciliation

- [x] 7.1 将D1–D7/P1–P8/P-READ A–H/J–L/I1–I9/R1–R5逐项关联命令、fixture、响应与截图，区分契约要求和实际通过证据；P-READ未获批准或未实现验收、或外部兼容依赖未解决不得标功能完成。
- [x] 7.2 更新Control Runbook与本change compatibility说明：read-only、current/history、双维度、Web string/source numeric边界、breaking升级回滚和无效入口禁用；不跨仓擅改source架构。
- [x] 7.3 最终运行OpenSpec strict与git diff --check，检查文档/代码一致；保留既有规范文件改动，记录git status及本change范围，禁止混入Secret/运行数据/未经授权实现，全部验收完成后才另行归档。

本change保持0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。实施已获授权；验收结果见planning-validation.md，实现按用户授权分批提交，发布尚未执行。

Release Gate本地检查依据见[release-compatibility.md](release-compatibility.md)：用户指定当前父目录覆盖相关项目，全部本地consumer枚举完成，无C/D类consumer；历史retention精确断言专项通过，证据更新完成，恢复27/27。等待Final Release Gate Re-review，不archive。

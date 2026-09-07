## ADDED Requirements

### Requirement: Gateway Account identity SHALL 在Control Web HTTP边界无损统一

作为existing Binding transport identity correctness fix，所有Control对Web暴露的Gateway Account ID SHALL使用规范正int64十进制JSON string，范围1至9223372036854775807。read API、candidate API、binding detail、bind/rebind request、所有嵌套mutation response、generated Go API boundary与generated TypeScript client MUST使用同一string契约；可空response ID仅允许string或null。

OpenAPI MUST使用共享string schema、minLength=1、maxLength=19、pattern=`^[1-9][0-9]{0,18}$`；服务端MUST额外严格验证int64上限。内部Go/store/sqlc与DB MUST继续使用int64，现有内部audit JSONB numeric约束不变；HTTP若投影audit Account ID MUST转为string。

#### Scenario: 全部read和write一致
- **WHEN** 通过Node/Gateway-centric/unresolved读取，或通过既有bind/rebind/unbind取得binding/previous_binding
- **THEN** Account ID及所有嵌套context均为string，bind/rebind body也为string，不出现read=string/write=number

#### Scenario: Nullable ID
- **WHEN** 当前读模型没有Account ID
- **THEN** 保留原允许的null，不伪造零或空串；有ID时只能输出decimal string

### Requirement: Account ID SHALL 在浏览器与服务端之间逐字round trip

TypeScript Account ID、React option/form/state与request serialization MUST使用string，MUST NOT使用number、Number、parseInt、parseFloat、一元+或numeric JSON中转。Server MUST严格base10解析为int64再执行既有操作，非法或overflow MUST返回既有400 validation_failed且无store/action/成功审计；MUST NOT truncate/round/wrap。此修正不新增Topology mutation UI或改变既有事务、CSRF、审计和action语义。

#### Scenario: Precision boundary四个值
- **WHEN** 分别使用9007199254740991、9007199254740992、9007199254740993、9223372036854775807
- **THEN** DB/source internal int64→Control JSON string→真实generated TS client→UI string→request string→server int64逐字不变，9007199254740993绝不变为9007199254740992

#### Scenario: Overflow
- **WHEN** 请求Account ID为字符串9223372036854775808
- **THEN** 服务端返回400 validation_failed，不调用store，不截断/舍入/溢出回绕

#### Scenario: Invalid representation
- **WHEN** 请求提供numeric JSON、零、负数、前导零、+号、空白、空串、小数、指数或必填null
- **THEN** 服务端拒绝而非转换后接受；合法旧numeric客户端也不能被隐式保留

### Requirement: Control Web identity correction SHALL 明确breaking compatibility

现有numeric read/write变string SHALL标记为BREAKING并按forward contract correction统一切换API与生成Web客户端，不提供number|string双轨。仓库内未发现稳定external消费者不等于外部已确认不存在；发布前MUST清点实际消费者，发现兼容承诺时MUST停止发布并重新评审，不能静默放行numeric。该任务属于existing Binding compatibility prerequisite，MUST NOT将mutation责任交给Topology。

#### Scenario: 旧numeric请求
- **WHEN** 旧客户端对纠正后的API发送numeric Account ID
- **THEN** 明确拒绝，不让旧请求以潜在舍入值继续执行；发布/回滚使用匹配的API和Web版本

#### Scenario: Topology入口暴露前
- **WHEN** Topology即将展示或链接带Account ID的既有入口
- **THEN** 必须先完成全部Control/Web transport修正验收；不存在的Binding管理UI不得为此新建

### Requirement: Gateway Directory source v1 SHALL 保持既有边界

本change MUST保持Gateway Directory source schema_version=1、numeric JSON Account ID、Go int64严格ingestion和existing persistence/canonicalization semantics。该source链路不经过JavaScript，MUST NOT因其numeric而保留Web numeric，也MUST NOT在本change升级source v2或新增相关migration。未来source string ID/schema_version2/dual-read/migration契约必须由独立Gateway Directory architecture change决定。

#### Scenario: Source numeric到Web string
- **WHEN** Gateway source v1返回numeric ID 9007199254740993
- **THEN** Control Go无损解码并持久化int64；对Web输出字符串"9007199254740993"，既不改source版本也不向Web直接透传number

#### Scenario: Database与source不变
- **WHEN** 应用Control/Web transport correction
- **THEN** 不改数据库ID类型、内部audit JSONB、source schema_version/canonicalization；identity correction不需要persistence migration。本change保持0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration

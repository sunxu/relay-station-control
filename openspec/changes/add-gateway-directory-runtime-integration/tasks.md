## 1. Architecture and contract review

- [x] 1.1 评审runtime生命周期、直接HTTP/HTTPS、无证书验证的Directory专用transport与Secret配置、默认关闭和既有预算不变。
- [x] 1.2 评审NULL reader reference补填、Gateway行锁、audit与migration/rollback边界；批准后才进入实施。

## 2. Controlled initial configuration

- [ ] 2.1 实施最小forward migration及固定审计入口，保持既有register不变。
- [ ] 2.2 复用deploy/asset-registry惯例添加SQL registrar模板（不新增CLI/API/UI），以受保护输入传递reference，明确actor、NULL前置条件和幂等输出。
- [ ] 2.3 真实PG验证首次补全、非NULL冲突、no-op、非法输入、全部历史拒绝与audit原子回滚。
- [ ] 2.4 真实PG验证runtime/PUBLIC拒绝、并发补全/首次调度/Binding、锁超时及Up/Down兼容历史。

## 3. Directory runtime integration

- [ ] 3.1 实现enabled/映射两项配置校验与独立SecretResolver，disabled零副作用。
- [ ] 3.2 接线ReconcileTick→WorkOnce及受限runtime pool、取消和等待；复用RunGatewayOnce内ScheduleCurrent，不额外调用ScheduleTick，验证多Gateway慢/失败前项后续仍处理与当前槽去重。
- [ ] 3.3 接线既有metrics并验证状态、固定标签与日志脱敏。
- [ ] 3.4 真实PG及进程测试验证180s/540s预算、restart、两个实例、lease loss、unknown commit、shutdown。
- [ ] 3.5 落实独立5s attempt及最多5s失败收尾context，验证慢header/body/finalize、取消、失lease和unknown commit，不延长成功或lease预算。
- [ ] 3.6 在共同ScheduleCurrent行锁内实现NULL no-work，验证补填竞争两种顺序、A未配置/B成功与A凭据失败/B成功；保留历史run恢复。

## 4. HTTP and HTTPS deployment

- [ ] 4.1 Directory client直接接受HTTP/HTTPS，移除目标许可检查并在Gateway/Node管理专用transport关闭证书验证；验证自签名/未知CA/过期/主机名不匹配可用、Node同策略、范围外客户端不变、双协议redirect拒绝与握手失败不降级。
- [ ] 4.2 验证独立reader token解析、缺失/错误/轮换和失败不刷新观测，执行敏感canary扫描。
- [ ] 4.3 随7.2统一交付ops本地HTTP私网origin、独立token和public拒绝流程；不新增自定义CA或证书部署流程；不改Gateway source和Node服务端代码。
- [ ] 4.4 在隔离部署验证HTTP及不验证证书的HTTPS成功、公网拒绝、Control enable/disable/restart与现有服务健康。

## 5. End-to-end acceptance

- [ ] 5.1 真实HTTP与HTTPS source v1执行首次采集、changed/unchanged、失败和恢复，核对DB current与观测时间。
- [ ] 5.2 通过既有candidate/bind/read验证decimal string精确身份与BOUND/resolved；无action时不得自动绑定。
- [ ] 5.3 验证Directory故障/stale不清除Binding，恢复后resolved；Gateway→Node调用独立可用。
- [ ] 5.4 复用已有transport/PG fixtures并参数化HTTP/HTTPS，允许多验收ID引用同一测试但保留逐项断言；运行必要生成及项目make test build，按变更范围运行PG、TLS、进程验收；只做相关页面smoke，不扩大为Topology重新开发。

## 6. Unified Gateway and Node management transport

- [x] 6.1 评审统一出站契约、Node既有SSRF/证书保证移除及配置退役和回滚影响。
- [ ] 6.2 修改Node专用transport及配置，移除目标许可/特殊IP/DNS重绑定检查和证书验证，复用普通拨号；不改固定接口或Secret契约。
- [ ] 6.3 使用合成fixture验证HTTP、各类不可信HTTPS、DNS变化/特殊地址、旧变量退役及无代理/redirect/预算/响应/Secret回归，不访问真实元数据服务。
- [ ] 6.4 随7.2统一更新Control/ops部署资料，并在规范同步时修正Node Purpose保证，核对范围外入站和数据面配置无变更，记录版本回滚条件；纳入最终evidence reconciliation后才关闭change。

## 7. Evidence and closure

- [ ] 7.1 将验收矩阵逐项关联fixture、命令、断言和实际结果，不把planning validation当实现证据。
- [ ] 7.2 一次更新Control/ops Runbook及部署模板，合并4.3/6.4的HTTP、旧变量退役、补填、默认关闭、部署顺序、回滚和限制说明；各任务引用同一交付物。
- [ ] 7.3 执行OpenSpec strict与diff检查，复核生成物、工作树和独立提交范围，等待Implementation/Release Review后另行归档。

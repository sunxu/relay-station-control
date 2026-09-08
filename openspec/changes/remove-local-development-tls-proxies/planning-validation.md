# Planning Validation

2026-09-09，Control baseline f25eb89，ops baseline 4e4be1e，两仓起始干净。最初用户批准本地移除TLS并执行，随后明确要求暂停开发；当前授权仅继续规划修订，实施和部署均暂停，不push/archive。Gateway Directory当前内部HTTP、Node两个HTTP入口保留。未读取或记录Secret值。

## Simplified Architecture Decision

2026-09-09，用户接受移除 CONTROL_COOKIE_SECURE、不新增 CONTROL_DEV_ALLOW_INSECURE_HTTP。冻结dev固定HTTP、staging/production固定HTTPS；复用现有Cookie/CSRF/同源机制，不增加传输策略系统。旧变量不再解析或影响结果；开发环境不再单独选择HTTPS。本地容器非loopback由dev策略允许，宿主仅loopback映射由ops负责。

本轮仅修订proposal/design/spec/tasks与本证据文档，代码和runtime未改变。7项任务中仅规划项完成，6项实施/验收/部署任务保持未完成。不得把设计strict通过当成实现测试通过。

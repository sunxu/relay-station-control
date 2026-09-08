## 1. Contract

- [x] 1.1 明确默认 present 与显式查看 missing/全部，完成 change strict 验证

## 2. Implementation

- [x] 2.1 修改页面默认值，保留既有全部生命周期入口与显式交互
- [x] 2.2 覆盖深链接、手动查询、missing/全部切换及 cursor 重置

## 3. Validation

- [x] 3.1 页面专项测试及 make test build
- [x] 3.2 strict、diff check 和范围检查，记录证据

## 4. Topology Account Quality

- [x] 4.1 可选生命周期 API、cursor绑定、query-access v2与生成客户端
- [x] 4.2 Quality页面默认present，保留其它/全部，过滤后分页
- [x] 4.3 真实PG过滤/分页/ACL/DownUp与API错误/游标、Web测试
- [x] 4.4 make test build、相关race、OpenSpec strict、证据对账

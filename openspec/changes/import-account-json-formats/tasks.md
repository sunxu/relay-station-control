## 1. 契约与覆盖

- [x] 1.1 审阅并批准 proposal/design/specs、保守 1 MiB/100 账号边界和 coverage review；以 OpenSpec strict 与人工逐条审阅为完成依据。

## 2. 后端窄范围校验

- [x] 2.1 先增加 service 校验测试，再实现 proxy_url/alias 格式及 upload_new-only disabled 校验；验证合法协议/认证/IPv6、非法类型/端口/query/fragment/control、双 alias、剩余 denylist 与 Replace disabled 拒绝。
- [x] 2.2 补 HTTP/service 接受边界证明：非法代理/disabled 无 reservation/operation/receipt/native mutation；合法字节进入既有 HMAC，改变代理或 disabled 导致同命令 conflict，精确重放不 redispatch；运行 owning Go tests 与现有 PostgreSQL integration。

## 3. JSON 转换与上传 UI

- [x] 3.1 先写 synthetic parser tests，再实现双格式纯转换；验证身份来源/规范化/重复/边界、字段映射、expiry instant、project ID precedence、proxy_key 关联/认证编码/IPv6、disabled 默认与 strict boolean，禁止配置不混入。
- [x] 3.2 删除邮箱输入，增加安全只读预览、逐账号结果与本地化说明；component tests 验证文件不匹配身份仍用 JSON、结果顺序和真实状态、重复点击零额外调用、生成 File <=1 MiB。
- [x] 3.3 实现解析 generation 与固定 Node 提交上下文、取消/401/卸载清理；delayed Promise focused tests 验证 A→B、Node 切换、401 中断余下项、迟到响应不恢复 Secret，非 401 保留 session。

## 4. owning 验证与完整回归

- [x] 4.1 迁移既有 upload Browser 身份输入路径并加精简双格式/partial-success/canary proof，所有交互 getByTestId；验证 Control-origin、CSRF、无自动 retry/replace/probe 与 URL/storage/DOM/log Secret 泄漏。
- [x] 4.2 形成 clean committed candidate（提交前确认授权），在既有 repository-owned upload acceptance 中证明 Browser→Control→真实 PostgreSQL→Node 的单账号和批量路径，记录 candidate/artifact identity；缺 runtime 时明确 blocked，不用 mock 替代正式 full-chain PASS。
- [x] 4.3 运行 make test build、translation gates、相关 Accounts Browser 与其余 Account Operations 兼容性检查；验证生成链无漂移，Gateway/Node/DB/OpenAPI 未改。

## 5. 收尾

- [x] 5.1 独立复核真实 diff 与 coverage matrix，修复 P0/P1/P2 finding；将规范、测试、实现逐项核对并记录实际计数，不把计划测试写成已 PASS。
- [x] 5.2 捕获 validation evidence、更新 durable 契约与任务状态，git diff --check 和 tracked worktree 检查；保留用户样例与 swap，不提交 Secret，不自动 push/archive/deploy。

## 6. 项目 ID 优先级修订（2026-10-12 用户授权）

- [x] 6.1 先补失败回归，再取消双 project ID 不同的拒绝分支，保持 project_id 优先及原有备用字段回退；用真实文件做只读内存解析，复核代理/disabled 不变，运行 make test build、OpenSpec strict 与 generated drift 检查并追加本轮证据。不修改来源文件，不上传真实账号，不移动已有镜像/tag。

## 7. 只读指纹密钥兼容修订（2026-10-12 用户授权）

- [x] 7.1 TDD 证明 0400/0600 密钥读取通过、其它权限/类型/长度/缺失/符号链接拒绝，修正共享读取校验；证明 HMAC/文件内容与权限不变。
- [x] 7.2 使用 0400 密钥运行真实 PostgreSQL account-operations HTTP 集成及 production wiring，完成 make test build、strict/generated/diff 检查，记录本轮证据；不修改部署密钥、Ops/Node/Gateway source，不自动发布镜像或部署。

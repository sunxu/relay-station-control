## ADDED Requirements

### Requirement: Management endpoint UI SHALL validate internal HTTP URLs

Gateway 与 Node management endpoint 表单 MUST 使用同一小型 internal HTTP validator，而不是 public-Web URL validator。该 validator MUST 接受 single-label Docker DNS、`host.docker.internal`、域名、IPv4、可选合法端口及满足后端既有约束的 base path；MUST 拒绝 HTTPS、其它 scheme、缺失 scheme/host、userinfo、query、fragment、control character、无效 port 与不安全 encoded path。UI validation 仅提供即时反馈，Go admission 与 PostgreSQL durable constraints 仍是权威安全边界。

#### Scenario: Docker DNS hostname
- **WHEN** 管理员输入 `http://gateway:8080` 或 `http://node:8317`
- **THEN** 表单接受该 endpoint，且不显示 public-Web URL 格式错误

#### Scenario: OrbStack host alias
- **WHEN** 管理员输入 `http://host.docker.internal:8080`
- **THEN** 表单接受该 endpoint

#### Scenario: HTTPS 与 malformed endpoint
- **WHEN** 管理员输入 `https://gateway:8080`、`ftp://gateway`、`gateway:8080`、userinfo、query、fragment 或 malformed endpoint
- **THEN** 表单拒绝并给出固定 HTTP-only 安全提示

# Internal HTTP Transport

## ADDED Requirements

### Requirement: Control-managed management endpoints are HTTP-only

Control-managed Gateway Directory and CLIProxyAPI management/health endpoints in local, staging, and production SHALL use `http://`. Gateway→Relay Node AI endpoints, Gateway generic Account/upstream transport, Sub2API Account `base_url`, generic HTTP clients, external HTTPS, OAuth, payment, update/download APIs, PostgreSQL, and Redis are outside this requirement.

#### Scenario: Control management calls in every environment
- **WHEN** a supported deployment environment configures a Control→Gateway Directory or Control→CLIProxyAPI management endpoint
- **THEN** the endpoint uses HTTP and no production-only HTTPS/TLS requirement rejects it

#### Scenario: HTTPS internal endpoint is rejected
- **WHEN** current Control runtime is configured with `https://` for Gateway Directory or CLIProxyAPI management/health
- **THEN** Control configuration/client construction rejects it before any request is sent

#### Scenario: Deployment templates are HTTP-only
- **WHEN** a current deployment template generates a Control→Gateway Directory or Control→CLIProxyAPI management endpoint
- **THEN** the generated endpoint is `http://` and the template does not expose an internal HTTPS/TLS option

#### Scenario: Gateway upstream scheme remains native
- **WHEN** a Gateway Account/upstream uses a scheme supported by Sub2API
- **THEN** this change does not reject, rewrite, classify, or otherwise alter that endpoint based on Relay Station topology

#### Scenario: Gateway Directory authentication over private HTTP
- **WHEN** Control calls `GET /internal/v1/api-account-directory` over private HTTP with the dedicated `relay_control_reader` token
- **THEN** the request is authorized only for that exact route and method

### Requirement: Internal network and route isolation remain mandatory

Deployments MUST restrict Control-managed management listeners to private Docker/VPC/firewall/Security Group paths. Public ingress MUST reject `/internal/v1/*`; the dedicated service token, Management Key, Secret injection, no-proxy transport, redirect rejection, timeouts, response limits, and logging redaction MUST remain.

#### Scenario: Public internal route denial
- **WHEN** a public Gateway ingress receives any `/internal/v1/*` request
- **THEN** it is rejected regardless of HTTP method or token

#### Scenario: Non-exact Directory access
- **WHEN** a caller uses another path/method or lacks the dedicated token
- **THEN** access is denied and no broader management read/write is granted

### Requirement: Browser ingress is independent

Browser/external ingress to Control SHALL retain its existing HTTPS, Secure Cookie, same-origin, CSRF, HttpOnly, SameSite, and MFA behavior where configured. Internal HTTP standardization MUST NOT remove or weaken browser ingress protections.

#### Scenario: Production browser ingress
- **WHEN** a production browser connects through the existing HTTPS ingress
- **THEN** Secure Cookie and existing authentication/CSRF policy remain enabled independently of internal HTTP calls

### Requirement: No data-plane or credential behavior change

The change MUST NOT modify Gateway Account/Group routing, Relay Node scheduling, CLIProxyAPI credentials, request semantics, PostgreSQL/Redis protocols, or persist/expose secrets. Internal HTTP SHALL NOT be described as providing TLS-equivalent confidentiality.

#### Scenario: Data-plane isolation
- **WHEN** an internal HTTP call fails or its transport is changed from internal HTTPS
- **THEN** Gateway/Node request scheduling and credential state remain governed by their existing systems and Control remains observational

### Requirement: Rollback uses an older release, not current dual-protocol support

The current runtime MUST NOT accept internal `https://` for rollback compatibility. Historical HTTPS behavior MAY remain only in old released artifacts, Git history, archived deployment configuration, or certificate backup material; rollback SHALL deploy the old application/deployment version.

#### Scenario: Rollback to historical HTTPS release
- **WHEN** operators need to restore the historical HTTPS architecture
- **THEN** they roll back to the old application/deployment artifact, while the current version continues rejecting internal `https://`

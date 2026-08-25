# Administrator access scenario traceability

Date: 2026-08-25
Change: `add-control-authentication-foundation`
Source: `openspec/changes/add-control-authentication-foundation/specs/administrator-access/spec.md`

This is the final scenario-mapping snapshot for the change. `Direct` means a named
automated test exercises the scenario's decisive boundary. `N/A` means the condition
does not exist in this architecture and the table records the enforcing evidence.
There are no partial, unmapped or pending scenarios.

## Scenario-to-test matrix

| Requirement | Scenario | Status | Current evidence or remaining gap |
|---|---|---:|---|
| Bootstrap | 生产环境完成首次 bootstrap | Direct | `web/e2e/authentication.spec.ts` real lifecycle; `TestAdministratorActivationCreatesAuditedMFAServiceSession` covers the same MFA/session primitives. |
| Bootstrap | bootstrap 在 MFA 确认前中断 | Direct | `TestBootstrapConcurrentStartCreatesOnlyOnePendingFlow` reconstructs the service over the same database, resumes the single enrollment, then safely resets it; UI resume/reset is covered by `App.test.tsx`. |
| Bootstrap | bootstrap Secret 无效 | Direct | `TestBootstrapAndActivationRollbackAndCompletedIrreversibility` rejects wrong and missing-file Secrets, creates no administrator and scans failure audit for the Secret; container acceptance covers missing/unsafe files. |
| Bootstrap | 并发 bootstrap | Direct | `TestBootstrapConcurrentStartCreatesOnlyOnePendingFlow` sends two concurrent starts and proves one pending identity and one success audit. |
| Bootstrap | 已完成后重复 bootstrap | Direct | `TestBootstrapCompletionCannotBeReopened`; Playwright verifies completed status after restart. |
| Bootstrap | 重启和应用回滚不重开 bootstrap | Direct | `TestBootstrapAndActivationRollbackAndCompletedIrreversibility` proves the completed database state cannot reopen; Playwright restarts the process and verifies `completed`. Compatible application rollback preserves this database-enforced boundary. |
| Administrator lifecycle | 创建后续管理员 | Direct | `TestAdministratorActivationCreatesAuditedMFAServiceSession`; Playwright second-administrator flow. |
| Administrator lifecycle | 激活后续管理员 | Direct | `TestAdministratorActivationCreatesAuditedMFAServiceSession`; Playwright activation and ten recovery codes. |
| Administrator lifecycle | 激活令牌无效或过期 | Direct | `TestLockActiveActivationTokenByDigestFiltersStateAndLocks`; service lifecycle verifies consumed/revoked token rejection. |
| Administrator lifecycle | 禁用管理员 | Direct | `TestRuntimeRoleDisableAdministratorHTTPAndLastAdminGuard`; `TestRuntimeRoleCanDisableSecondAdministratorButNotSafetyGuardOrLastAdministrator`; Playwright disable flow. |
| Administrator lifecycle | 保护最后一个可用管理员 | Direct | `TestLastEnabledAdministratorIsProtected`; runtime-role store and HTTP tests cover self-disable, direct last-admin attempt and unchanged state. |
| Administrator lifecycle | 禁止角色和服务身份登录 | Direct | `TestAuthenticationSchemaConstraints`; HTTP route matrix rejects `Authorization: Bearer` service material as an administrator session. |
| Password authentication | 合法密码进入第二认证阶段 | Direct | Service lifecycle produces a challenge and Playwright completes password then TOTP. |
| Password authentication | 非生产环境未强制 MFA | Direct | `TestDevWithoutRequiredMFASignsSessionWhileProductionRequiresChallenge` proves dev with explicit MFA disablement signs an authenticated `none`-MFA session without a challenge, while the production configuration creates only an MFA challenge. |
| Password authentication | 账号维度失败限制 | Direct | `TestConcurrentAuthenticationFailuresReachAccountThreshold`; rolling-window store tests. |
| Password authentication | 来源维度失败限制 | Direct | `TestAuthFailureRollingWindowIsConcurrentAndRestartSafe`, boundary test, fixed source metric labels. |
| Password authentication | 成功登录后的失败计数 | Direct | `TestSuccessfulMFALoginClearsAccountFailuresButPreservesSourceFailures` creates both windows, completes recovery-code MFA, then proves the account window is deleted while the source window remains. |
| Password authentication | 修改密码 | Direct | `TestAdministratorActivationCreatesAuditedMFAServiceSession` verifies old password/session rejection and new password flow. |
| TOTP/recovery | 注册 TOTP | Direct | `TestAdministratorActivationCreatesAuditedMFAServiceSession`; Playwright enrollment/confirmation. |
| TOTP/recovery | 首次显示恢复码 | Direct | Service lifecycle and Playwright assert ten codes; `App.test.tsx` verifies one-time DOM lifecycle. |
| TOTP/recovery | 使用 TOTP 登录 | Direct | Service lifecycle, RFC tests and Playwright TOTP login. |
| TOTP/recovery | 重放 TOTP 验证码 | Direct | `TestTOTPReplayAndCodeValidation`; `TestVerifyAndConsumeTOTPConcurrentReplay`. |
| TOTP/recovery | 使用恢复码 | Direct | Service lifecycle and Playwright recovery-code login with remaining count. |
| TOTP/recovery | 恢复码并发消费 | Direct | `TestRecoveryAndActivationTokensAreConsumedOnce`. |
| TOTP/recovery | 重新生成恢复码 | Direct | `TestBootstrapAndActivationRollbackAndCompletedIrreversibility` injects recovery-code insert failure, proves the old batch remains usable and no success audit commits, then verifies successful rotation. |
| TOTP/recovery | 生产环境绕过 MFA | Direct | `TestConfigFailClosed`; session authorization rejects missing required MFA assurance. |
| Session/CSRF | 完成登录后签发会话 | Direct | Service lifecycle authenticates stored digest/CSRF and verifies audit; production cookie unit test. |
| Session/CSRF | 缺失或错误 CSRF 证明 | Direct | `TestVerifyCSRFRejectsArbitraryNonEmptyProof`, `TestCSRFAndReauthenticationProofsAreSessionBoundAndExpire`, generated-header/route tests, and same/cross-origin policy tests cover missing, arbitrary, cross-session, rotated-old and cross-origin proofs. |
| Session/CSRF | 空闲超时 | Direct | `TestSessionLookupRetainsOldKeyAndRejectsExpiredAndRevokedRows`. |
| Session/CSRF | 绝对超时 | Direct | `TestSessionLookupRetainsOldKeyAndRejectsExpiredAndRevokedRows`. |
| Session/CSRF | 注销 | Direct | Service lifecycle rejects revoked session; idempotent OpenAPI/HTTP case; DB-failure test proves no premature cookie clearing. |
| Session/CSRF | 会话令牌轮换 | Direct | Service lifecycle covers reauthentication/password rotation and old-token behavior. |
| Session/CSRF | 数据库或认证状态不可用 | Direct | `TestNewServiceFailsClosedWhenSessionGaugeCannotLoad`, logout DB-failure HTTP test and fail-closed UI cover the management plane; final phase-0 acceptance kept the same two Nodes/six accounts healthy while acceptance Control/PostgreSQL/TLS were stopped. |
| High risk | 成功重新认证 | Direct | Service lifecycle reauthenticates with a recovery code and verifies rotated session. |
| High risk | 重新认证已过期 | Direct | `TestHighRiskOperationsRequireDatabaseFreshReauthentication` and `TestCSRFAndReauthenticationProofsAreSessionBoundAndExpire` cover database expiry, cross-session proof copying, revoked/expired sessions and forged in-memory freshness. |
| High risk | 缺少操作原因 | Direct | `TestDisableAdministratorRejectsMissingOrShortReasonWithoutDatabaseWrites` verifies missing and sub-10-character reasons return the fixed 400 envelope with `no-store`/stable request ID, leave administrator/session state unchanged and write no success or request-correlated audit row. |
| High risk | 会话变化使证明失效 | Direct | `TestCSRFAndReauthenticationProofsAreSessionBoundAndExpire`, fresh-session DB locking and service lifecycle cover cross-session, expiry, logout/password/disable revocation and revoked rows. |
| Authorization | 未认证访问管理 API | Direct | `TestHTTPAuthenticationRouteMatrixAndSecurityEnvelope`; OpenAPI security boundary tests. |
| Authorization | 已禁用管理员使用旧会话 | Direct | Runtime disable HTTP test verifies target session revocation; service lifecycle rejects disabled-session access. |
| Authorization | 服务身份访问交互 API | Direct | HTTP route matrix rejects bearer service credentials; schema fixes `auth_source=local`, `role=super_admin`. |
| Authorization | 健康检查保持匿名 | Direct | `TestHealthz`; route matrix and OpenAPI contract. |
| Authorization | Control 认证服务故障与数据面隔离 | Direct | Final `control-auth-e2e.sh all` phase-0 check verified the same two Nodes/six accounts and zero cross-Node duplicates before and during acceptance Control/PostgreSQL/TLS outage, without modifying Node state. |
| Audit | 状态变更与审计原子提交 | Direct | `TestBootstrapAndActivationRollbackAndCompletedIrreversibility` injects audit failures into bootstrap completion, activation and MFA reset and proves state/secret material/success audit roll back together; recovery-login rollback is covered separately. |
| Audit | 认证失败审计不泄露账号 | Direct | `TestUnknownAndExistingAccountsShareAuthenticationFailureOutcome`; fingerprint/redaction tests. |
| Audit | Secret 与令牌脱敏 | Direct | `TestAuthenticationCanaryDoesNotLeakAcrossHTTPAuditOrMetrics`, bootstrap rejection audit scanning, structured-log redaction and audit allowlist tests cover every emitted surface in this build. Trace is `N/A`: no application trace exporter or trace emission path exists, and Playwright trace capture is disabled. |
| Audit | 审计不可通过产品 API 修改 | Direct | `TestAuditLogsAreImmutableForProductSQL`; `TestProductRuntimeRoleHasLeastPrivilegeAndCannotMutateAudit`; no update/delete API exists. |
| Audit | 进程重启后审计关联保持稳定 | Direct | Stable request-ID unit/canary correlation; UUID persistence store test; Playwright restart. |
| Observability | 记录成功与失败指标 | Direct | Metrics/Prometheus tests and service lifecycle attempt counters use closed labels. |
| Observability | 指标后端不可用 | N/A | This build has no external metrics backend/exporter. Authentication and audit commit against PostgreSQL before the independent scrape handler reads `AuthMetrics`; metric recording errors are deliberately ignored. Prometheus collector and service lifecycle tests verify the non-authoritative boundary. |

## No-Secret evidence checklist

Evidence artifacts must record only command outcome, fixed test name/status, bounded HTTP status/error code, fixed audit action/result, container UID/GID/mode and aggregate resource figures. They must never record raw request/response bodies from secret-bearing steps.

| Surface | Automated or review evidence | Current disposition |
|---|---|---:|
| HTTP error/success responses | `TestAuthenticationCanaryDoesNotLeakAcrossHTTPAuditOrMetrics`; uniform envelope/no-store tests | Verified for synthetic canary across login response; expand canary to each one-time response only through in-memory assertions. |
| Audit rows | Same cross-surface canary test; `TestAuditDetailsAllowlist` | Verified: fixed action/result/details and keyed fingerprints only. |
| Structured/text logs | `TestSensitiveRedactionAcrossTextAndStructuredLogs` | Verified for the redaction helper; production log collection must keep body/header logging disabled. |
| Metrics | Canary test plus descriptor/label and Prometheus collector tests | Verified: only environment/operation/result/dimension fixed labels; no IDs, names, IPs or request values. |
| Trace | Acceptance Playwright disables trace; no application trace exporter or trace emission path is configured | N/A for this change. Enabling tracing later requires a new synthetic-canary scan before deployment. |
| PostgreSQL token material | Token/service tests and schema columns use HMAC digests; TOTP uses versioned AES-GCM | Verified by unit/integration behavior; evidence must query lengths/state, never dump bytea/ciphertext. |
| Browser one-time material | `App.test.tsx` DOM/navigation/bfcache tests; Playwright trace/screenshots/video disabled | Verified for UI lifecycle. Never preserve Playwright failure body, storage state or HAR for secret-bearing flows. |
| Container startup failures | `control-auth-e2e.sh` scans bounded startup output and reports only case labels | Verified by container acceptance; raw Docker logs remain outside repository and must be deleted after review. |
| Runtime files | Acceptance records only `uid:gid:mode`; runtime directory is outside workspace | Verified for synthetic files. Never commit file paths that disclose deployment topology or any file content. |
| Documentation and committed evidence | This file and `authentication-foundation-acceptance.md` contain bounded summaries only | Review with targeted secret-pattern scan before commit; do not treat generic entropy-like hashes as safe evidence. |

## Final review gates

1. Task 11.4's traceability criterion is satisfied: every specification Scenario has at least one named validation artifact and there are no partial or pending rows.
2. Run tests with synthetic unique identities and sources so retries cannot inherit rate-limit state.
3. Keep raw Playwright/Docker output in the external runtime directory; retain only this bounded summary.
4. Scan staged evidence for the named synthetic canaries and for obvious credential keys before commit. Do not print candidate matches into CI logs.

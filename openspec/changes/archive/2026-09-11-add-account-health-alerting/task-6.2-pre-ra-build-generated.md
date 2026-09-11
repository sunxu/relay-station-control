# Task 6.2 — PRE-RA Build / Generated Gate

日期：2026-09-11。HEAD：`eab99a93bd2e16c83835b64da3566295ca8f8fee`。
Stage 0 formal external Review：PASS；原有未提交 Stage 0 diff 保留。只执行 6.2，不重跑 6.1，不启动 6.3 或正式 P5-RA-001..086。

## Environment and commands

使用仓库 DevRAM temp/npm/XDG 配置、GO111MODULE=on、GOPROXY=https://goproxy.cn、Node 24。本轮新建独立 Go cache：`/Volumes/DevRAM/tmp/phase5-62-bJpk8f/go-build`，未删除其它缓存。测试日志及 diff comparison artifacts 位于同一 repo-external 目录。未设置 owner/runtime DB test URL，按 6.2 scope 不重建 6.1 / Stage 0 DB harness；不连接开发或业务数据库。

1. `make generate`：PASS，exit 0。
2. 捕获 `git diff HEAD`，再次 `make generate`：PASS；两次 diff 的 `cmp` PASS。
3. `cd tools && go test ./... -count=1 -v`：PASS，11 pass results；OpenAPI exhaustive expected/actual 45/45，逐 operation 与 count 检查均保留。
4. `go test ./internal/drivers/gatewaydirectory -run '^(TestManagementHTTPOnly|TestManagementHTTPSRejectedBeforeRequest)$' -count=1 -v`：2/2 PASS。Gateway management HTTP-only / HTTPS rejected before request；DingTalk direct HTTPS contract 未修改。
5. 正常环境 `make test build`：PASS，exit 0；完整执行 generate、tools Go tests、repository Go tests、frontend tests、typecheck、frontend build、Go build。
6. 同正常环境补充 `go test ./... -count=1 -json`：PASS，exit 0，用于 exact skip inventory，不使用缺 URL 的 skip 声称 PostgreSQL PASS。
7. build 后 `make generate`：PASS；生成前后完整 diff `cmp` PASS，且与首次生成后的 diff 相同。Final generator fixed-point PASS。
8. `git diff --check`：PASS。

## Expected generated synchronization

仅 generator 输出以下两个文件，没有手工修改：

| File | Exact generated delta |
|---|---|
| internal/api/api.gen.go | AccountInventoryItem.Email、AccountInventoryQueryRequest.Email、NormalizedAccountEmail 三处注释 |
| web/src/api/generated/control.ts | NormalizedAccountEmail 一处 JSDoc description |

全部来自 Stage 0 已审 `NormalizedAccountEmail.description`：邮箱为非 Secret 普通业务身份，批准的 authenticated business surfaces/audit 可完整使用；不用于 URL 或 Prometheus/Alertmanager labels。Type、JSON fields、schema shape、enum、path、operation ID、HTTP method、client method、server interface、validation constraint 均不变。

Unexpected generated semantic changes：NONE。没有改 sqlc、OpenAPI source、migration、production logic 或其它 generated file。Generation idempotent：PASS。

## Gate results and skips

- Formal make：31 Go packages PASS（含 tools），6 packages 无 test files。
- Repository uncached inventory：30 packages PASS；1394 test/subtest pass results，0 fail；465 expected test skips，不计为 PASS。
- Tools uncached：11 pass results，0 fail/skip。
- Expected skips：442 EXPECTED EXTERNAL/DB；23 EXPECTED OPT-IN。下节列出全部 package/test/subtest 与 gate reason。
- 6 个 package-level skip 为 `[no test files]`：deploy/acceptance/account-inventory-history-rollback、deploy/acceptance/account-inventory-lifecycle-rollback、deploy/acceptance/account-inventory-readonly-query-postgres-recovery、deploy/acceptance/account-inventory-snapshot-postgres-recovery、internal/store/sqlc、internal/webui；不是测试漏跑。
- Unexpected skips：0。已知 `TestLocalDirectoryToolingNaturalRecovery` 仍由 `RELAY_DIRECTORY_TOOLING_E2E=1` opt-in 控制。
- Frontend：28 files / 184 tests PASS；typecheck PASS；frontend build PASS；Go build PASS。
- `bin/control` 为生成的 arm64 Mach-O executable；`bin/control` 和 `internal/webui/dist/index.html` 均命中既有 ignore policy，没有 tracked build artifact。

### Initial diagnostic invocation and correction

首次为采集 skip 设置命令级 `GOFLAGS=-v` 的 make invocation exit 2：该变量被 synthetic scanner 的 `go run` 子进程继承，编译 package name 进入 CombinedOutput，触发 `TestLifecycleRunnerScansSyntheticArtifactWithoutExternalConfiguration` 固定输出断言。分类：C，诊断环境干扰；不是 Stage 0 regression、44/45 baseline 或产品缺陷。未修改源码/测试；移除该变量后精确测试 `-count=1 -v` PASS，重新执行原始 `make test build` exit 0。Skip 最终清单由不设置 GOFLAGS 的 uncached JSON run 取得。

最终 Go build 输出过一条宿主 module download stat-cache 写入权限 warning；命令仍 exit 0，binary 已实际生成。未调整权限、缓存位置或源码。前端既有 jsdom pseudo-element warning 不影响 184 tests PASS。

## Scope and status

Stage 0 文件内容保留；6.2 仅新增两个 generated comment synchronization、本文和 tasks.md 的 6.2 checkbox。无 test log、cache、DB data、coverage、Playwright output 或 Secret 加入仓库。没有手改 generated clients。

6.2 CLOSED；真实 checklist 45/50；仅剩 6.3、6.4、6.5、6.6、6.7 OPEN。新 P0/P1/P2 blockers：0/0/0（self-review，不冒充正式外部 Review）。READY FOR FORMAL REVIEW。

Runtime Acceptance：NOT STARTED。RA-GO：NOT YET DECLARED。Real DingTalk messages：0。Commit：NONE。Push：NONE。

## Exact expected skip inventory

来自本轮正常环境的 uncached JSON test events；没有将 skip 计为 PASS。

### cmd/control

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestCrossNodeDuplicateOwnershipProductionTriggerCatchesTimeOnlyStaleness | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestCrossNodeDuplicateOwnershipProductionWiring | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestGatewayDirectoryMainDeploymentHTTP | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestGatewayDirectoryRuntimeProcessRecovery/http | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestGatewayDirectoryRuntimeProcessSameSlotCompetition | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestLocalDirectoryToolingNaturalRecovery | EXPECTED OPT-IN | set RELAY_DIRECTORY_TOOLING_E2E=1 |
| TestNewDatabasePoolUsesUTC | EXPECTED EXTERNAL/DB | set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL |

### deploy/acceptance/account-inventory-history-postgres

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAccountInventoryHistoryPostgresPlannerCatalogGate | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/coverage_boundaries | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/finalize_transaction_atomicity | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/incomplete_segment_publication_gates | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/last_segment_concurrent_completion | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/planner_and_provider_aggregation | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/planner_eligibility | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresRollupPublicationMatrix/UTC_eligibility_boundary | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |
| TestAccountInventoryHistoryPostgresSchemaSmoke | EXPECTED EXTERNAL/DB | PostgreSQL history acceptance is not enabled |

### deploy/acceptance/account-inventory-history-process

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAccountInventoryHistoryProcessClaimRetainedUntilLeaseExpiry | EXPECTED OPT-IN | history process held transaction acceptance is not enabled |
| TestAccountInventoryHistoryProcessDisabledCompatibleMetrics | EXPECTED OPT-IN | history process acceptance is not enabled |
| TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource | EXPECTED OPT-IN | history process acceptance is not enabled |
| TestAccountInventoryHistoryProcessHeldTransactionPoolExhaustion | EXPECTED OPT-IN | history process held transaction acceptance is not enabled |
| TestAccountInventoryHistoryProcessHeldTransactionTimeoutIsAtomic | EXPECTED OPT-IN | history process held transaction acceptance is not enabled |
| TestAccountInventoryHistoryProcessMetricsFailureIsolation | EXPECTED OPT-IN | history process acceptance is not enabled |
| TestAccountInventoryHistoryProcessReconcilerRecoveredClaim | EXPECTED OPT-IN | history process held transaction acceptance is not enabled |
| TestAccountInventoryHistoryProcessSeedClaimedForRestart | EXPECTED OPT-IN | history process restart seed acceptance is not enabled |
| TestAccountInventoryHistoryProcessSeedEligibleSource | EXPECTED OPT-IN | history process seed acceptance is not enabled |
| TestAccountInventoryHistoryProcessSourceBackedRetentionCompleted | EXPECTED OPT-IN | history process source-backed retention acceptance is not enabled |
| TestAccountInventoryHistoryProcessStaleFenceHasZeroImpact | EXPECTED OPT-IN | history process stale fence acceptance is not enabled |
| TestAccountInventoryHistoryProcessTerminalInternalPreservesSource | EXPECTED OPT-IN | history process terminal internal acceptance is not enabled |

### deploy/acceptance/account-inventory-history-shell

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestHistoryAcceptancePostStartupFailureCleansResources | EXPECTED OPT-IN | set CONTROL_HISTORY_FAILURE_CLEANUP_ACCEPTANCE=1 for the Docker failure-cleanup gate |

### internal/api

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAccountAvailabilityHTTPReadContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAccountInventoryHTTPRepositoryRetentionNullSourceAndInconsistentCurrentState | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAccountQualityHTTPReadContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAccountQualityIncidentsHTTPReadContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAccountRequestHistoryHTTPReadContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAssetRegistryHTTPRuntimeReadsPaginationSecurityAndRecovery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAssetRegistryRejectsForgedExpiredAndRevokedSessions | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestAuthenticationCanaryDoesNotLeakAcrossHTTPAuditOrMetrics | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |
| TestCrossNodeDuplicateOccurrenceHTTPReadOnly | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestDevHTTPLoginSessionAndCSRF | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestDisableAdministratorRejectsMissingOrShortReasonWithoutDatabaseWrites | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestDurableJobHTTPReadOnlySecurityPaginationAndRecovery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestEnvironmentCookieAndOriginPolicy | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |
| TestHTTPAuthenticationRouteMatrixAndSecurityEnvelope | EXPECTED EXTERNAL/DB | set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL |
| TestInventoryPollCapacityHTTP | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestLogoutDatabaseFailureReturns503WithoutClearingSessionCookie | EXPECTED EXTERNAL/DB | set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL |
| TestProblemAccountsPOSTHTTPContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestRelayBindingHTTPCompleteSuite | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestRuntimeRoleDisableAdministratorHTTPAndLastAdminGuard | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestSameOriginPolicyRejectsCrossSiteAndMissingProductionProof | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |
| TestTopologyHTTPReadContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |
| TestUnifiedAccountPOSTContracts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL |

### internal/auth

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAdministratorActivationCreatesAuditedMFAServiceSession | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests |
| TestBootstrapAndActivationRollbackAndCompletedIrreversibility | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestBootstrapConcurrentStartCreatesOnlyOnePendingFlow | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestCompleteLoginMFASameTimeStepOnlyOneSucceeds | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestConcurrentAuthenticationFailuresReachAccountThreshold | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests |
| TestCSRFAndReauthenticationProofsAreSessionBoundAndExpire | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestDevWithoutRequiredMFASignsSessionWhileProductionRequiresChallenge | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestHighRiskOperationsRequireDatabaseFreshReauthentication | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |
| TestMFAChallengeBindsSourceRejectsExpiryReplayAndRetainsOldKey | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |
| TestNewServiceFailsClosedWhenSessionGaugeCannotLoad | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests |
| TestNewServiceInitializesActiveSessionGaugeFromDatabase | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests |
| TestRecoveryLoginRollbackKeepsChallengeAndCodeUsable | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestSecurityRejectionsUseIndependentSanitizedAuditActions | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL auth integration tests |
| TestSessionLookupRetainsOldKeyAndRejectsExpiredAndRevokedRows | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |
| TestSuccessfulMFALoginClearsAccountFailuresButPreservesSourceFailures | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run isolated authentication acceptance tests |
| TestUnknownAndExistingAccountsShareAuthenticationFailureOutcome | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL |

### internal/dingtalk

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestDingTalkObservabilitySecurityPostgres/oversized_response | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestDingTalkObservabilitySecurityPostgres/raw_business_rejection | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestDingTalkObservabilitySecurityPostgres/retry_budget | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkAcceptedCrashReconcilesAndFreshWorkerSucceeds | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkExpiredRunningBothPolicies | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkExpiredRunningReplayGuards/cancel | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkExpiredRunningReplayGuards/deadline | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkExpiredRunningReplayGuards/exhausted | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkRetryWaitCancellationAndDeadlineGuards | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkTimeoutResetBothPolicies/reset | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkTimeoutResetBothPolicies/timeout | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |
| TestRuntimeDingTalkUnknownReplayBudgetAndStableSnapshot | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL for PostgreSQL runtime tests |

### internal/drivers/cliproxyapi

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAccountInventoryLifecycleCanariesTraverseDriverWorkerArtifacts | EXPECTED OPT-IN | lifecycle canary artifact acceptance is opt-in |

### internal/environment

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestVerifyDatabaseIdentityAndRecovery | EXPECTED EXTERNAL/DB | set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL |

### internal/historyruntime

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestHistoryLocalOutputsExcludeCanaries | EXPECTED OPT-IN | history canary acceptance is opt-in |
| TestHistoryProductionSourcesHaveNoDirectNetworkImports | EXPECTED OPT-IN | history canary acceptance is opt-in |

### internal/inventorypoll

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAccountInventoryLifecycleCanariesTraversePolicyRaceRollbackArtifacts | EXPECTED OPT-IN | lifecycle canary artifact acceptance is opt-in |

### internal/store

| Test / subtest | Classification | Gate reason |
|---|---|---|
| TestAccountAvailabilityACLAndMigrationPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityBatchPerformanceAndPaginationPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityConfirmationPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityFailuresMustFollowSuccessWatermarkPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityFreshnessDisabledAndConcurrencyPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNodeIsolationPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationActiveStableAndResolvedOnce | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationConcurrentReconcileAndRecurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationForbiddenTupleAndDiagnosticSilence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationNoBackfillAfterDisabledEnable | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationPaginationZeroAndMultipleTransitions | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationRenameUsesTransitionSnapshots | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationTransitionIsTransactional | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationWriteFaultsRollbackAllEvidence/async_job_events | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationWriteFaultsRollbackAllEvidence/async_jobs | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityNotificationWriteFaultsRollbackAllEvidence/operation_outbox | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityRecoveryEvidencePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountAvailabilityRecoveryRestartAndRetentionPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryDailyRollupNoProviderAtomicFinalize | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryDailyRollupPolicyBoundaryResetAndCoverage | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryAggregateSchemaConstraintAndProtectionGateMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryAuditExactAllowlistAndRetentionBoundary | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCapacityAcceptance | EXPECTED OPT-IN | history capacity acceptance is opt-in |
| TestAccountInventoryHistoryCapacityOneTenFifty/nodes_1 | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCapacityOneTenFifty/nodes_10 | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCapacityOneTenFifty/nodes_50 | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCompactionClaimRenewReclaimFencing | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCompactionMainPathAndRecovery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCompletionCountMismatchFailsClosedAndRetainsPolls | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryConcurrentRetentionQueryPromotionAndScope/retention_first | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryConcurrentRetentionQueryPromotionAndScope/scope_first | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryCrashRecoveryMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryExpiredLeaseRequiresReconcileAcrossCompactionPhases | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryFailedShapesAndProviderDayBound | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryLeaseExpiryWhileWaitingForRunLock | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryMigrationBackfillsHealthWithoutHistoryOrIdentityCopy | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryMigrationEmptyDownUpAndChecksumGolden | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryPlannerLimitOneMakesPersistentProgress | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryPlannerSerializesRetentionBoundary | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryPollRetentionChildFailuresRollbackAndResume | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryPollRetentionRejectsIneligibleCandidates | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryResumeDeleteNeverReaggregatesResidualSource | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryRetentionBatchesConservationAndCurrentQuery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryRetentionEligibilityBoundaries | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryRetiredDaySerializesLatePollInsertion | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistorySchemaACLAndProtectedDown | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistorySecurityBoundaryMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistorySensitiveCanaryDatabaseSinks | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistorySnapshotDeleteSelectionBoundariesAndNoLateInsert | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistorySummarizeWriteFailuresAreAtomic | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryHistoryZeroPollLineageCompletesAcrossRetentionCutoff | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleActualRolePermissionsAndControlledRead | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleAllowsOneEmptyProviderPolicySet | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleCanariesTraverseSQLParameterArtifact | EXPECTED OPT-IN | lifecycle canary artifact acceptance is opt-in |
| TestAccountInventoryLifecycleCapacityOneTenFifty | EXPECTED OPT-IN | lifecycle capacity acceptance is opt-in |
| TestAccountInventoryLifecycleCapacityThousandAccounts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleCommitUnknownReplayIsSingleState | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleConcurrentFinalizeAndScopeTransition/finalize_first | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleConcurrentFinalizeAndScopeTransition/scope_first | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleConcurrentOldAndNewSlotsAvoidDeadlock | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleConcurrentSlotsAdvanceOnlyNewestOnce | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleConsecutiveMissingRecoveryAndSaturation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleFailedPromotionAndAtomicRollbackDoNotChangeAccounts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleFakeDriverRealStoreSequence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleFencingExpiryAndReplayAreIdempotent | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleMigrationDoesNotBackfillSnapshotHistory | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleMigrationRejectsPreexistingFuturePolicyBinding | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecyclePermissionsAndProtectedWrites | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleProviderOutOfScopeAndPartialReactivation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleScopeAuditAloneProtectsDown | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleScopeTransitionAuditRollsBackWithState | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleSkipMatrixPreservesMissingProgress | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleUncommittedTerminationRollsBackMissingAndUpsert/missing | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryLifecycleUncommittedTerminationRollsBackMissingAndUpsert/upsert | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryPollCapacityExpiredRetryRemainsBounded | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryPollCapacityRuntimeReadIsSchedulerCompatible | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryPollCapacityWorkerSlowDriver | EXPECTED OPT-IN | slow worker capacity acceptance is opt-in |
| TestAccountInventoryPollMigrationDownRefusesEvidence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryPollRuntimeRoleHasNoDirectWriteSurface | EXPECTED EXTERNAL/DB | set CONTROL_RUNTIME_DATABASE_TEST_URL to run product-role integration tests |
| TestAccountInventoryPollSchemaRejectsInvalidSlotsDuplicatesAndAbandonedEvidence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryProviderStateExactFreshnessBoundary | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryProviderStateQueryExpectedHeldAndHealthBadges | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryProviderStateQueryMigrationRollback | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryProviderStateQueryRuntimeEnvelopeAndACL | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryProviderStateQueryStaleHealthAndHeldOutOfScope | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQueryAuditCommitAndDisconnectSemantics | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQueryCapacityOneTenFifty | EXPECTED OPT-IN | readonly query capacity acceptance is opt-in |
| TestAccountInventoryReadonlyQueryDatabaseFaultsFailClosedAndRecover | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQueryMigrationEmptyDownUpRestoresCompatibility | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQueryMigrationPreservesExistingLifecycleState | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQueryRuntimeAndUnauthorizedPermissionMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQuerySeesOnlyCommittedPromotionsScopeAndRollback | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventoryReadonlyQueryStoreAndPermissionMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventorySnapshotLegacyV5UpgradePreservesUnevaluatedPromotion | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventorySnapshotMigrationDownRefusesPromotionEvidence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountInventorySnapshotRuntimeRoleCannotWriteEvidence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountQualityIncidentsACLAndRollbackPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountQualityIncidentsBoundaryPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountQualityIncidentsErrorsPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountQualityIncidentsPerformancePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountQualityIncidentsPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestHistoryACLAndRollbackPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestHistoryBoundaryAndStableKeysetPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestHistoryOpaqueHashPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestHistoryPerformancePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestHistoryPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestHistoryReadFailuresPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestQualityHTTPCollectorPostgresClosedLoop | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestQualityRepositoryErrorsAndTargets | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestQualityRepositoryPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestQualityRepositoryPostgresPerformance | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountRequestQualityTargetsUseActiveCapabilityTruth | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthActiveInvalidPrecedenceAndRecoveryPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthAvailabilityOtherReasonsDoNotImplyInvalidPostgres/account_blocked | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthAvailabilityOtherReasonsDoNotImplyInvalidPostgres/forbidden | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthEmptyAccountsAndIncompleteSourcePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthProjectionTTLAndExpectedValidUntilPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthQualificationGatesRemainConservativePostgres/missing | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthQualificationGatesRemainConservativePostgres/monitoring_disabled | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthQualificationGatesRemainConservativePostgres/out_of_scope | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthQualificationGatesRemainConservativePostgres/provider_degraded | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthQualificationGatesRemainConservativePostgres/stale_source | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAccountTokenHealthQualificationGatesRemainConservativePostgres/unsupported_source_mode | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAntigravityAvailabilityMetadataFinalize | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistrarCanOnlyUseControlledWriteAndReconciliationFunctions | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistrationIsAtomicIdempotentAndCapabilityBound | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistryBoundSecretIsAbsentFromPGStatActivity | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistryConcurrentMonitoringEnableIsIdempotent | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistryConcurrentPolicyActivationSerializesPerScope | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistryIdentityEndpointAndSecretConstraints | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAssetRegistryProtectedDownPreservesEnvironmentIdentity | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAuditLogsAreImmutableForProductSQL | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAuthenticationSchemaConstraints | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAuthenticationSchemaIsPresent | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAuthenticationTimestampsUseTimeZoneAwareColumns | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAuthFailureRollingWindowExcludesExpiredBoundary | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestAuthFailureRollingWindowIsConcurrentAndRestartSafe | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestBootstrapCompletionCannotBeReopened | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestBootstrapResetAuditUUIDSurvivesPendingAdministratorDeletion | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateAbsenceResolveReopenHistoryRegressionPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateEvidenceWriteAmplificationRegressionPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateMaterialCheckpointPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateMaterialStaleAndRecoveryPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationDisabledEnableDoesNotBackfill | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationEnqueueFailureRollsBackAllRows/async_job_events | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationEnqueueFailureRollsBackAllRows/async_jobs | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationEnqueueFailureRollsBackAllRows/operation_outbox | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationMembershipOnlyDoesNotCreateIntent | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationProblemsMembershipAndAvailabilityCoexist | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationRecurrenceStartsNewChain | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationSuccessMakesJobEventOutboxVisible | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateNotificationUsesTransitionTimeRenameSnapshot | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipAlertObserver | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipAlertObserverDefaultIsNoop | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/concurrent_add/remove:_a_newly_eligible_owner_and_a_newly_absent_owner_both_land_without_a_lost_update | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/concurrent_first_detect:_two_workers_race_the_same_account_key,_only_one_ACTIVE_occurrence_survives | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/concurrent_refresh/refresh:_two_workers_reconfirm_the_same_membership,_no_lost_update | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/concurrent_refresh/resolve:_the_later_lock_holder_re-reads_source_truth,_never_un-resolves | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/concurrent_reopen:_two_workers_see_a_fresh_duplicate_after_RESOLVED_history,_only_one_new_ACTIVE_occurrence_is_created | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/insert-race_path_is_deterministically_exercised:_the_losing_worker_actually_re-selects_FOR_UPDATE_and_re-evaluates | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/reconciler_vs_normal_worker:_concurrent_Reconcile_and_Evaluate_on_the_same_ACTIVE_duplicate_converge_on_one_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipConcurrency/reconciler_vs_normal_worker:_concurrent_resolve-eligible_pass_never_leaves_the_occurrence_reverted_to_ACTIVE | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipEvidenceEvaluationMigrationUpDownUp | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipEvidenceEvaluationPolicyOnlyTimestampGuard | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipEvidenceEvaluationRuntimePrivileges | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotGuardMigrationUpDownUp | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotRace | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipIndependentFromNodeLocalDuplicates | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/3+_Node_duplicate_is_one_occurrence,_not_pairwise | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/A/B_duplicate_+_B_fresh_absent:_remove_then_resolve | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/A/B_duplicate_+_B_stale:_degrade,_no_resolve,_affected_set_unchanged | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/A/B/C_duplicate_+_C_fresh_absent:_removed_but_still_ACTIVE_(2_remain) | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/A/B/C_duplicate_+_C_stale:_no_removal,_evidence_state_degraded,_still_ACTIVE | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/add_owner:_A/B_duplicate_then_C_becomes_fresh_present_joins_the_same_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/degraded_observation_cites_health_scheduled_at_and_a_NULL_source_poll_run_id | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/detect:_first_duplicate_creates_ACTIVE_occurrence_with_full_affected_set | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/invalid_input_is_rejected_without_touching_the_database | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/new_eligible_Node_not_owner_confirmed_at_evaluationAt_must_not_join_the_affected_set | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/no_candidate:_single_owner_produces_no_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/refresh:_membership_unchanged_appends_evidence_without_creating_a_new_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/reopen:_RESOLVED_history_does_not_block_a_brand-new_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/rollback:_a_mid-transaction_failure_leaves_no_partial_occurrence/evidence_row | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipLifecycle/zero-evidence_degraded_pass:_retained_Node_unclassifiable_this_pass,_latest_evaluation_id_preserved | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipMetricsRepository | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipMigrationDownFailsClosedWithHistory | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipMigrationUpDownUp | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipOccurrenceReadModel | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipQuery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipQueryAccessMigrationUpDownUp | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipQueryExplain | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipQueryRuntimePrivileges | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/ACTIVE_A/B/C_occurrence_with_C_fresh_absent_during_downtime:_reconcile_shrinks_to_A/B,_still_ACTIVE | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/ACTIVE_occurrence_with_a_Node_fresh_absent_during_downtime:_reconcile_resolves_it | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/ACTIVE_occurrence_with_a_Node_stale_during_downtime:_reconcile_degrades,_does_not_resolve | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/idempotent:_repeated_reconciliation_against_unchanged_truth_never_duplicates_state | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/invalid_environment_id_is_rejected_without_touching_the_database | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/restart_with_a_duplicate_that_first_appeared_during_downtime:_reconcile_creates_the_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipReconciliation/restart_with_an_existing_duplicate:_reconcile_reuses_the_existing_ACTIVE_occurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipRuntimePrivilegeMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestCrossNodeDuplicateOwnershipSchemaFoundation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDingTalkCanonicalPayloadEnqueue | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDingTalkCatalogCleanInstallAndForwardUpgrade/clean_install | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDingTalkCatalogCleanInstallAndForwardUpgrade/forward_upgrade | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDingTalkDuplicateDisplayNamesEnqueue | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobAtomicEnqueueCanonicalHashAndLifecycle | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancelAfterUnknownRetryIsNotSafeCancellation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceARunningKnownNoEffectWithCancel/policy-false | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceARunningKnownNoEffectWithCancel/policy-true | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceBCurrentUnknownBeforeCommit/cancel-after-final-attempt-result | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceBCurrentUnknownBeforeCommit/cancel-race | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceBCurrentUnknownBeforeCommit/max-attempts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceCUnknownThenKnownNoEffectTwoCancelOrders/cancel-after-known-no-effect-commit | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceCUnknownThenKnownNoEffectTwoCancelOrders/cancel-before-known-no-effect-commit | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceCurrentUnknownAfterCommit | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceD2UnknownAfterVerifyReset | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceDVerifyAbsentResetsUnknown | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceEOrdinaryRetryWaitCancellation/policy-false | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceEOrdinaryRetryWaitCancellation/policy-true | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceFDirectSuccessAfterCancel/unknown-policy-false | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceFDirectSuccessAfterCancel/unknown-policy-true | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceGNeedsVerificationWithCancel/unknown-policy-false | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceGNeedsVerificationWithCancel/unknown-policy-true | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceHPermanentFailureWithCancel/unknown-policy-false | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceHPermanentFailureWithCancel/unknown-policy-true | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceHReasonSequenceNotPolicyOrError/known-reason-wins-over-error-code | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceHReasonSequenceNotPolicyOrError/unknown-reason-wins-over-error-code | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceLockWaitRechecksCommittedCancellation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidencePendingPreservesRequestSemantics | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/expired-lease | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/invalid-role-and-reason | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/prior-unknown-explicit-cancel | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/reserved-cancel-reason | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/stale-fence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/without-cancel | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobCancellationEvidenceRunningCancelledGuardMatrix/without-proof | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobClosedConstraintMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobConcurrentClaimsFencingAndRecoveryPaths | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobDatabaseDeadlineClosesUnstartedWork | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobDatabaseRejectsSensitiveAndMismatchedPayload | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobDirectSuccessNeedsPersistedAuthorizationAndFence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobDirectSuccessRejectsExpiredToken | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobExecutionPolicyCatalogAndJobInvariants | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobExecutionPolicyDownIsForwardOnly | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobExecutionPolicyEnqueueSnapshotAndSameKeyConflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobExecutionPolicyForwardMigrationDefaultsAndOldJobs | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobExpiredUnknownPolicyRecoveryAndUnsafeBounds | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobIdempotencyAtomicBundleAndCancellation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobIndexesAndPublicReadSnapshot | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobProtectedDownEachEvidenceClassPreservesPriorSchema/event | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobProtectedDownEachEvidenceClassPreservesPriorSchema/job | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobProtectedDownEachEvidenceClassPreservesPriorSchema/kind | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobProtectedDownEachEvidenceClassPreservesPriorSchema/outbox | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobProtectedDownRequiresEmptyEvidenceTables | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobRecoveryFencingBudgetsAndOutboxCrash | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobRuntimeCatalogAndStateBypassMatrix | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobRuntimeRoleHasMinimumPrivileges | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobUnknownEvidenceSurvivesLaterKnownNoEffectRetry | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobUnknownReplayBudgetAndCancellationAreDBBounded | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestDurableJobWorkerDirectSuccessPersistsWorkerEvent | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryAndRelayBindingMigrationsUpDownUp | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryCoordinatorNoWorkAndShapeValidation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryCoordinatorSourceTimeInvalid | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryCoordinatorSuccessChangedAndUnchanged/changed | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryCoordinatorSuccessChangedAndUnchanged/unchanged | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryFailureBookkeepingHonorsParentCancellation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryFailureBookkeepingIsBoundedAndReconciles | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryHistoricalNullRunIsReconciledWithoutNewSchedule | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryMetricsSnapshotAndCollector | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialConcurrency/binding_and_fill_share_gateway_lock | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialConcurrency/different_references_one_winner | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialConcurrency/fill_holds_gateway_lock_before_schedule | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialConcurrency/registrar_lock_timeout | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialConcurrency/schedule_holds_NULL_lock_before_fill | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialConfiguration | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/audit_failure_rolls_back_reference | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/binding_history_conflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/different_reference_conflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/directory_history_conflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/failed_terminal_run_history_conflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/invalid_actor_and_reference | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/snapshot-only_history_conflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderInitialRejectionMatrix/succeeded_terminal_run_history_conflict | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryReaderMigrationRoundTrip | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRecoveryEvidence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRepositoryWorkflowAndRecovery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeAttemptTimeout/body | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeAttemptTimeout/headers | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeFinalizeDeadlineAndRecovery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeFrozenBudgets | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeSourceLifecycle/tls=false | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeSourceLifecycle/tls=true | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeUnconfiguredNoWork | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryRuntimeUnknownCommitRecovery | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectorySchemaFoundation | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectorySensitiveValuesAreNotReflected | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestGatewayDirectoryTargetReadAccess | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestHistoryMetricsBacklogIncludesUnplannedEligibleSnapshotsAndDrains | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestHistoryMetricsOldestIncludesEligibleSourceWithoutPlannedRun | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventoryPollRepositoryClaimFinalizeAndFencing | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventoryPollSchedulerPinsPolicyAndReconcilesWithStoredGrace | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotBindingWaitCompletesWithinLeaseMargin | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotContractFailureFinalizesWithoutPromotion | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotDatabaseRejectsInflatedAggregateCountsAtomically | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotDatabaseRejectsMalformedDuplicateWithoutCanaryReflection | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotDatabaseRejectsNullWrongTypedAndOversizedJSON | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotDatabaseRejectsProviderMissingAboveNodeUnidentified | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotDatabaseRejectsUnclosedIncompleteProviderCounts | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizeAndPolicyActivationHaveOnlyAtomicOutcomes | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizeCapacityKeepsLeaseAndGraceMargin/items_0 | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizeCapacityKeepsLeaseAndGraceMargin/items_1 | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizeCapacityKeepsLeaseAndGraceMargin/items_1000 | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizeLeaseExpiryWhileWaitingForPolicyRollsBack | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizePersistsDuplicateWithoutPromotion | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizePromotesAndReadsCurrentProvider | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFinalizeSkipsChangedPolicyAtomically | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotFuturePolicyBindingKeepsCurrentActivationEligible | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotImmutabilityGuardRejectsDirectAndParentCascadeDelete | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotNodeIdentityIncompleteSkipsPromotion | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotPromotesCompleteProviderBesideIncompleteProvider | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotRejectsContradictoryNodeAndProviderCompleteness | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestInventorySnapshotStalePollCannotMoveProviderPointerBackward | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestLastEnabledAdministratorIsProtected | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestLegacyV1FinalizeClearsAvailabilityMetadataPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestLegacyV1FinalizeDoesNotClearUnpromotedMetadataPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestLockActiveActivationTokenByDigestFiltersStateAndLocks | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestMonitoringIntervalsAndRegistrarLeastPrivilege | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityAcceptancePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityACLAndRollbackPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityAdapterLeavesOtherProviderTokenFieldsNullPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityCompositionPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityLifecycleFilterAndACLPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityLifecycleFilterPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityPerformancePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityUnifiedAuditPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityUnifiedMigrationPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityUnifiedPerformancePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityUnifiedReadModelPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityV4ACLAndNoNewDomainPersistencePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityV4AddsTokenProjectionWithoutChangingV1V2V3Postgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityV4BatchProjectionIsSetBased | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNodeAccountQualityV4ForwardUpgradePreservesV1V2V3AndColumnsPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNotificationAbortedSerializableNoResiduePostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNotificationCommittedReplayPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNotificationConcurrentSameCommittedSnapshotPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNotificationDisplaySnapshotRuntimeACLAndProjection | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestNotificationMigrationPreservesV1Postgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDiagnosticsNeverClearActiveIssues/disabled | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDiagnosticsNeverClearActiveIssues/inventory_absent | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDiagnosticsNeverClearActiveIssues/missing | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDiagnosticsNeverClearActiveIssues/out_of_scope | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDiagnosticsNeverClearActiveIssues/stale | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDiagnosticsNeverClearActiveIssues/unknown | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateConservativePostgres/degraded | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateConservativePostgres/incomplete | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateConservativePostgres/stale | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateConservativePostgres/unavailable | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateConservativePostgres/unverifiable | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateExitKeepsAvailabilityIssue | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsDuplicateMembershipPostgres | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsMigration29To30PreservesExistingSchema | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsMissingAssetDiagnosticsPreserveOccurrence | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsPostgresACL | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsPostgresCoreProjection | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProblemAccountsPostgresExplainPerformance | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestProductRuntimeRoleHasLeastPrivilegeAndCannotMutateAudit | EXPECTED EXTERNAL/DB | set CONTROL_RUNTIME_DATABASE_TEST_URL to run product-role integration tests |
| TestProviderPolicyHistoryIsImmutableCanonicalAndDatabaseTimed | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRecoveryAndActivationTokensAreConsumedOnce | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingAuditShape | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingMigrationRollbackFailClosedWithHistory/fails_when_binding_records_exist | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingMigrationRollbackFailClosedWithHistory/fails_when_only_relay_binding_audit_records_exist | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingRepository_Bind | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingRepository_Concurrency | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingRepository_ReadModel | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingRepository_Rebind | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingRepository_Unbind | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayBindingRuntimeLockPrivilegeMigrationUpDownUp | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRelayNodeGatewayAccountBindingSchema | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRotateAdminSessionCSRFOnlyUpdatesActiveSession | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |
| TestRuntimeRoleCanDisableSecondAdministratorButNotSafetyGuardOrLastAdministrator | EXPECTED EXTERNAL/DB | set CONTROL_DATABASE_TEST_URL to run PostgreSQL schema integration tests |

<!-- skip-inventory-complete: 465 -->

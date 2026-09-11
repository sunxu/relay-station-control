# DingTalk observability / security focused implementation evidence

## Scope and baseline

- Control baseline: `dcca00b6144e873c32adea6ee5f6365e08c5a3bd`.
- Independent branch/worktree: `phase5-dingtalk-observability-security`, `/Volumes/DevRAM/phase5-dingtalk-observability-security`.
- Tasks 4.6 / 4.7 only; `tasks.md` and architecture documents remain unchanged. Architecture Review remains PASS. This is implementation evidence, not formal Implementation Review approval.
- Runtime Acceptance: NOT STARTED. Real DingTalk messages: 0. No task 5.2 work, commit, push, or integration into the main worktree.

## Minimal production correction

The existing `jobSlogLogger` emitted every durable-job lifecycle result at INFO, including final `ResultFailure`. `cmd/control/main.go` now emits `ResultFailure` at ERROR and retains INFO for other results. It preserves the existing component/action/result/job_kind/error_code fields; no notification-specific logging subsystem or job-kind branch was added.

`TestDingTalkFinalFailureUsesExistingErrorLogger` verifies the actual JSON slog adapter's level, fixed code and exact allowed field set. The PostgreSQL integration separately verifies the real Worker's committed failure record; these are compositional tests, not a child-process log capture.

## Focused proofs

`TestDingTalkObservabilitySecurityPostgres` uses isolated PostgreSQL databases, the real Executor, Worker, JobRepository and EnqueueTx, and controlled HTTPS endpoints. Cases are fifth-attempt exhaustion, permanent business rejection with synthetic response material, and oversized response rejection.

- Existing Jobs repository/API expose failed status, kind, attempts/max attempts, fixed error and timestamps; API responses are no-store and exclude payload/secret material.
- The real health handler (`/api/healthz`) returns healthy before and after durable failure. This is handler-level evidence, not deployment-process acceptance.
- A committed ACTIVE Availability occurrence is seeded as an explicit fixture. Its associated real durable notification fails; a full-row snapshot of occurrence storage remains identical. This does not substitute for domain confirmation testing; existing transactional notification tests are rerun separately.
- Audit row count does not change during delivery. No notification audit event was introduced.
- Full-row scans cover async_job_kinds, async_jobs, async_job_events, operation_outbox, audit_logs and account_availability_occurrences. Payload keys exactly match the frozen 14-field snapshot, with complete email preserved. Forbidden additional credential fields are rejected by Registry.ValidateAndHash.
- Existing Prometheus collection retains four metric families and only its existing status label. No email, account key, occurrence identity, node name or secret is emitted.

Runtime canaries are generated in test memory. Values are not recorded in this evidence:

```text
webhook_canary_hits = 0
signing_canary_hits = 0
credential_canary_hits = 0
raw_response_canary_hits = 0
```

`transport_security_test.go` verifies all six uppercase/lowercase proxy variables using a non-loopback hostname, test-only mapped dial and counting dead proxy. NO_PROXY/no_proxy are cleared, not wildcarded. Target hits are positive, proxy hits zero; the production-created transport has Proxy == nil.

302/307/308 use separate controlled TLS source/destination endpoints: source hits are positive and destination hits zero. DNS/connect/TLS/timeout/post-write reset and HTTP/business/invalid/oversized responses retain fixed classifications without runtime canary material in returned codes or captured logs. No production transport behavior was changed.

TRACE SURFACE ABSENT: current code has no trace backend/exporter. Executor's net/http/httptrace GotConn callback only records a local boolean for effect classification; it does not export URL, query or response material. No tracing framework was added.

## Validation ledger

- `go test ./internal/jobs/... ./cmd/control/... -count=1`: PASS.
- Explicit PostgreSQL `go test ./internal/dingtalk/... -count=1`: PASS (46.415s), including new storage/API/health/metrics evidence and existing runtime/replay coverage.
- Explicit PostgreSQL durable-job and notification integration suite: PASS (61.643s).
- Explicit PostgreSQL Token Health / Quality v4 / Problems / duplicate concurrency regression: PASS (40.220s).
- OpenSpec current strict: PASS; all strict: 20/20 PASS.
- Transport security focused race tests: PASS.
- Jobs API/UI and Problems focused regression: PASS, 6 files / 30 tests. Tests exercise the existing generated Jobs transport and rendered Jobs list/detail, including safe diagnostics and dropping uncontracted payload/secret fields. Synthetic frontend fixtures contain no real secrets.
- Frontend full suite: PASS, 28 files / 176 tests.
- `make generate`: PASS after retrying dependency installation (initial partial installation lacked orval).
- `make test build`: NOT PASS. `make test` stops in the existing tools contract test: actual OpenAPI operation count 45, expected 44. `tools/` and `api/openapi.yaml` have no changes against this worktree's committed baseline; no workaround was applied.
- Separate `make build`: frontend generation/typecheck/build PASS, Go link BLOCKED by ENOSPC on the RAM volume. Retrying outside the sandbox removed the cache permission concern but still reproduced ENOSPC. No unrelated caches/worktrees were deleted to force a pass.
- Final diff whitespace check: PASS. Main Control worktree is clean at the stated baseline; Ops/Gateway/CLIProxyAPI are clean and unchanged. No migration, generated client, task accounting or architecture changes.

Full-store: NOT GREEN / PRE-EXISTING baseline remains outside this task; the entire explicit-PostgreSQL store suite was not rerun or repaired. Focused PostgreSQL results above are not skipped runs and are not a claim that full-store is green.

## Handoff

4.6 eligible to close: YES, based on focused implementation evidence. 4.7 eligible to close: YES, based on focused implementation evidence. These are recommendations for formal review/integration, not task checkbox changes or approval on behalf of the reviewer. The full build limitation remains explicit above.

Self-review: no known P0/P1/P2 defect in the scoped change. Implementation Review readiness: READY with the recorded validation limitations. Runtime Acceptance remains NOT STARTED.

## Formal review and main-worktree integration

Original implementation baseline: `dcca00b6144e873c32adea6ee5f6365e08c5a3bd`. Original Implementation Review: PASS for 4.6 and 4.7, P0/P1/P2 = 0/0/0, explicitly approved by the user after the preceding evidence. Architecture Review remains PASS.

Source commit: `f4b290e402719a381d5c41a69feb9a66fa6790a5` (`fix(phase5): expose dingtalk delivery failures safely`). The source worktree is clean. Main integration baseline: `dcca00b6144e873c32adea6ee5f6365e08c5a3bd`. `git apply --3way --check` and `git apply --3way` succeeded without conflicts; only the eight reviewed files were applied. No main-worktree commit or push was performed.

Integrated validation (does not replace historical results):

- Frontend full suite, including Jobs/Problems focused cases: PASS, 28 files / 176 tests.
- Frontend typecheck and build: PASS.
- `make generate`: PASS; no generated tracked changes.
- Explicit PostgreSQL DingTalk/jobs/Control run and transport race run: BLOCKED before execution by RAM-volume ENOSPC during compilation/linking. Test URLs were explicitly supplied; no skipped database test is claimed as PASS. Integrated backend proofs are pending.
- `make test build`: ENVIRONMENT / HOST RESOURCE BLOCKER. The tools package reported cached PASS, then Go test binaries failed to link with ENOSPC. A separate uncached exact OpenAPI test also could not link. This invocation is neither a fresh 44/45 reproduction nor a full PASS.
- The OpenAPI test and YAML blob hashes still match baseline. Earlier independent-worktree uncached 44/45 reproduction remains historical evidence; the inconsistency with earlier integrated PASS remains OPEN. No cache cleanup or baseline fix was attempted.
- OpenSpec current strict PASS; all strict 20/20 PASS.

Tasks remain 38/50: 4.6 and 4.7 remain OPEN until integrated backend validation can execute and pass. 5.2 and all 6.x remain OPEN. This pauses task closure, not the already granted source Implementation Review approval. Runtime Acceptance remains NOT STARTED; real DingTalk messages remain zero.

## Integrated rerun after user-cleared DevRAM space

Baseline and reviewed code remain unchanged. The user cleared DevRAM before this rerun; initial space was 8.0GiB total / 4.6GiB used / 3.3GiB available. The same `/Volumes/DevRAM` TMPDIR/GOCACHE/GOTMPDIR/npm/XDG cache policy was retained; no unrelated cleanup, code/test changes or configuration changes were made. Logs are under `/Volumes/DevRAM/tmp/obs-final-*.log`.

- Explicit isolated PostgreSQL `TestDingTalkObservabilitySecurityPostgres`: PASS (17.429s), all three cases executed, zero skips. Fifth-attempt failure, Jobs failed state, healthy 200/ok before/after, unchanged complete Availability occurrence storage, DB/job/events/outbox/audit/API/log/metrics Secret-negative and bounded raw-response isolation all pass. Email remains intact. Canary counts remain zero; values are not reproduced here.
- DingTalk full focused/runtime/replay suite: PASS (50.842s); jobs suite: PASS (3.750s). Explicit PostgreSQL durable-job/notification/Availability/Duplicate focused store suite: PASS (67.182s), no unexpected skips. This is not full-store or deployment Runtime Acceptance.
- Transport security race suite: PASS (1.836s): six proxy variables ignored, non-loopback mapped target reached, dead proxy hits zero, no wildcard bypass; 302/307/308 destination hits zero; fixed safe error classification preserved.
- `TestDingTalkFinalFailureUsesExistingErrorLogger`: PASS; all Control `TestDingTalk*` focused tests PASS (0.534s). Failure maps to ERROR; other results remain INFO with unchanged fields.
- Frontend full suite including Jobs/Problems: PASS, 28 files / 176 tests; typecheck PASS.
- `make generate`: PASS, no generated tracked diff.
- `make test build`: KNOWN BASELINE 44/45 REPRODUCED at the exact tools test. Both tools test and OpenAPI YAML blob hashes match HEAD. Historical make-test-build evidence inconsistency remains OPEN; no baseline fix.
- Separate `make build`: PASS, frontend build and Go link complete. A sandbox module-cache write warning was handled by retrying the same command with permission; all specified build/temp/cache variables stayed on DevRAM.
- OpenSpec current strict and all strict: PASS, 20/20. Diff/reference checks PASS. TRACE SURFACE ABSENT remains unchanged.

Additional non-gating observation: the unnecessarily broad DB-enabled `cmd/control/...` run failed `TestGatewayDirectoryMainDeploymentHTTPS` and `TestGatewayDirectoryRuntimeProcessRecovery/tls=true` (no fetch/snapshot). These are not classified as 44/45, not claimed PASS, and not repaired here. The required DingTalk-focused Control tests passed separately. Full-store remains NOT GREEN / PRE-EXISTING and was not run wholesale.

All specified 4.6/4.7 closure gates are now satisfied under the user's known-44/45 exception. With prior formal source and integrated code review PASS, only checkboxes 4.6 and 4.7 are closed: actual total 40/50. 5.2 and all 6.x remain OPEN. No new formal review is inferred. Runtime Acceptance NOT STARTED; no real DingTalk; no main commit/push.

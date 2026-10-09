# Account JSON Import Validation

IMPLEMENTATION = COMPLETE / PASS
OWNING_ACCEPTANCE = PASS
OpenSpec archive, push and deployment were not performed.

## Accepted runtime candidate

- Source SHA: `2f3195b256d3d75c0ab5ed2de531d21ac8c4a293`.
- Control local image ID: `sha256:aa9972476111ebf62f3e15abc6fd88823e0431ce5f5e24b8478cc6153a6c70e9`.
- OCI revision matches candidate; running Control image identity verified by
  the repository-owned runtime harness.
- Reused Node source: `0b34a22fcaec392d39f710f3a8418595b491607d`.
- Node local image ID: `sha256:06a2e730ee5420b2f02b77a8e231bb5a384d81d312d6f0d8709b6b5aa94a27a1`.
- Node platform RepoDigest: `sha256:0f968ee25552df8eca55bc3b4bf27ea7d63af1b651513fff44806cb15ad03ce3`.
- Node version/commit observations match the published workflow's full source
  SHA; the Docker image ID and RepoDigest are recorded as distinct identities.
- Browser: Playwright bundled Chromium, approved host execution; no system
  executable override or custom Chromium security flags were introduced.
- Command: `EXPECTED_SHA=<candidate>` with explicit Control image ID and Node
  identities, `deploy/acceptance/runtime/run.sh upload`.

## Proof results

| Gate | Result |
|---|---|
| Full `make test build` in Linux non-root validation container | PASS |
| Frontend unit/component | PASS — 48 files / 367 tests |
| Typecheck / build | PASS |
| RESOURCE_PARITY / REFERENCED_KEY_COMPLETENESS / TRANSLATION_SOURCE_AUDIT | PASS |
| Accounts desktop and import Browser | PASS — 6/6 |
| Real PostgreSQL account operations HTTP integration | PASS |
| Real Control/PostgreSQL/pinned-Node upload Browser | PASS — 1/1 scenario, 3 independent uploads |
| Single JSON identity derived without email input | PASS |
| Sub2API proxy association and Node persisted proxy | PASS |
| Missing batch disabled defaults false; explicit true retained | PASS |
| Independent commands, partial failure and unknown outcome | PASS — component and controlled Browser |
| Node/session/file generation isolation | PASS — focused component proof |
| HMAC byte preservation, exact replay, proxy/disabled conflict | PASS — service and real PostgreSQL HTTP proof |
| Replace Existing disabled rejection | PASS |
| Secret DOM/URL/storage/history/console / runtime artifact scan | PASS |
| Same-origin transport, CSRF, no automatic retry/replace/probe | PASS |
| OpenSpec strict / git diff check | PASS |
| Generated drift | NONE |

Authoritative runtime markers were observed: `SECRET_ARTIFACT_SCAN=PASS`,
`UPLOAD_NEW_BROWSER=PASS`, `UPLOAD_NEW_CONTROL=PASS`,
`UPLOAD_NEW_POSTGRES=PASS`, `UPLOAD_NEW_NODE=PASS`,
`UPLOAD_BATCH_PROXY_DISABLED=PASS`, `UPLOAD_NEW_NATIVE_MUTATIONS=3`.
The batch also checks all registry, operation, receipt and audit records and
the actual Node credential files' proxy URL and disabled booleans. No mock
substitutes for the owning Control/database/Node chain.

## Reconciliation and final review

Historical failures and environmental details are retained in
`validation-progress.md`; that file describes the initial candidate, not the
final disposition. Harness corrections aligned Phase 10 Settings/Dashboard
and Accounts locators and replaced regex extraction with structural JSON
parsing. No product contract was weakened to pass acceptance.

The final five additional unit cases cover exact size/count/identity bounds,
native extension preservation, normalized duplicate identities and invalid
batch disabled values. They and this evidence do not change the accepted
production artifact or runtime harness. The full Make run executed all 367
tests with those cases present.

Review checked JSON identity, whole-file rejection, proxy/disabled mapping,
credential non-reflection, partial outcomes, 401 stopping, stale generations,
replay and Replace compatibility, localization, stable interaction test IDs,
and scope. A separate reviewer subagent was unavailable with the required
`gpt-5.6-luna` model; no other model was substituted. The main agent performed
the final source/spec/test reconciliation.

P0 = 0
P1 = 0
P2 = 0

Backend change = narrow reviewed account-command credential validation only.
OpenAPI / database / migration changes = NO.
Gateway / Node source changes = NO.
Real user credential samples were not added to Git.
Acceptance Compose resources were cleaned up.

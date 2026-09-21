# Phase 10 Test Contract Coverage Review

> 目标：在 implementation 前为 Stage 0 frozen MUST/MUST NOT 指定 owning test layer 和 concrete proof。任何 `TEST_COVERAGE_CONTRACT_GAP` 阻止 Stage 1 实施。

| Frozen contract | Lowest owning layer | Concrete proof | Browser proof | Status |
|---|---|---|---|---|
| PC-only 1280/1440 shell | component/layout + Browser | AppShell layout component tests；CSS/token assertions | 1280×720、1440×900 semantic invariants | OWNED |
| legacy 390×844 superseded | test inventory / Playwright config | 删除或改写 Phase 8 mobile-only expectations，静态扫描无新 mobile contract | 不要求 390×844 PASS | OWNED |
| 七项 Sidebar IA | component | exact nav model keys/routes/testids | zh-CN/en Browser sidebar assertions | OWNED |
| Problems 英文命名 | translation resource | resource key exact `Problems`; source audit rejects `Issues` nav | en Browser no `Issues` nav | OWNED |
| zh-CN 英文泄漏 ZERO | translation audit | source/resource exception allowlist | 1280/1440 DOM text audit | OWNED |
| Asset/Node ownership split | component + transport | `/assets` owns Environment/Gateway/Driver/Policy；`/nodes` is sole executable Node lifecycle owner after Stage 3B；同一 fixture 证明 `/assets` 不再有第二套 Node mutation controls | navigation + direct/reload assertions | OWNED |
| Command Search != global entity search | component/transport/security | search index unit tests只含静态 nav/allowed loaded entities；无 search API call；account_key/Secret 不进入 URL/history/storage/log/metrics | Browser query confirms scope labels + unsafe identity persistence ZERO | OWNED |
| Dashboard truth source | adapter/component | 每个 authoritative card 使用 matrix-approved API；paginated fixture不能生成 totals | Dashboard Browser no fake totals | OWNED |
| Dashboard no auto probe | transport/unit | mount 时 fetch calls不包含 gateway/node health/connection-test | Browser request capture = ZERO probes | OWNED |
| Accounts preserve API semantics | existing API adapters + page tests | existing Orval clients/filters/body preserved | relevant account E2E | OWNED |
| Operations Jobs-first | component | default list uses `/api/jobs`; no account-op global list adapter | Browser Operations shows Durable Jobs | OWNED |
| Monitoring read-only | transport | page transport allowlist仅 GET/read-query；无 admin mutation API | Browser network capture no mutation | OWNED |
| Settings preserves security | existing auth integration + frontend | current auth API tests, reauth/MFA/CSRF regression | high-risk Browser flows | OWNED |
| UI preference browser-local | unit | local state/storage only；network call count zero | reload/session behavior | OWNED |
| no fake Audit Logs/API Keys | nav/page component | no route/nav entries | Sidebar/Search assertions | OWNED |
| no Mock production data | component/transport review | production components require API state; mock only tests | Browser stack uses controlled real/mocked API according to existing E2E policy, not shipped UI | OWNED |
| route/deep-link compatibility | routing unit + embedded-handler | all canonical paths accept optional one trailing slash；`/assets`/`/jobs`/`/topology` legacy compatibility；unknown static/API precedence unchanged | direct navigation + reload + Back/Forward | OWNED |
| canonical spec reconciliation | OpenSpec strict validation | `asset-registry` MODIFIED delta + `node-centric-topology-ui` MODIFIED delta parse successfully；no contradictory 390px required contract remains for Phase 10 | N/A | OWNED |
| i18n parity | translation validation | RESOURCE_PARITY / REFERENCED_KEY_COMPLETENESS / TRANSLATION_SOURCE_AUDIT | live locale switch | OWNED |
| machine values unchanged | formatter/presentation unit | mappings do not rewrite API enum; unknown/outcome_unknown distinct | status rendering assertions | OWNED |
| timestamp/number formatting boundary | foundation unit | all new formatting via `foundation/format.ts`; source scan for stray patterns in new files | locale Browser checks | OWNED |
| Playwright interaction locator policy | E2E static review | interaction locator audit rejects getByRole/getByText/CSS for actions | all new interactions getByTestId | OWNED |
| accessibility | component/a11y semantics | labels, focusable controls, status text | keyboard/focus critical flows | OWNED |
| Browser only -> Control API | transport/security | fetch URL audit stays same-origin `/api/*`; no Gateway/Node/Prometheus | Browser request origin capture | OWNED |
| credentials never exposed | existing secret-negative tests + UI | no credential fields in DTO/presentation/search; source/DOM canary tests | relevant asset E2E negative | OWNED |
| generated client unchanged absent API change | build/generation | `npm run build` + generated drift check | N/A | OWNED |
| Backend/DB/Gateway/Node change = NO | git diff scope gate | final diff path audit | N/A | OWNED |

## Coverage disposition

`TEST_COVERAGE_CONTRACT_GAP = NONE` for the frozen Stage 0 presentation scope.

Architecture Review MUST reopen this review if any backend API, aggregation contract, routing framework, preference persistence, global search or new business capability is added.

# Acceptance & E2E Efficiency Policy

## Status

Mandatory repository-wide policy.

This policy applies to all current and future phases unless explicitly changed through a reviewed testing-policy or architecture decision.

It complements:

- `AGENTS.md`
- `docs/testing/PLAYWRIGHT_E2E_POLICY.md`

The goal is to keep acceptance rigorous while avoiding duplicated proof, opaque long waits, unnecessary corrective rounds, and acceptance infrastructure that becomes more complex than the product being tested.

---

## 1. Core Principle

**Prove each fact once, at the lowest layer that owns it.**

Acceptance quality is measured by confidence in system behavior, not by the number of assertions, repeated runs, corrective rounds, or elapsed test time.

> Be strict on architecture, persistence, security, concurrency, and execution semantics.
> Keep Browser E2E lean.
> Do not repeatedly re-prove lower-layer facts at higher layers.
> Do not turn acceptance infrastructure into a second product.

---

## 2. Responsibility by Test Layer

### Unit / Domain Tests

Use for:

- pure business rules
- validation
- canonicalization
- deterministic mappings
- local state transitions
- error-code derivation
- small concurrency invariants

### PostgreSQL / Persistence Integration

Use for:

- durable operation state
- command reservation
- receipts
- audit
- replay
- serialization
- blockers
- transactional boundaries
- pre- vs post-acceptance persistence semantics

### HTTP / API Integration

Use for:

- authentication
- authorization
- CSRF
- request parsing
- HTTP status mapping
- structured error mapping
- API replay
- zero-persistence security failures
- real HTTP → service → PostgreSQL behavior

### Native / Runtime Integration

Use for:

- external runtime behavior
- native API semantics
- runtime artifact identity
- real mutation count
- provider/runtime integration
- transport-failure classification

### Browser E2E

Use for:

- real user workflows
- frontend → real API wiring
- correct result rendering
- destructive confirmation
- duplicate-submit prevention
- credential/file lifecycle
- structured error presentation
- representative full-chain coverage

Browser E2E SHOULD NOT repeat the full persistence and error-classification matrix already proven by lower layers unless that evidence is specifically necessary to prove that the browser path is real.

---

## 3. Do Not Duplicate Approved Evidence

Once a lower-level behavior has passed review, later phases should reuse that evidence.

Examples:

- If receipt/audit semantics are already proven by real HTTP/PostgreSQL integration, Browser E2E does not need to re-prove every receipt/audit field for every operation.
- If native 5xx classification is already proven in adapter/integration tests, Browser E2E only needs the failure variants required to verify UI behavior.
- If authentication failures are already proven to produce zero persistence at HTTP/PostgreSQL level, Browser E2E should verify the browser-visible security boundary rather than reconstructing the entire persistence matrix repeatedly.

Repeat lower-level evidence only when it is necessary to establish that a new cross-layer path is genuinely using the real stack.

---

## 4. Contract First, Test Expectation Second

Before asserting or waiting for a state, verify that the frozen contract actually requires it.

For every new acceptance assertion, ask:

1. Is this state explicitly required by the frozen contract?
2. Is it required for user-visible behavior?
3. Is it an implementation detail?
4. Is it merely a convenient assumption made by the test?

A test must not create a new product requirement.

Do not block acceptance waiting for a specific eventual state unless the contract requires that exact state.

---

## 5. Execution Truth and Observation Truth Must Stay Separate

Do not turn an asynchronous observation subsystem into operation verification.

General rule:

- operation subsystem = execution truth
- Inventory / monitoring = business observation
- observation must not terminalize execution unless explicitly required
- delayed observation must not invalidate an already-terminal operation
- Browser E2E must not silently promote Inventory into operation truth

If a mutation has the correct HTTP semantics, durable operation result, and native result, then delayed Inventory is an observation problem unless the frozen contract explicitly says otherwise.

---

## 6. Build the Production-Like Acceptance Stack Early

Before expanding Browser E2E coverage, build one production-like stack and prove one real full-chain happy path.

Minimum full chain:

```text
Browser
→ application HTTP server
→ real persistence
→ real external/native dependency
```

Do this before a large browser matrix.

The acceptance stack should expose missing dependencies early, including:

- runtime secrets/configuration
- provider policy
- external runtime composition
- authentication
- background observation components
- runtime artifact identity
- required database bootstrap

## 6.1 Shared Schema and Migration Compatibility

Fresh-install migration success does not prove upgrade compatibility. For a
product with a supported deployed database baseline, any migration that changes
a shared schema contract, including a `CHECK`, enum, `NOT NULL`, `UNIQUE`, foreign
key, bounded taxonomy, lookup domain, role/grant, function, or trigger, must
consider all active producers and consumers and representative data from the
previous schema version.

#### Fresh-Install-Only Exception

A change may use fresh-install-only schema acceptance only when all of the
following are true:

1. The affected product or database has never had a supported deployed version
   requiring an upgrade into the candidate schema.
2. The active design or OpenSpec explicitly declares `EMPTY DB → latest` and
   forward-only support.
3. The exception is reviewed and approved as part of that change.
4. Fresh installation executes the complete retained migration chain from an
   empty database to the latest schema.
5. Historical migration files and provenance identities are retained and are not
   rewritten, squashed, renumbered, or deleted to obtain the exception.
6. Current-schema ACL, security, compatibility-floor, and other current
   invariants remain covered.

Under this exception, historical deployed-version upgrade, `Down`, rollback,
round-trip, and old-binary compatibility coverage are not required unless the
active change separately requires them. Once a schema version becomes a
supported deployed baseline, later changes follow the normal upgrade policy
unless a separately reviewed policy says otherwise.

For the current Phase 8 Stage 0 change, the reviewed support policy is
`EMPTY DB → latest` / forward-only. The exception does not authorize a
historical migration rewrite or removal of current-schema security proofs.

For the normal deployed-schema path, review and validate at minimum:

At minimum, review and validate:

```text
fresh database                  0 → latest
previous-version minimal DB     N-1 → latest
previous-version realistic DB   N-1 + representative valid data → latest
```

For every bounded database vocabulary, verify:

```text
CURRENT_PRODUCED_VALUES ⊆ DB_ALLOWED_VALUES
```

The producer set includes every active writer, including writers belonging to
older features. For a constraint replacement, record the set difference
`OLD_ALLOWED_SET - NEW_ALLOWED_SET`, `CURRENT_PRODUCER_SET - NEW_ALLOWED_SET`,
and `NEW_ALLOWED_SET - OLD_ALLOWED_SET`. A non-empty current-producer difference
blocks the change unless that producer is retired in the same reviewed change.

Historical rows that were legal before the migration must either remain valid or
have an explicitly reviewed, data-preserving transformation. Do not rewrite or
delete immutable audit history to make a constraint pass. A historical migration
remains immutable; a `Down` body is not a supported production rollback mechanism
unless the migration explicitly documents that contract. These rules are
independent: unsupported `Down` behavior does not remove the requirement to test
forward upgrades from the previous version.

Migration 38 is a concrete example: an unrelated feature migration narrowed a
shared audit-category constraint, so fresh-install tests passed while realistic
previous-version data exposed values still emitted by active producers. The rule
also applies to status enums, provider kinds, asset types, lifecycle states, job
types, and other bounded shared vocabularies.

---

## 7. One Full-Chain Proof Before Matrix Expansion

Use this order:

```text
stack composition
→ one representative real happy path
→ prove full chain
→ expand Browser matrix
```

Do not implement many Browser scenarios while the first real full-chain path is still incomplete.

If the first full chain fails, stop expansion and fix the earliest broken boundary.

---

## 8. Diagnose Asynchronous Failures Layer by Layer

Never use one opaque long wait across several subsystems.

Diagnose in order:

```text
source/runtime state
→ application resolver/adapter
→ native snapshot
→ background observer
→ database projection
→ API
→ Browser
```

Stop at the first failed layer.

Bad:

```text
wait 390 seconds for account to appear
```

Good:

```text
Node account present?
NodeResolver correct?
authenticated native GET works?
fresh snapshot contains account?
observer scheduled?
DB projection updated?
API returns account?
Browser renders account?
```

---

## 9. Long Waits Are a Diagnostic Failure Signal

As a default rule:

- waits above approximately 30 seconds require explicit justification
- do not increase a timeout merely because the previous timeout expired
- identify the stalled subsystem before extending a wait
- do not hide missing schedulers, observers, configuration, or runtime composition behind larger timeouts

Before increasing a timeout, answer:

1. What exact component should make progress?
2. How do we know it is running?
3. What event or state proves progress?
4. What is the first observable failure boundary?
5. Why is the proposed timeout justified?

---

## 10. Classify Failures Before Modifying Product Code

A Browser failure is not automatically a product bug.

Classify first:

- `PRODUCT_BUG`
- `E2E_HARNESS_BUG`
- `FIXTURE_BUG`
- `STALE_ARTIFACT`
- `ENVIRONMENT_OR_SANDBOX_FAILURE`
- `TEST_EXPECTATION_BUG`
- `UPSTREAM_BUG`
- `BASELINE_FAILURE`

Before changing production code, verify the relevant layers:

```text
runtime artifact provenance
environment readiness
HTTP result
durable backend result
native/external result
frontend state/rendering
```

Only modify production code when evidence identifies a real production defect.

---

## 11. Verify Runtime Provenance Before Debugging Behavior

When runtime behavior contradicts recently changed source, first prove that the expected artifact is actually running.

Do not trust mutable tags.

Prefer evidence such as:

- source commit
- image ID
- image digest
- running container image ID
- runtime version/commit headers
- runtime DOM/testability hook

A stale image is an acceptance-harness problem, not a product bug.

### Artifact / Identity Handoff Review

For every artifact or identity passed between acceptance layers, separate:

- the source reference, such as a tag, name, path, branch, or alias
- the resolved immutable identity, such as a commit SHA, local image ID, manifest digest, or checksum
- the execution identity actually consumed by the next layer
- the evidence that verifies the execution identity afterward

Reviewers must answer:

1. What is the source of truth?
2. Which inputs are mutable references?
3. Where are those references resolved?
4. What exact immutable identity is produced?
5. What value is passed to the next layer?
6. Does any downstream layer resolve the mutable reference again?
7. What exact identity does execution consume?
8. How is that identity verified afterward?

Resolve a mutable reference exactly once before execution whenever identity affects correctness or reproducibility. Downstream execution must consume the resolved immutable identity. Do not convert it back into a mutable name, tag, or path.

For every check-then-use flow, ask:

```text
What happens if the mutable reference changes after validation but before execution?
```

If that change can alter the object being executed, record a mutable handoff / TOCTOU gap. Check the complete chain:

```text
validation → handoff → execution → verification
```

Matching identity at the validation endpoint and after execution does not by itself prove that the intermediate handoff was immutable.

When identity drift could invalidate correctness, reproducibility, security, or acceptance evidence, consider a negative handoff test:

```text
resolve mutable reference → identity A
change mutable reference → identity B
execute
```

The acceptable outcomes are execution of A or fail-closed behavior. Silent execution of B is not acceptable. This test is required only where identity drift has material impact; ordinary business references do not need a speculative handoff matrix.

Use precise terminology. Distinguish Git commit SHA, OCI revision label, Docker local image ID, RepoDigest, manifest digest, config digest, and artifact checksum. Values that share the textual form `sha256:<value>` do not necessarily have the same semantic identity.

Identity handoff correctness belongs to the layer that passes identity between components. Prove that handoff there; do not add a Browser matrix merely to compensate for missing lower-layer provenance evidence.

---

## 12. Keep Environment Failures Separate From Product Debugging

Failures such as these are environment/harness failures:

- Chromium `EPERM`
- browser startup failure
- missing Playwright browser
- sandbox restrictions
- stale container
- unwritable directories
- missing test secret
- unavailable local runtime dependency

Do not modify product semantics to work around environment failures.

Fix the environment first, then rerun the product diagnostic.

### Browser Execution Provenance

Official Browser E2E and acceptance evidence MUST use the Playwright bundled Chromium unless an explicitly reviewed testing-policy change says otherwise. System-installed Chrome is diagnostic only and cannot replace official acceptance evidence.

A system-Chrome PASS does not close a bundled-Chromium environment failure. If bundled Chromium cannot launch because of `EPERM`, sandbox restrictions, host execution permissions, a missing browser binary, or another launch restriction, classify the result as `ENVIRONMENT_OR_SANDBOX_FAILURE` (or `BROWSER_HOST_EXECUTION_PERMISSION` where applicable). Official Browser acceptance remains blocked until the approved bundled Chromium path executes successfully.

The approved remedy is host execution of the same repository Playwright command while preserving the bundled Chromium, repository configuration, test source, and normal security settings. Do not use a system-Chrome channel or executable path, a temporary external Playwright configuration, modified tests, unsafe Chromium flags, or reduced sandbox/security settings as an acceptance workaround.

---

## 13. Use 3/3 Only at Meaningful Stability Gates

Do not require three complete runs for every intermediate diagnostic.

Default:

- intermediate diagnosis: one deterministic PASS is enough
- race/flakiness corrective: repeat as specifically required
- completed acceptance core suite: run 3/3
- final stability gate: run 3/3

Avoid unnecessary repetition unless repeatability itself is the risk being tested.

---

## 14. Stop at the Earliest Blocker

Acceptance must fail fast.

When a required gate fails:

```text
STOP
```

Do not continue later operations, future matrix cases, unrelated regressions, or final closeout until the earliest blocker is classified and resolved.

---

## 15. Keep Corrective Rounds Narrow

A corrective round should address one proven blocker.

Do not combine product repair, harness repair, new feature work, artifact re-freeze, guard alignment, and unrelated cleanup in one corrective.

Preferred sequence:

```text
finding
→ narrow corrective
→ focused proof
→ independent review when risk warrants it
→ resume acceptance
```

---

## 16. Independent Review Should Be Risk-Based

Independent review is appropriate for meaningful changes to:

- production semantics
- persistence
- security
- concurrency
- runtime artifact identity
- architecture boundaries
- native/runtime assumptions

Do not create a large independent-review round for every trivial test-only adjustment.

Small locator, fixture, and harness corrections may be validated with focused evidence and included in the next meaningful review.

---

## 17. Keep Browser E2E Small

Browser E2E is expensive.

Prefer a representative matrix covering:

- primary happy paths
- destructive confirmation
- structured error rendering
- secret/file handling
- duplicate-submit behavior
- one or a few critical failure states

Do not automatically duplicate every backend error permutation in Browser E2E.

Large error matrices belong primarily in lower-level integration tests.

---

## 18. Browser E2E Is Not a System Certification Framework

Browser tests may inspect lower layers to prove that the path is real.

They should not require every scenario to independently prove all of:

```text
HTTP
registry
operation
receipt
audit
native request count
Inventory
Browser
secret scan
repeatability
```

Use the minimum evidence necessary for the scenario.

---

## 19. Reuse Stable Harness Infrastructure

Once proven, reuse:

- runtime composition
- authentication
- test secrets
- provider-policy bootstrap
- external runtime
- observation service
- secret scanner
- mutation counter
- artifact provenance checks

Do not independently rebuild or re-prove stable foundations for every Browser case.

Revalidate them only when a relevant component changes.

---

## 20. Async Observers Must Be Deterministic in Acceptance

If production uses polling/background observation:

- compose the real production observer
- activate prerequisites before the relevant scheduling/poll slot
- do not race a ticker
- expose which observation stage failed
- use bounded polling of real state only as test synchronization

Do not create E2E-only business-truth writers, fake observers, direct Inventory inserts, or alternate observation APIs.

---

## 21. Test Fixtures Must Be Independent but Simple

Each acceptance scenario should be runnable independently.

It must not depend on:

- previous test execution
- shared dirty DB state
- shared dirty native state
- manually prepared developer state
- accidental test order

However, do not create large fixture frameworks solely for theoretical purity.

Prefer small deterministic setup and cleanup helpers using real production interfaces.

---

## 22. Native Mutation Counts Must Come From Real Evidence

Never hard-code mutation counts.

When exact mutation count matters, derive it from:

- real access logs
- runtime logs
- a transparent counting proxy

A transparent observer must preserve request/response semantics and avoid logging request bodies, Authorization, or secret headers.

---

## 23. Secret Scanning Should Be Implemented Once and Reused

Maintain one reusable secret-audit mechanism.

It should scan relevant:

- application logs
- observer logs
- external runtime logs
- proxy logs
- browser console
- Playwright output
- generated acceptance artifacts

Controlled credential input fixtures may be explicitly excluded.

Broad exclusions such as an entire runtime or logs directory are forbidden.

The scanner must never print raw secret values on failure.

---

## 24. Do Not Casually Modify Pinned Upstream Runtime Code

If broad testing discovers an upstream/runtime defect:

1. determine whether it is pre-existing
2. determine whether the current phase reaches that path
3. determine whether it blocks the current phase
4. repair only when required for phase correctness or runtime stability
5. otherwise document a narrow exception when justified

Do not enter an endless loop fixing unrelated upstream defects found by broad test suites.

---

## 25. Artifact Changes Have Cascading Cost

For exact-pinned runtimes, an artifact change implies:

```text
new source commit
→ rebuild
→ new digest
→ runtime acceptance
→ artifact re-freeze
→ consumer guard alignment
```

Therefore, do not modify a pinned runtime artifact for unrelated or out-of-scope issues.

Before changing it, explicitly confirm that the issue blocks the current phase.

---

## 26. Establish Baselines Before Calling Something a Regression

When a new failure appears after a corrective, compare:

```text
upstream baseline
pre-corrective baseline
post-corrective candidate
```

before classification.

Do not assume every newly observed failure is newly introduced.

Do not waive a failure solely because the changed code appears unrelated.

Separate ordinary test failures, race findings, environment failures, and accepted baseline exceptions.

---

## 27. Keep Acceptance Evidence Proportional

Before adding an assertion, ask:

```text
What fact does this assertion uniquely prove?
```

If the fact is already fully established at another layer, simplify or remove the duplicate assertion.

Acceptance quality comes from responsibility coverage, not assertion count.

---

## 28. Preferred Workflow for Future Phases

Use this sequence by default:

```text
1. Freeze contract and responsibility boundaries.
2. Unit/domain tests prove local rules.
3. Real persistence/API integration proves durable semantics.
4. Build the production-like acceptance stack early.
5. Prove one real full-chain happy path.
6. Implement UI.
7. Add a small Browser E2E matrix focused on user behavior.
8. Diagnose failures layer-by-layer.
9. Run the completed core Browser suite 3/3 once near final acceptance.
10. Run final regression and independent closeout.
```

Do not invert this sequence by completing UI first and discovering the production-like acceptance stack only at the end.

---

## 29. Mandatory Questions Before Adding an Acceptance Assertion

Before adding a new Browser/E2E assertion, answer:

1. Is this required by the frozen contract?
2. Which layer owns this behavior?
3. Has a lower layer already proven it?
4. Does Browser E2E uniquely add confidence?
5. Does this assertion depend on unrelated asynchronous systems?
6. Can a failure identify one clear responsible boundary?
7. Is the runtime cost justified?

If the assertion adds no unique confidence, do not add it.

---

## 30. Mandatory Questions Before Adding or Increasing a Wait

Before adding or extending a wait, answer:

1. What exact component is expected to make progress?
2. How do we know it is running?
3. What event or state proves progress?
4. What is the first observable failure boundary?
5. Why is this timeout duration justified?

Never increase a timeout without answering these questions.

---

## 31. Pre-Implementation Test Contract Coverage Review

Any change that introduces or changes product behavior, an OpenSpec
contract, persistence, concurrency, security, an API contract, a shared
schema, a native/runtime integration, artifact provenance, or an
architecture-level corrective MUST pass a **Test Contract Coverage Review**
before implementation begins.

The required sequence is:

```text
freeze requirements and architecture
→ map each normative requirement to an owning test layer and concrete proof
→ pass Test Contract Coverage Review
→ authorize implementation
→ run focused proof
→ run consolidated regression
→ run representative Browser E2E where required
→ reconcile contract, coverage, tests, and implementation
```

The review prevents implementation from starting with an unowned frozen
requirement. A `MUST`, `MUST NOT`, `SHALL`, exact external result, ordering
requirement, or compatibility invariant without an adequate proof owner is a
`TEST_COVERAGE_CONTRACT_GAP`. It is an implementation-readiness blocker, not
a product failure or a test execution failure:

```text
TEST_COVERAGE_CONTRACT_GAP
→ Test Contract Coverage Review: CHANGES_REQUIRED
→ implementation authorization: NOT AUTHORIZED
```

For each requirement, record enough information for an independent reviewer
to reproduce the proof:

| Contract / Requirement | Risk | Owning Layer | Concrete Proof | Negative / Race Cases | Browser Required |
|---|---|---|---|---|---|
| exact normative behavior | affected failure mode | unit/domain, store, service, adapter, API, artifact, or Browser | existing or planned test, setup, and expected result | applicable boundary or competing order | YES/NO with reason |

Do not describe coverage only as “integration tested” or “covered by E2E”.
The matrix must identify the input or state setup, observable result, and the
layer that owns the fact.

### State-space and concurrency review

When behavior depends on independent classification dimensions, review the
decision matrix before reducing it to a few examples. For example, identity
cardinality and physical mutation eligibility require at least:

```text
0 identities
1 identity + safe physical evidence
1 identity + unsafe physical evidence
>1 identities
```

Apply the same review to identity versus physical eligibility, operation state
versus lifecycle state, authentication versus command identity, execution
state versus replay state, and producer values versus schema-allowed values.

For every contract involving serialization, ordering, races, locks,
at-most-once behavior, concurrent retry, or lifecycle interaction, record the
competing orderings before implementation:

```text
A before B
B before A
concurrent arrival
restart while unresolved
```

Concurrency truth belongs primarily to the transaction or serialization layer
that owns it. PostgreSQL/store integration should prove locking, atomicity,
durable state, and restart behavior. Browser E2E MUST NOT compensate for
missing database or service concurrency proof.

### Durable-boundary review

For flows with `pre-acceptance`, `prepared`, `dispatched`,
`outcome_unknown`, or `terminal` states, the coverage review MUST state:

- which durable records may already exist;
- which registry, operation, receipt, audit, and remote effects must remain zero;
- which remote work may execute;
- which states are replayable and what replay returns.

A deterministic pre-acceptance rejection MUST have explicit zero-side-effect
evidence at the lowest owning layer. Do not duplicate every durable assertion
in Browser tests.

### Exact contracts and parser boundaries

For bounded protocol or error taxonomies, reconcile OpenSpec, OpenAPI,
domain/service mappings, store or database mappings, handlers, and tests.
When the contract specifies one result, tests MUST assert that exact result;
acceptance such as `404 || 409` or `200 || 202` is invalid.

For JSON, multipart, file, size-limit, and optional-field inputs, the review
must identify applicable boundaries among:

```text
absent
present-empty
valid
malformed
oversized
trailing data
extra or duplicate input
boundary-1 / boundary / boundary+1
```

This is an applicability review, not a demand for every mechanical
combination on every endpoint.

### Migration and historical fixtures

Historical migration tests own bounded historical schema intervals. A test for
migration `N` should use `N-1` prerequisites, apply `N`, verify `N`, and run
`Down N` only when that migration explicitly supports Down. It MUST NOT
migrate to latest and walk Down through arbitrary future migrations. A future
forward-only migration must not invalidate an unrelated historical test.

Historical migration verification must use historical-schema-compatible SQL
and fixtures. Current generated queries belong to current-schema integration
tests. If one test mixes historical migration assertions with current query
behavior, split the ownership.

Shared schema changes require realistic previous-version upgrade coverage in
addition to fresh installation when a supported deployed baseline exists. A
reviewed fresh-install-only exception follows the conditions above. For bounded
values, verify
`CURRENT_PRODUCED_VALUES ⊆ DB_ALLOWED_VALUES`, and for constraint replacement
review `OLD_ALLOWED_SET - NEW_ALLOWED_SET`, `CURRENT_PRODUCER_SET -
NEW_ALLOWED_SET`, and `NEW_ALLOWED_SET - OLD_ALLOWED_SET`. Preserve the
existing forward-only and historical-migration immutability policy.

Migration or fixture tests MUST use a disposable database/schema or a
deterministic recreation of their required baseline. A failed test must not
leave shared schema state that changes later test meaning. Cleanup MUST NOT
depend solely on Down from the latest migration.

### Semantic inventories and artifact provenance

When identity matters, prefer semantic set membership or exact set equality to
magic counts. Required API operations should assert exact METHOD, PATH, and
`operationId` membership. An exact public allowlist is a separate contract and
must have an authoritative inventory; a count alone proves neither membership
nor identity.

For exact-pinned runtime or artifact dependencies, the coverage owner must
prove the complete handoff:

```text
source commit/reference
→ build input
→ immutable artifact identity
→ execution identity
→ post-start verification
```

Use the existing Artifact / Identity Handoff Review rules. Do not defer
provenance proof until a final Browser test.

### Browser admission and runtime discipline

Before adding a Browser scenario, answer: **What browser-owned fact does this
prove?** Valid examples include real frontend-to-API wiring, result rendering,
destructive confirmation, credential/file UX, duplicate-submit UI prevention,
and representative full-chain behavior. SQL locking, migration compatibility,
JSON parsing, HTTP status mapping, native classification, and replay matrices
belong to their lower owning layers.

Browser coverage MUST remain representative and minimal. A phase may set a
reviewed scenario budget, but this repository policy does not impose one
global count. Removing a Browser case requires a named lower-layer owner and
concrete replacement evidence.

During diagnosis, use focused, fail-fast, bounded-time runs. Do not wait for a
full expensive suite merely to discover the first blocker, and do not reduce
coverage or add parallelism to hide shared-state races. Reuse fresh PASS
evidence when later changes do not touch its owner or dependencies; rerun a
gate when risk or changed ownership requires it.

### Feedback and final reconciliation

If implementation reveals a new state, error class, race, persistence
boundary, artifact assumption, or schema compatibility requirement, stop and
decide whether the frozen contract changed or the coverage plan was
incomplete. Update the contract or coverage matrix and re-review before
continuing.

Before final implementation review, reconcile:

```text
Frozen Contract
↔ Coverage Matrix
↔ Actual Tests
↔ Implementation
```

Every planned proof must exist at its owning layer, every frozen normative
requirement must have evidence, and no Browser scenario may compensate for a
lower-layer proof gap.

### Readiness checklist

```text
[ ] Every normative requirement has an owning layer.
[ ] Every normative requirement has a concrete proof.
[ ] Concurrency invariants include competing orderings.
[ ] Durable boundaries define allowed and forbidden side effects.
[ ] Independent classification dimensions were reviewed as a matrix.
[ ] Exact protocol results are consistent across authoritative layers.
[ ] Applicable parser boundaries are covered.
[ ] Shared-schema migration compatibility is planned.
[ ] Historical migration tests are version-bounded.
[ ] Artifact provenance has an owning verification step where applicable.
[ ] Browser cases prove browser-owned or cross-layer facts.
[ ] No MUST depends on an unspecified “future test”.
```

Examples:

```text
Node lifecycle vs account dispatch
→ PostgreSQL/store owner
→ A-before-B and B-before-A race tests
→ Browser: NO

identity cardinality × physical eligibility
→ adapter/service owner
→ 0, 1-safe, 1-unsafe, and >1 matrix

Migration N
→ historical test owns N-1 → N
→ future forward-only migration must not break it

required API operations
→ exact METHOD/PATH/operationId membership
→ no magic global operation count unless count itself is frozen
```

## 32. Final Guiding Principle

When acceptance rigor conflicts with acceptance complexity, prefer:

```text
Strict architecture.
Strict persistence.
Strict security.
Strict execution semantics.

Lean Browser E2E.
Deterministic harnesses.
Layered diagnostics.
One proof per fact.

Do not turn acceptance infrastructure into a second product.
```

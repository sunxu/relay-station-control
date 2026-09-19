# Engineering Validation Principles

These principles apply project-wide across implementation, integration, acceptance, artifacts, migrations, and formal validation.

## 1. Validate the real production path

Formal acceptance must exercise the production boundary responsible for the behavior under test.

Do not replace authentication, registration, credential handling, runtime resolution, lifecycle transitions, persistence, fencing, audit, or security enforcement with direct fixtures merely to obtain the expected final state.

Prefer:

```text
real input
→ production runtime
→ production persistence
→ observable result
```

A final database state alone does not prove that the production path works.

## 2. Reuse production capabilities

Acceptance and tooling should compose existing production capabilities rather than reimplementing them.

Reusable runtime behavior should live outside `cmd/*` when multiple legitimate consumers need it.

Avoid copying business logic into:

* shell scripts
* acceptance helpers
* test binaries
* migration utilities

Acceptance may orchestrate production components, but must not become an alternative implementation.

## 3. Keep fixtures aligned with the architecture

Fixtures are consumers of the current architecture and must evolve with:

* schema changes
* credential changes
* lifecycle changes
* runtime contracts
* security boundaries

Legacy fixtures must not remain the execution truth for current acceptance.

Be especially cautious with deprecated columns, direct insertion of derived state, obsolete helper databases, old artifacts, or bypassed registration paths.

## 4. Preserve invariants; avoid timing and test workarounds

Do not fix missing state or synchronization by arbitrarily changing timeouts, grace periods, leases, polling intervals, retries, or Browser waits.

First identify the earliest broken boundary and determine whether deterministic orchestration is missing.

Do not weaken tests, security checks, runtime invariants, or expected behavior merely to obtain PASS.

Structural refactors must preserve existing semantics unless a behavior change is explicitly designed and authorized.

## 5. Validate real runtime capability, not only startup

A healthy process does not prove that all runtime capabilities are usable.

For fail-soft or lazily initialized subsystems, validate the first real operation where appropriate:

```text
startup
→ mutation
→ protected read
→ runtime use
```

Runtime configuration is part of the system being validated.

## 6. Freeze and identify the candidate

Formal acceptance must run against an immutable, clean candidate.

Preferred flow:

```text
implementation
→ focused tests
→ candidate commit
→ clean worktree
→ artifact build
→ runtime acceptance
```

Keep implementation identity explicit and separate from later validation-document commits.

Artifact-based validation should preserve:

```text
source revision
→ artifact identity
→ runtime execution
→ result
```

Changing an artifact invalidates evidence tied to the previous artifact unless equivalence is independently established.

## 7. Require durable, safe evidence

Formal closure requires durable evidence bound to the actual candidate and artifacts tested.

Evidence should record enough provenance to establish:

```text
candidate
→ artifact
→ execution
→ result
```

Use sanitized summaries, hashes, identifiers, and narrowly scoped logs.

Never commit credentials, private keys, tokens, cookies, authenticated storage state, or other secrets.

Ephemeral terminal output, chat history, or memory alone is not sufficient evidence for formal closure.

## 8. Reconcile before closure

Before declaring a Gate, Stage, Phase, or release complete, ensure that:

* implementation matches the approved scope;
* required validation passed;
* production boundaries were not bypassed;
* candidate and artifact provenance are known;
* durable evidence exists;
* task and validation records reflect the current state;
* historical blockers are clearly separated from current authoritative status;
* unresolved work is explicitly deferred to a later scope.

The project-wide completion model is:

```text
Design
→ Implementation
→ Verification
→ Provenance
→ Evidence
→ Reconciliation
→ Closure
```

A runtime PASS without provenance, durable evidence, and record reconciliation is not formal closure.


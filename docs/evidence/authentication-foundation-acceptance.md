# Authentication foundation acceptance evidence

Date: 2026-08-25
Change: `add-control-authentication-foundation`
Scope: synthetic local acceptance environment only

This file records bounded results, not raw logs. It intentionally excludes administrator identifiers, addresses, passwords, TOTP material, recovery codes, cookies, CSRF proofs, activation/bootstrap tokens, keyring values, certificates and database connection strings.

## Verified

- Linux image platform: `linux/arm64`.
- Image user and effective Control process user: `65532:65532`.
- Bootstrap and auth keyring files as observed by the Control process: owner `65532:65532`, mode `0400`.
- Production Control started with Secure cookies and required MFA through a local TLS acceptance proxy.
- Sensitive bootstrap status response and the authentication Web shell returned `Cache-Control: no-store`.
- Fail-closed startup cases returned non-zero without secret material in output:
  - missing auth keyring path;
  - missing bootstrap Secret path;
  - group/other-readable auth keyring;
  - group/other-readable bootstrap Secret;
  - insecure production Cookie configuration.
- Browser/API lifecycle verified:
  - first-administrator bootstrap and ten one-time recovery codes;
  - process restart with bootstrap remaining `completed`;
  - TOTP login on a fresh TOTP step;
  - one-time recovery-code login;
  - second administrator creation and activation;
  - second administrator activation returned ten one-time recovery codes;
  - second administrator disable revoked its active session and subsequent login was rejected with the generic authentication failure.
- Playwright report policy: list reporter only; trace, screenshots and video disabled; transient error context is outside the repository and removed after each run.
- Package installation used `npm ci` with the committed lockfile and the project
  `https://registry.npmmirror.com/` configuration; 286 packages were reproduced.
- Because npmmirror does not implement npm's audit endpoint, OSV Scanner v2.5.1
  scanned all 334 packages in `web/package-lock.json` and 25 packages in `go.mod`;
  it reported zero known vulnerabilities. Direct frontend dependency licenses
  are MIT or Apache-2.0. Direct Go dependency licenses are MIT, BSD-3-Clause or
  Apache-2.0.
- PostgreSQL 18 runtime-role regression verified that the product connection can
  disable a second administrator through the guarded trigger without direct
  `SELECT`/`UPDATE` privilege on `control_admin_safety_guard`; self-disable and
  last-enabled-administrator attempts are rejected, while a legal disable also
  revokes the target session.
- The HTTP runtime-role regression returned the bounded success/error envelopes,
  `Cache-Control: no-store` and a stable request ID; it did not reproduce the
  former trigger-permission HTTP 503.

- Existing phase-0 data-plane isolation:
  - before Control outage: two Nodes, six accounts, zero cross-Node duplicates;
  - while the acceptance Control, PostgreSQL and TLS proxy were stopped: the same two Nodes and six accounts passed the same check with zero cross-Node duplicates;
  - the acceptance PostgreSQL migration and Control/TLS services were restored after the check;
  - no Node account, configuration or traffic fixture was modified.

## Argon2id target-container capacity

The benchmark image used the production non-root Alpine runtime on
`linux/arm64`, limited to 2 CPUs and 768 MiB. The benchmark asserts that the
production floor remains `m=65536 KiB`, `t=3`; no lower-cost parameter set was
used. The source is `internal/auth/password_benchmark_test.go`.

| Operation | Container CPU limit | Result |
|---|---:|---:|
| Hash, serial | 1 CPU | 113.25 ms/op |
| Hash, serial | 2 CPU | 57.48 ms/op |
| Verify, serial | 1 CPU | 110.38 ms/op |
| Verify, serial | 2 CPU | 62.43 ms/op |
| Hash + verify, concurrency 1 | 2 CPU | 216.14 ms/op |
| Hash + verify, concurrency 2 | 2 CPU | 108.37 ms/op |
| Hash + verify, concurrency 4 | 2 CPU | 114.08 ms/op |
| Hash + verify, concurrency 8 | 2 CPU | 269.97 ms/op |

An isolated eight-second concurrency-4 run reached approximately 200.08% CPU
and 538.1 MiB peak RSS (70.06% of 768 MiB), at 115.15 ms/op. Concurrency 8
completed without OOM but reached the full 768 MiB limit and regressed to
269.97 ms/op. The accepted concurrency ceiling for a 768 MiB container is 4;
the preferred throughput point is 2. Do not configure concurrency 8 at this
memory limit. Temporary benchmark containers, images and Dockerfile were
removed after the run.

## Reproduction

Use a new explicit private runtime directory outside the workspace:

```sh
CONTROL_E2E_RUNTIME_DIR=/absolute/private/runtime \
PHASE0_RUNTIME_DIR=/absolute/existing/phase0-runtime \
  deploy/acceptance/control-auth-e2e.sh all
```

If the official Go module proxy is unreachable from the Docker builder, `CONTROL_E2E_GOPROXY` may select an approved mirror for the build only. The default remains `https://proxy.golang.org,direct`.

## 1. Harness

- [x] 1.1 Add isolated Buildx image builder with candidate label/platform verification.
- [x] 1.2 Add repo-external authenticated bootstrap/TOTP and storage-state runner.
- [x] 1.3 Add lifecycle-fixture entrypoint using production Reconcile/Evaluate and no direct enqueue.
- [ ] 1.4 DEFERRED — separate Recovery Harness Hardening follow-up; no recovery PASS claimed here.

## 2. Documentation and verification

- [x] 2.1 Document runtime, Secret, cleanup, auth, lifecycle and Buildx rules; recovery orchestration is explicitly deferred.
- [x] 2.2 Run shell/Node syntax checks, focused harness checks, OpenSpec strict validation and secret/path scans.
- [x] 2.3 Capture clean-worktree and diff evidence without committing or pushing.

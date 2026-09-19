# Tasks

- [x] Confirm Migration 34 dropped `TimeZone=UTC` and freeze the existing compatibility contract.
- [x] Create the independent OpenSpec change and review migration numbering.
- [x] Add the minimal forward-only migration restoring `TimeZone=UTC`.
- [x] Add catalog contract coverage for security definer, owner, search path, and function-local timezone.
- [x] Prove fresh install reaches the latest schema with the catalog contract and History compatibility.
- [x] Prove `51 → 52` upgrade is idempotent and preserves existing current inventory state.
- [x] Run History schema/process focused validation and readonly query regression.
- [x] Run `make generate`, `make test build`, `go vet ./...`, and `git diff --check`.
- [x] Validate and archive this OpenSpec change after all evidence passes.
- [x] Verify clean worktree and record commit provenance.

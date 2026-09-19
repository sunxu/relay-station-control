# Phase 8 Stage 0 Gate 7 Acceptance Evidence

Status: complete. This is a bounded, redacted summary for the final
independent verification of the exact candidate below. Raw runtime logs,
browser state, cookies, credentials, database URLs, account identities and
container paths are not retained in the repository.

## Candidate and artifacts

| Field | Value |
|---|---|
| Candidate SHA | `c51d149daf3765dc981a445a9c84c26305575592` |
| Control image ID | `sha256:67e152929c1589ce43766611549c00c71f32b7c325d9c521f58a6fd703f06ecc` |
| Control OCI revision | `c51d149daf3765dc981a445a9c84c26305575592` |
| Node image | `relay-station-node:phase7-replacement-0b34a22f-20260918` |
| Node image ID | `sha256:7e3428ca0d4bc1640311f540ec1bc33b0d6fcf0cd0d9d700fc19fd98a480ee99` |
| Node source revision | `0b34a22fcaec392d39f710f3a8418595b491607d` |
| Platform | `linux/arm64` |
| Node rebuilt or pushed | `NO / NO` |
| Worktree during acceptance | `CLEAN` |

The Control image was reused by image ID during this evidence run. The Node
replacement artifact was reused unchanged.

## Execution identity and results

The two executions used isolated fresh PostgreSQL 18.6 acceptance databases,
the current replacement Node, the repository acceptance Compose, and
Playwright bundled Chromium through the approved host execution path.

| Evidence | Result |
|---|---|
| Gate-B Node identity and authentication | PASS |
| Control image identity and revision | PASS |
| Fresh migrations 00001 through 00051 | PASS |
| Real Node registration | PASS |
| `management_credential_sealed` presence | PASS |
| Monitoring enable | PASS |
| Stage 0 resolver and K2 open | PASS |
| Real inventory poll run | PASS |
| Poll run finalized | PASS |
| `account_inventory` source linkage to the poll | PASS |
| Target account discovery through current protected lifecycle read | PASS |
| Security Replay | PASS |
| Security Replay duplicate mutation bound | PASS; one HTTP mutation |
| Disable Mode | PASS |
| Disable receipt and audit | PASS |
| Secret scans | PASS |

The inventory bootstrap used the shared Stage 0 runtime, current Node driver,
existing scheduler/claim/worker/finalize path and current-schema protected read.
It did not insert `account_inventory`, snapshot items, credentials or legacy
reader references directly.

## Security and scope assertions

The acceptance output contained no plaintext credential, K2 material, sealed
blob, credential-bearing header, cookie, storage state or raw Node response.
The current path did not use `FileSecretResolver`, a legacy helper database,
the historical Node artifact or direct inventory fixture insertion.

The raw capture files were temporary-only. Their hashes are recorded to bind
the redacted result to the captured executions without retaining the raw logs:

```text
security-replay.log  e1eed1184fba832e5dd37bcecb9d0b07d183bd723f2d4f8233e8062214e6c11c
disable.log          3a9bb0dde00801fa5c57249deb6f7315440bf9f9cba59b455e3fa1741845f2a6
```

Execution windows were 2026-09-19T10:29Z–10:30Z and
2026-09-19T10:31Z–10:35Z. The scheduler's normal five-minute slot boundary
was observed; no scheduler parameter, Browser timeout or production inventory
behavior was changed.

## Final disposition

```text
GATE7_DURABLE_EVIDENCE = PRESENT
GATE7 = PASS
PHASE_8_STAGE_0 = CLOSED
NEXT_STAGE_AUTHORIZATION = NOT_GRANTED
```

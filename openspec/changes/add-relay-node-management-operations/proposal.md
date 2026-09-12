## Why

Phase 6 Node management operations must be planned after Node lifecycle foundation exists. This skeleton reserves the independent operations change without prematurely defining implementation details.

## What Changes

- 规划 Node Health、固定 `/healthz` Connection Test、immediate Monitoring Enable/Disable、admin API/UI、CSRF/audit 和 shared receipt reuse。

## Dependencies

- `add-relay-node-asset-lifecycle-management` MUST be reviewed and approved first.

## Non-Goals

- No scheduled monitoring product API。
- 不做 credential/account mutation、arbitrary HTTP client 或 Node lifecycle schema。

## Planning status

Detailed planning is deferred until the Node lifecycle change is approved. No implementation is authorized.

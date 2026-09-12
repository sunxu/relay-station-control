## Why

Phase 6 Node lifecycle 依赖 Gateway change 建立的 shared command receipt、revision、reason taxonomy 和 compatibility gate。本 change 只建立后续 Node asset lifecycle 的 planning boundary，不重复定义 shared foundation。

## What Changes

- 规划 Node active/retired lifecycle、revision、Retire/Replace、immutable lineage、binding close、monitoring cancellation 和 Inventory fencing。

## Dependencies

- `add-gateway-asset-lifecycle-management` MUST be reviewed and approved first.

## Non-Goals

- 不展开 database/API/tasks implementation。
- 不实现 Health/Connection Test、Monitoring product action、credential/account mutation。
- 不复制 Gateway shared schema、receipt、advisory lock、intent encoding、revision 或 compatibility gate。

## Planning status

Detailed planning is deferred until the shared Gateway foundation review passes. No implementation is authorized.

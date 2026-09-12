## Deferred design boundary

本 skeleton 仅记录 ownership 和 dependency。后续 design MUST reuse `add-gateway-asset-lifecycle-management` 提供的 `asset_admin_command_receipts`、advisory lock、canonical intent encoding、asset revision、`administrator_retire`/`replacement` reason taxonomy 和 compatibility gate。

Node-specific future scope includes Node lifecycle/revision, atomic Retire/Replace, immutable lineage, binding closure, current/future monitoring cancellation persistence, and Inventory lifecycle fencing. Future monitoring cancellation MUST retain history and make cancelled activations permanently ineligible; its representation remains a later implementation-level decision.

Detailed planning is deferred until shared Gateway foundation review PASS. This artifact does not authorize Migration, API, sqlc, UI or runtime implementation.

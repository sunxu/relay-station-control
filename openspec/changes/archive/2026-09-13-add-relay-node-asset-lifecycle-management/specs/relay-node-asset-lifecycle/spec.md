## ADDED Requirements

### Requirement: Relay Node lifecycle SHALL preserve stable identity

`relay_node_assets.instance_id` MUST remain the stable physical identity. A Node MUST transition
only `active -> retired`; `retired` is terminal. PostgreSQL MUST enforce lifecycle status,
revision, retirement shape, actor FK and `retired_at >= created_at`. Physical DELETE, identity
reuse, retired-to-active, identity mutation, history overwrite and capability ownership rewrite
MUST be rejected.

#### Scenario: Existing Node backfill
- **WHEN** additive migration sees an existing Node
- **THEN** it backfills `lifecycle_status=active` and `revision=1` without changing identity,
  metadata, capability rows or monitoring history

#### Scenario: Register and Edit ownership
- **WHEN** Register uses a new identity or Edit changes approved mutable metadata with the
  matching revision
- **THEN** Register creates active revision 1 and Edit advances revision; node type, driver
  contract, stable identity and capability declaration remain immutable

#### Scenario: Retired identity reuse
- **WHEN** a new Register or Replace uses an identity present in current or historical rows
- **THEN** the command returns `duplicate_identity` and no row, capability, audit or receipt
  transition is committed

### Requirement: Node canonical intent SHALL reuse shared v1 encoding exactly

Node action canonical intent arrays MUST reuse the Gateway Stage 1 shared v1 fixed-order
UTF-8 JSON array encoding, SHA-256 `canonical_intent_hash` and K1/version HMAC secret
fingerprinting byte-for-byte; Node MUST NOT define a second encoding version, hash or HMAC
scheme. The frozen per-action arrays are:

```text
Register: [1, "node.register", new_instance_id, display_name,
           normalized_management_endpoint, node_type, driver_contract_version,
           sorted_capabilities, secret_triplet]
Edit:     [1, "node.edit", instance_id, expected_revision,
           display_name_patch, management_endpoint_patch, secret_triplet]
Retire:   [1, "node.retire", instance_id, expected_revision,
           "administrator_retire"]
Replace:  [1, "node.replace", old_instance_id, expected_revision, new_instance_id,
           new_display_name, new_normalized_management_endpoint, new_node_type,
           new_driver_contract_version, sorted_new_capabilities,
           new_secret_triplet, "replacement"]
```

`secret_triplet` is `[operation, secret_fingerprint_key_version, secret_fingerprint]` and MUST
match the Gateway shared tri-state encoding exactly: `absent -> ["absent",null,null]`,
`clear -> ["clear",null,null]`, `set -> ["set",1,"<64 lowercase hex>"]`. Edit's
`display_name_patch`/`management_endpoint_patch` reuse the shared Stage 1 patch-presence
vocabulary `absent/clear/set`, but only support the `absent|set` subset — a Node's
`display_name`/`management_endpoint` MAY be left unchanged (`absent`) or replaced with a new
value (`set`); `clear` is not a valid value for these two fields (there is no empty
display_name/management_endpoint state). Only `reader_secret_ref` (the `secret_triplet`) uses the
full three-state `absent|clear|set` vocabulary. This does not alter the frozen array byte
structure — only the set of valid patch values for each field.

Released v1 arrays for an existing `command_kind` are immutable. A new `command_kind` MAY define
its own shared-v1 array. Changing the field-set or semantics of an existing `command_kind`
requires an explicitly reviewed new encoding version or a proven backward-compatible mechanism,
not an in-place extension of the existing v1 array.

#### Scenario: Encoding fixture parity
- **WHEN** a Node action canonical intent is encoded for hashing
- **THEN** its byte sequence matches the frozen fixture for that action exactly, including array
  order, the shared `secret_triplet` shape and the shared `absent/clear/set` patch-presence
  representation, with `display_name_patch`/`management_endpoint_patch` restricted to
  `absent|set`

#### Scenario: No second encoding scheme
- **WHEN** implementation adds a new Node `command_kind` or needs to change an existing one
- **THEN** a new `command_kind` MAY define its own shared-v1 array under this same encoding
  family, but MUST NOT introduce a second `canonical_intent_hash` algorithm, HMAC key version
  scheme or encoding version field; released v1 arrays for `node.register`, `node.edit`,
  `node.retire` and `node.replace` are immutable, and changing the field-set or semantics of any
  of these existing `command_kind` values requires an explicitly reviewed new encoding version or
  a proven backward-compatible mechanism, not an in-place extension

### Requirement: Node Retire and Replace SHALL be durable atomic commands

Register, Edit, Retire and Replace MUST reuse the shared command receipt, global command ID,
transaction advisory lock, actor-first replay, canonical intent, revision and compatibility
foundation. Retire MUST atomically close current monitoring, cancel future monitoring, close the
current binding, apply the frozen Node-owned durable-work fencing/terminalization rules and
retire the Node. Replace MUST additionally create a
new active identity and immutable replacement lineage. A replacement MUST NOT migrate bindings,
monitoring schedules, Inventory truth, availability/request-quality truth, durable job ownership,
credential/account state or CLIProxyAPI state. Every action's sanitized receipt result and success
response MUST return the full Node read-model projection (`instance_id`, `display_name`,
`node_type`, `driver_contract_version`, `management_endpoint`, `secret_configured`,
`capabilities`, monitoring projection, `lifecycle_status`, `revision`, `retired_at`,
`retired_by`, `retire_reason`) plus the three non-negative counts
(`closed_binding_count`, `closed_monitoring_count`, `cancelled_future_monitoring_count`); for a
retired asset the monitoring projection MUST explicitly report historical/`current: false` rather
than omitting it. Replace additionally returns `old_asset`, `new_asset` (both using this same
full projection) and the immutable lineage
(`old_instance_id,new_instance_id,replaced_at,replaced_by,command_id`).

#### Scenario: Retire replay
- **WHEN** the same actor submits the same command and canonical intent after commit
- **THEN** the persisted status/body is returned, with no second audit, receipt, revision,
  monitoring close or binding close

#### Scenario: Replace crash
- **WHEN** Replace crashes before commit
- **THEN** old Node, monitoring, binding, jobs, new identity, lineage, audit and receipt remain
  at their pre-command durable state

#### Scenario: Full projection on every action
- **WHEN** Register, Edit, Retire or Replace succeeds or is replayed
- **THEN** the response and receipt include the complete Node read-model projection and counts,
  not only the fields that changed, and a retired asset's monitoring projection is explicit
  `current: false`

#### Scenario: Replace success
- **WHEN** a valid Replace commits
- **THEN** old Node is retired with `replacement`, new Node is active revision 1 with zero
  current binding, and one immutable lineage row describes old-to-new identity

### Requirement: Node replacement lineage SHALL be immutable and acyclic

`relay_node_asset_replacements` MUST have unique old and new identities, reject old=new, use
restricted actor and Node FKs, and reject UPDATE, DELETE and TRUNCATE. A→B→C is valid; fork,
merge, cycle and identity reuse are invalid. Lineage and lifecycle changes MUST commit in one
transaction and current/history reads MUST expose predecessor and successor without resurrecting
retired identities.

#### Scenario: Lineage fork or cycle
- **WHEN** a command attempts a second successor, second predecessor or A→B→A
- **THEN** PostgreSQL or the locked replacement transaction rejects it without partial lineage

### Requirement: Monitoring cancellation SHALL be durable and cancellation-aware

Monitoring activation history MUST retain `effective_from/effective_to` and schedule metadata.
Future rows MAY be cancelled exactly once with non-NULL `cancelled_at`, `cancelled_by` and
`cancel_reason`; all three are NULL together or non-NULL together. This change's first version
fixes `cancel_reason IN ('node_retired','node_replaced')` only, with `cancelled_by` a
`control_admin_users.admin_id` FK (`ON UPDATE RESTRICT ON DELETE RESTRICT`); the existing current
close `end_reason` taxonomy (`deployment_disable|scheduled_disable|reconciliation`, its own
text `end_actor`) is a separate column family and MUST NOT be merged into `cancel_reason`,
because those existing reasons use system/deployment actor semantics incompatible with an admin
UUID FK. A future `add-relay-node-management-operations` administrator Disable MAY add its own
`cancel_reason` value as an independent delta, reusing these same cancellation columns rather
than creating a second cancellation persistence. Current intervals MUST close with
`effective_to=boundary` and the existing `end_reason/end_actor/end_recorded_at` metadata (the
existing `end_reason` allowlist additively gains `node_retired|node_replaced`; `end_actor`
remains the baseline text column, and Node lifecycle writes the authenticated canonical
`actor_admin_id` UUID string into it without restructuring the actor schema), not cancellation
metadata. Cancelled future rows MUST never become current, eligible or expected slots.

#### Scenario: Future cancellation
- **WHEN** a future activation is cancelled for `node_retired` or `node_replaced`
- **THEN** the row remains, original schedule fields remain unchanged, cancellation metadata is
  durable and its generated `active_range` is exactly `'empty'::tstzrange`

#### Scenario: Cancellation immutability
- **WHEN** a caller uncancels, changes cancellation metadata, cancels a current/past row, or
  deletes/truncates activation history
- **THEN** the database rejects the operation

### Requirement: Node lifecycle HTTP and reads SHALL be explicit and lifecycle-aware

Control MUST expose fixed lifecycle mutations:
`POST /api/assets/nodes`, `PATCH /api/assets/nodes/{instance_id}`,
`POST /api/assets/nodes/{instance_id}/retire`, and
`POST /api/assets/nodes/{instance_id}/replace`. Mutations require authenticated active
`super_admin`, CSRF, `command_id`, and where applicable decimal-string `expected_revision`.
Existing `GET /api/assets/nodes` and detail route MUST support `lifecycle=active|retired|all`,
stable lifecycle-bound keyset cursor, historical detail and predecessor/successor. Default list
is active; legacy `nodes` count remains total and `node_counts.active` is the operational count.

#### Scenario: Retired default visibility
- **WHEN** an administrator reads the default Node collection
- **THEN** retired Nodes are excluded; `lifecycle=retired` or `all` returns them without
  changing identity or current operational eligibility

#### Scenario: Fixed error mapping
- **WHEN** input, endpoint, target, revision or identity preconditions fail, or the service is
  unavailable
- **THEN** the API returns only the frozen `validation_failed`/`invalid_endpoint`/
  `asset_not_found`/`asset_retired`/`duplicate_identity`/`stale_revision`/`revision_exhausted`/
  `command_conflict`/`service_unavailable` codes and never raw PostgreSQL errors; Node has no
  Gateway-style singleton current-identity invariant, so no `current_identity_conflict` code
  exists and identity conflicts always use `duplicate_identity`

### Requirement: Node mutation audit and metrics SHALL be bounded

Successful transitions MUST write exactly one `asset_node` audit with action
`node.register|node.edit|node.retire|node.replace`; replay MUST write no second transition audit.
Details MUST be limited to command/identity/reason/revision/count fields. Metrics MUST reuse
`control_asset_mutation_total` with `asset_type=node`, fixed action and result enums; Node ID,
command ID, endpoint, actor, Secret and cancellation count MUST NOT be labels.

#### Scenario: Audit or receipt failure
- **WHEN** a successful mutation cannot append its audit or receipt
- **THEN** the full domain transaction rolls back and no success is returned

### Requirement: Node lifecycle SHALL enforce operational eligibility fences

Inventory scheduling, claim/preflight, outbound dispatch and promotion/current truth MUST
re-read Node active lifecycle and required non-cancelled monitoring eligibility at their
respective short transaction fences. Before any network dispatch, Control MUST establish a
durable dispatch authorization boundary using the frozen `account_inventory_poll_runs` columns
`dispatch_authorized_attempt`, `dispatch_authorized_at` and `dispatch_authorized_fencing_token`:
a short transaction that locks the Node, verifies active lifecycle, current monitoring
eligibility and that `lease_fencing_token` matches the run's current fencing token, then persists
these three columns once for the current `attempt_count` (claiming/leasing a run is NOT itself
dispatch authorization; a retry that returns the run to `retry_wait` clears these columns, and
the next attempt obtains a fresh authorization), before releasing the Node lock and performing
transport. The race is resolved by transaction commit order of this authorization write, not by
wall-clock socket-send time: if Node Retire commits before the authorization write, authorization
fails and no HTTP is sent; if the authorization write commits first, the bounded attempt MAY
perform or finish transport even if Retire commits before the socket write completes, but
finalize/promotion MUST still lifecycle-fence and reject current truth. Control MUST NOT hold
the Node DB lock across HTTP.

Node Retire/Replace's terminalization of that Node's poll runs MUST distinguish run state rather
than only checking for transport evidence: `pending`/`retry_wait` runs and `running` runs without
a valid current-attempt authorization (columns NULL or stale relative to the current
`attempt_count`/`lease_fencing_token`) MUST be abandoned immediately by the lifecycle
transaction; a `running` run WITH a valid current-attempt authorization MUST NOT be immediately
abandoned — the lifecycle transaction still commits, but this already-authorized bounded
in-flight attempt MAY finish transport with no new retry/attempt/authorization granted
afterward, reaching `finalized` with promotion skipped by this fence. If that authorized worker
crashes or its lease expires before producing evidence, restart/reconciler MUST NOT grant it (or
any new attempt) a fresh authorization after the Node's retirement/replacement, and MUST
terminalize it as `abandoned` with `node_retired`/`node_replaced` without any outbound
resurrection. Retired/replaced Nodes MUST not create new polls, outbound requests, current
snapshot/account lifecycle changes, availability/request-quality current truth or new binding.
Existing old-Node work MUST become durable abandoned/terminal evidence and MUST not be
retargeted.

#### Scenario: Retire race with dispatch authorization
- **WHEN** a run is valid at claim but Node Retire and the dispatch authorization write race for
  commit order
- **THEN** whichever commits first is authoritative: Retire-first prevents dispatch and durably
  abandons/terminalizes the run; authorization-first allows this bounded attempt to complete
  transport, but finalize/promotion still fences and skips current truth if Retire has since
  committed

#### Scenario: Authorization belongs to a single attempt
- **WHEN** a run is retried (`running` -> `retry_wait` -> `running` with a new `attempt_count`
  and `lease_fencing_token`)
- **THEN** the previous attempt's authorization columns are cleared on the retry transition and
  MUST NOT be treated as authorizing the new attempt; the new attempt requires its own
  authorization write matching its own `attempt_count`/fencing token

#### Scenario: Retire/Replace does not abandon an authorized in-flight attempt
- **WHEN** Node Retire/Replace commits while a `running` poll run holds a valid current-attempt
  authorization
- **THEN** the lifecycle transaction still commits the Node retirement/replacement; the run is not
  immediately abandoned, the authorized attempt MAY finish transport, and no new
  retry/attempt/authorization is granted for it afterward

#### Scenario: authorized worker crash after Node retirement
- **WHEN** an authorized in-flight attempt crashes or its lease expires without producing
  transport evidence, after the Node has already been retired/replaced
- **THEN** restart/reconciler MUST NOT issue a new authorization or attempt for this run; it MUST
  terminalize the run as `abandoned` with `node_retired`/`node_replaced`, without resurrecting
  outbound work

#### Scenario: Old worker after Replace
- **WHEN** an old worker finalizes after replacement or fencing loss
- **THEN** transport evidence may remain, but promotion/current truth is skipped and the stale
  worker cannot update the new identity

### Requirement: Node lifecycle SHALL raise compatibility floor atomically before schema exposure

Because a class 1 (Gateway-aware) runtime cannot safely interpret retired Node history,
monitoring cancellation metadata, cancelled-future empty ranges, replacement lineage or the new
binding end reasons introduced by this change, no class1-incompatible Node schema change MAY be
committed while the compatibility floor remains 1. The following MUST be an atomic, monotonic
production sequence: (1) a signed class 2 artifact/manifest is ready; (2) the external gate/
trust-root wrapper is already deployed; (3) the running class 1 Control process and workers are
stopped; (4) the automatic restart/start path is disabled; (5) one PostgreSQL migration
transaction begins; (6) the first class1-incompatible Node schema changes are applied; (7) the
compatibility floor is raised 1→2 in that same transaction; (8) the transaction commits; (9) the
supported startup path is re-enabled; (10) the wrapper verifies the running artifact's class ≥
floor 2; (11) Control starts; (12) Node lifecycle mutations are exposed only after step 11 passes.
Additive columns that a class 1 runtime can safely ignore MAY land in an earlier compatibility
window migration, but active_range cancellation semantics, retired-Node durable semantics, the
new binding end reasons and any evidence a class 1 runtime could misinterpret MUST be gated by
the same atomic floor-2 barrier. class 1 rollback against a floor 2 database MUST fail closed
through the same mandatory wrapper.

#### Scenario: No floor-1 gap
- **WHEN** any class1-incompatible Node schema change (cancellation metadata, replacement
  lineage, new binding end reasons, retired-Node durable rows) has been committed
- **THEN** the compatibility floor is already 2 in that same migration transaction; there is no
  observable state where the incompatible schema exists and the floor still reads 1

#### Scenario: class 1 rollback against floor 2
- **WHEN** an operator attempts to start a pinned class 1 artifact against a floor 2 database
- **THEN** the mandatory wrapper fails closed before the process starts, and no Node mutation or
  read path becomes reachable

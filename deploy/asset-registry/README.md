# Controlled asset registry SQL

Run all templates with the environment-specific LOGIN that inherits the
`relay_control_asset_registrar` capability role. Each mutation template starts
an explicit `SERIALIZABLE` transaction, fixes the transaction time zone to UTC,
accepts psql variables rather than SQL
fragments, and is safely replayable only when the complete requested content is
identical. Conflicting content fails without partial writes.

- `register-assets.sql` registers one Driver definition, the singleton Gateway,
  and one Relay Node with its declared capabilities.
- `activate-provider-policy.sql` creates or reuses an immutable normalized
  Provider policy version, records its activation boundary, and updates the
  latest-selection binding through the lifecycle-aware transaction. Moving an
  active Provider out of scope updates its current accounts atomically; moving
  it back to active leaves old accounts out of scope until a complete promoted
  runtime observation sees them again. Scope-changing lifecycle activations
  are immediate database-time operations; a future boundary is rejected so
  policy effectiveness and current account state cannot diverge. Every scope
  transition requires a named actor plus a bounded reason and appends immutable
  audit evidence. The template fails closed unless
  `CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=true`; clear or set it to `false`
  before an application rollback.
- `set-node-monitoring.sql` enables, disables, or schedules the Node inventory
  monitoring interval using a Node row lock.
- `reconcile.sql` runs as a read-only repeatable-read transaction through a
  fixed SECURITY DEFINER reconciliation function. The registrar has no direct
  table read permission beyond the environment singleton; reconciliation
  returns only fixed issue codes plus counts and never selects endpoint or
  Secret reference values.

Never pass credentials as `*_secret_ref`; those parameters accept only opaque
secret-manager references. `register-assets.sql` reads the two references from
the fixed `CONTROL_GATEWAY_READER_SECRET_REF` and
`CONTROL_NODE_READER_SECRET_REF` environment variables, then sends them as
extended-query bind parameters. They therefore do not appear in the `psql`
command line, process list, PostgreSQL statement text, or statement logs. Set
either variable to the empty string when that asset has no reference. Use
PostgreSQL 18 `psql -X --set=ON_ERROR_STOP=on
--set=name=value` for the remaining, non-sensitive values documented at the top
of the selected template.

Provide every non-empty `effective_at` as RFC 3339 with an explicit numeric
offset or `Z`; the database stores and compares it as an absolute instant.

# Account Inventory Readonly Query

## ADDED Requirements

### Requirement: secured current inventory query context

The current inventory query function MUST remain a `SECURITY DEFINER` function owned by `relay_control_migrator`, and its function-local configuration MUST contain both `search_path=pg_catalog` and `TimeZone=UTC` after a fresh install or any supported forward migration.

#### Scenario: fresh install preserves the secured query context

- **GIVEN** PostgreSQL applies the complete migration chain to a fresh database
- **WHEN** the catalog contract inspects `control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)`
- **THEN** `prosecdef` is true, the owner is `relay_control_migrator`, and `proconfig` contains both required settings

#### Scenario: existing version 51 schema receives the corrective migration

- **GIVEN** a database has completed migrations through version 51
- **WHEN** the forward migration to version 52 is applied once or repeated
- **THEN** the migration succeeds once, repeats as a no-op, and the catalog contains `TimeZone=UTC` without changing current inventory data or query projection semantics

#### Scenario: history compatibility consumes the repaired function

- **GIVEN** the complete current schema
- **WHEN** `control_history_schema_compatibility_v1()` is called
- **THEN** it returns a compatible result and does not raise the secured-function contract error

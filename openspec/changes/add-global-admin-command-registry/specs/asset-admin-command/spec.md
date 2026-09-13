## ADDED Requirements

### Requirement: Shared asset admin command writers SHALL use the global command registry

All existing Gateway/Relay Node lifecycle and shared asset administrator command writers that accept `command_id` MUST reserve and lookup that ID through `admin_command_registry` under the existing UUID-derived advisory serialization before existing actor/intent/domain processing. Existing immutable `asset_admin_command_receipts` remain the exact completed replay evidence and existing K1/canonical-intent bytes remain unchanged.

#### Scenario: Existing receipt replay after registry migration
- **WHEN** the same actor replays a historical asset command with the same intent after the registry migration
- **THEN** actor/domain/intent resolve through the backfilled registry and Control returns the original persisted receipt body/status without a second mutation, audit or receipt

#### Scenario: Account-domain reservation collides with new asset command
- **WHEN** a Phase 7 account operation has already reserved a command ID and an asset writer receives that same UUID
- **THEN** the asset writer returns `command_conflict` before lifecycle/revision/Secret validation and commits no asset change

## ADDED Requirements

### Requirement: Node Monitoring commands SHALL participate in the global command registry

Monitoring Enable/Disable commands MUST use the same global command registry and actor-first serialization as Gateway/Node lifecycle commands. Existing per-Node Disable fence semantics, receipt `committed_at` ordering and monitoring transaction behavior MUST remain unchanged after registration. Health and Connection Test remain command-ID-free observations and MUST NOT reserve global command IDs.

#### Scenario: Monitoring command collides with another domain
- **WHEN** a Monitoring Enable/Disable request uses a command ID already reserved by a Gateway/Node/account command
- **THEN** Control returns `command_conflict` before monitoring-state validation and makes no monitoring change

#### Scenario: Health uses no command identity
- **WHEN** an administrator runs Node Health or Connection Test
- **THEN** no global command reservation is created and existing observation semantics remain unchanged

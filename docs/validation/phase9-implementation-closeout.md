# Phase 9 implementation closeout

日期：2026-09-20  
规划冻结：`e659cc26bb77ab61dd3d8d8c16343530effad9d6`

## Final status

```text
PHASE9_IMPLEMENTATION_RUN = PASS
PHASE9_PRODUCTION_ACCEPTANCE = PASS
PHASE9_STATUS = CLOSED
P0 = 0
P1 = 0
ARCHITECTURE_DELTA_DETECTED = NO
```

Phase 9 production acceptance completed successfully. The final deployment
bundle is `deploy-v0.9.1`.

## Control corrective evidence

```text
ROOT_CAUSE_CONTROL_INIT = CONTROL_DEPLOYMENT_INIT_CAPABILITY_ORDER_BUG
CONTROL_CORRECTIVE_COMMIT = 680ff6e
CONTROL_FINAL_RELEASE_COMMIT = f90ebde7b926aa4ae8b51acdf389e97b7a415493
CONTROL_RELEASE_TAG = deploy-v0.9.1
CONTROL_DIGEST = sha256:9006e30637290695fbc1620205d9a07b4799d622c35e14a1f54218caac885717
```

`relay-control-init` initially passed the driver capabilities to
`public.control_register_node_driver(...)` in the order
`management_health_read`, `management_account_inventory_read`. Migration
00003 defines the database canonical order as
`management_account_inventory_read`, `management_health_read`. The corrective
changed only the deployment-init capability order to satisfy that existing
database contract; it did not change the migration or relax canonical
ordering. The final release commit also contains the ordinary empty provider
list bootstrap correction found during fresh acceptance.

Gateway and Node source were unchanged and their validated immutable digests
were reused:

```text
GATEWAY_DIGEST = sha256:b066d2d3ea1e6e14956f37e7603a845d97df6dace07bba2cbccd1475add3617b
NODE_DIGEST = sha256:22b9853f6f45c87ad87234487155f5d9ca5bc3dddfb637ad236cc7435a5aa159
```

## Ops corrective evidence

The production acceptance corrective set covered:

- PostgreSQL 18 persistent volume layout;
- removal of the runtime `apk add` dependency from `control-secret-init`;
- `control-init` runtime role/password wiring;
- the native production Node configuration;
- direct `control-init` binary invocation; and
- Control runtime login-role wiring.

```text
OPS_ARTIFACT_PIN_COMMIT = c216414f0eee4f8aba501f1ebec4c3f4ada93779
```

No architecture change was introduced.

## Production Acceptance

```text
FRESH_DEPLOYMENT = PASS
control-postgres = healthy
gateway-postgres = healthy
redis = healthy
control-secret-init = exited 0
control-init = exited 0
control = healthy
gateway = healthy
node = healthy
```

Repeat and lifecycle checks passed:

```text
REPEAT_UP = PASS
control init remained successful
persistent state preserved
secret bytes preserved

RESTART_RECREATE = PASS
services returned healthy
secret bytes preserved
persistent data preserved
```

Gateway authentication and runtime checks passed:

```text
GATEWAY_BEARER = PASS
valid bearer = HTTP 200
invalid bearer = HTTP 401

ARTIFACT_EQUALITY = PASS
Control = sha256:9006e30637290695fbc1620205d9a07b4799d622c35e14a1f54218caac885717
Gateway = sha256:b066d2d3ea1e6e14956f37e7603a845d97df6dace07bba2cbccd1475add3617b
Node = sha256:22b9853f6f45c87ad87234487155f5d9ca5bc3dddfb637ad236cc7435a5aa159

SECRET_LEAK_SCAN = PASS
BUSINESS_SMOKE = PASS
Control health = HTTP 200
Gateway health = HTTP 200
Node health = HTTP 200
```

## Validation

The final validation set passed:

```text
production contract tests = PASS
docker compose config --quiet = PASS
git diff --check = PASS

Control make test = PASS
Control make build = PASS
```

Control `deploy-v0.9.1` remains immutable and points to
`f90ebde7b926aa4ae8b51acdf389e97b7a415493`. Ops owns the complete deployment
bundle tag; component artifacts may be reused by immutable digest when their
source and artifact are unchanged.

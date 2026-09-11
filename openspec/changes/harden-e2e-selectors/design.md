## Selector policy

- `data-testid`: stable UI element identity.
- Stable business `data-*`: account, instance, occurrence and job identity.
- `role`/accessible name: only where the accessible contract itself is stable.
- Do not use `.ant-*`, CSS hierarchy, `nth-child`, XPath hierarchy, or DOM implementation details as durable selectors.

## Inventory-first plan

Current inventory covers `web/e2e/authentication.spec.ts`, `problems.spec.ts`, and `topology.spec.ts`.

Fragile or coupled findings include `.ant-card`, `form button[type=submit]`, `.secret-list code`, `.first()`, broad `getByText`, and copy-coupled Chinese labels. Existing role/label selectors that express stable accessibility contracts are classified STABLE and are not changed in this phase.

Priority areas are Problems page, Management navigation, bootstrap/login flow, and Jobs observability. The minimum expected additions are scoped selectors for page roots, navigation targets, entity rows, occurrence/job rows, detail containers, and business identity cells. Exact names require UI-owner review before implementation.

## Guard

The follow-up implementation may add a lightweight selector guard or thin page objects, but must keep selectors local and explicit. It must not infer identity from position or presentation classes.

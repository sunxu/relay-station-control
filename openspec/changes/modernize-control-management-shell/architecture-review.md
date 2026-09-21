# Phase 10 Architecture Review

## Review Baseline

```text
Local planning commit:
a6ebc98

Remote availability at review time:
NOT AVAILABLE

Reviewed artifacts:
docs/phase10/CONTROL_WEB_UI_REQUIREMENTS_CN.md
openspec/changes/modernize-control-management-shell/**
```

Repository source/API cross-check 使用当前可读取的 `relay-station-control` main baseline；`a6ebc98` 未 push，因此 review 无法从 remote 按 SHA 重取，但 Stage 0 文件与本会话生成并由用户提交的内容一致。

## Round 1 Disposition

```text
P0 = 0
P1 = 2
P2 = 2

ROUND_1 = CHANGES_REQUIRED
```

### P1-1 — Asset Registry canonical ownership conflicts with `/nodes`

Current canonical `asset-registry` requirement “只读资产页面处理空状态与故障” requires one Asset Registry page to display Environment / Gateway / Node / Driver / Provider Policy, explicitly says “不新增第二套页面”, and owns Node lifecycle + Stage 3 operations.

Phase 10 introduced `/nodes` as Relay Node primary management surface while `/assets` was described only as Gateway auxiliary. Without a `MODIFIED` delta, archive would leave two contradictory canonical truths and Environment / Provider Policy / Driver ownership under-specified.

Corrective：

- `/assets` auxiliary owns Environment identity、Gateway lifecycle、Driver catalog、Current Provider Policy；
- `/nodes` is the sole executable Node lifecycle / monitoring presentation owner after Stage 3B；
- `/assets` then removes executable Node controls or retains only navigation；
- API / CSRF / revision / credential / transaction / replay / audit semantics unchanged。

### P1-2 — Current canonical Topology still requires 390px

Current canonical `node-centric-topology-ui` includes:

```text
WHEN ... 在390px/桌面/键盘模式查看
THEN ... 各区 ... 可辨识
```

Phase 10 states PC-only and explicitly supersedes Mobile/Tablet acceptance. An ADDED requirement in a new capability does not reconcile this older canonical MUST.

Corrective：

- add `MODIFIED Requirements` for exact existing requirement `Topology SHALL 保护身份并安全恢复读取`；
- replace 390px release requirement with 1280×720 / 1440×900 Desktop acceptance；
- keep keyboard/accessibility and all read-only identity/truth semantics；
- `/topology` remains compatibility alias to `/monitoring` during migration。

### P2-1 — New route trailing-slash behavior was unspecified

Existing `/assets` canonical contract explicitly handles `/assets` and `/assets/`; new Phase 10 routes initially did not. With current lightweight pathname matching, an unhandled trailing slash can fall back to default route.

Corrective freezes optional single trailing slash normalization for every canonical Phase 10 route.

### P2-2 — Optional entity search lacked identity persistence constraints

Requirements allow optional current-page entity search. Existing account security contracts forbid canonical `account_key` from browser navigation URL/storage/log/metrics outside narrow approved paths.

Corrective freezes Search as memory-only for entity selection when no safe deep-link identity exists and forbids Secret / credential / token / account_key persistence.

## Verified Architecture Strengths

- Embedded web handler uses generic SPA fallback for non-`/static/*` unknown routes; new `/accounts` / `/nodes` / `/operations` / `/monitoring` / `/settings` do not require Go/backend route changes.
- `/api/healthz` returns current status + Control version, so Dashboard Control status/version is a valid authoritative point-in-time summary.
- Gateway `gateway_counts` and Relay Node `node_counts` are server-computed full registry lifecycle counts, not current-page `items.length`.
- Jobs, Problems and Account Inventory APIs remain pagination/query surfaces without a global total contract; keeping them Navigation-only on Dashboard is correct.
- Current frontend routing is lightweight pathname + pushState + popstate; Phase 10 can extend it without React Router.
- `foundationTheme` is already the existing Ant Design theme entry and can be expanded without a second design system.
- Browser direct Gateway/Node access remains prohibited; explicit health/connection probes are Control-owned API actions and are not automatically triggered by Dashboard.

## Corrected Candidate Status

After applying this review corrective:

```text
P0 = 0
Known P1 = 0
Known P2 = 0

TEST_COVERAGE_CONTRACT_GAP = NONE
BACKEND/API = NO CHANGE
DATABASE/MIGRATION = NO CHANGE
GATEWAY SOURCE = NO CHANGE
RELAY NODE SOURCE = NO CHANGE

RE-REVIEW READINESS = READY
```

Final Architecture Review PASS is contingent on the real worktree proving:

```bash
openspec validate modernize-control-management-shell --strict
openspec show modernize-control-management-shell --json --deltas-only
git diff --check
```

and showing the three delta capabilities:

```text
control-management-shell
asset-registry
node-centric-topology-ui
```

## Re-review — Residual Contract Scan

Re-review scanned current canonical `openspec/specs/**` for:

```text
Asset Registry + Node operation ownership
/assets + Node UI ownership
390px / narrow-screen mandatory acceptance
/topology UI route coupling
automatic health probe semantics
```

One residual requirement was found:

```text
relay-node-management-operations
Requirement: Node operations SHALL remain explicit and isolated
```

It explicitly bound Node Health / Connection Test / Monitoring UI to the historical Asset Registry. A fourth `MODIFIED` delta is therefore required and has been added.

No separate residual Mobile product gate was found outside `node-centric-topology-ui`.

Monitoring is additionally frozen to zero automatic remote probes: existing Node/Gateway Health remains explicit bounded observation; mount/refresh cannot manufacture a health dashboard by probing every asset.

### Final corrected candidate

```text
Delta capabilities:
1. control-management-shell
2. asset-registry
3. node-centric-topology-ui
4. relay-node-management-operations

Known P0 = 0
Known P1 = 0
Known P2 = 0

Backend/API change = NO
Database/Migration change = NO
Gateway source change = NO
Relay Node source change = NO
```

### Final Architecture Review disposition

Document/source architecture re-review:

```text
PASS CANDIDATE
```

Repository-level final PASS requires the actual local commit/worktree to prove:

```bash
openspec validate modernize-control-management-shell --strict
openspec show modernize-control-management-shell --json --deltas-only
git diff --check
```

The `--deltas-only` output MUST contain all four capabilities above. If strict validation passes and no unreviewed files are present, Architecture Review MAY be recorded as `PASS` and Stage 1 implementation authorized.

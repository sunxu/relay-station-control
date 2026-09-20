# Phase 8 Stage 0 Planning Validation

## Status

```text
Planning baseline:
7e73fb87ea46e8930afd94a3bd25f009ece5bedb

Validated runtime provenance:
fab6aadc36a9f8ebe1309e5db457dcbac0136880

Ops planning truth baseline:
5367d4a9546610221cb21addde64e8550e016aed

Requirements reopened:
NO

Architecture reopened:
NO

Base TCCR reopened:
NO

Compatibility Addendum:
PASS

Product implementation started:
NO

Migration 00051 created:
NO

Runtime/API/frontend implementation touched:
NO

Implementation authorization:
NOT AUTHORIZED
```

## Authoritative Inputs

- Ops Phase 8 Program Plan。
- Stage 0 frozen Requirements R1–R21。
- Approved Architecture Decisions A–L。
- Base TCCR proof matrix A01–N 与 Browser A–C。
- Compatibility Addendum O01–O05（migration 00051 / class 4 / floor 4）。
- Implementation Readiness Review；本 change 仅关闭 Control-owned planning corrective，不自行把 readiness 改为 PASS。
- Ops System Design 与 ADR-0001 Stage 0 窄范围 amendment。

## Traceability Method

链路固定为：

```text
Requirements R1–R21
→ Architecture A–L
→ Base TCCR proof IDs + Compatibility Addendum O01–O05
→ OpenSpec delta requirements
→ tasks.md
```

Proof ID 可以由多个测试共同完成；本 mapping 不要求一个 proof 对应一个 test function，也不新增冻结契约之外的产品要求。

## Requirements Traceability

| Requirement | Architecture | Proof owner | OpenSpec delta | Tasks |
|---|---|---|---|---|
| R1 scope限两类credential | A/B/H, YAGNI | A01/A06 | `asset-management-credential-protection` scope | 0.1, 7.6 |
| R2 protected-at-rest only | A/B/D | E/M/L01 | protection, asset lifecycle | 1.4–1.5, 2.1–2.2, 3.4 |
| R3 external asset-count-independent key | C | B/C/L02/L07 incl. B07 | K2/commitment | 1.1–1.3, 6.1–6.2 |
| R4 rotation out of scope | C/L/YAGNI | B05/A06 | K2 process lifecycle | 1.1–1.3, 6.3 |
| R5 tri-state write semantics | E/F/G | F | registry/admin/lifecycle specs | 3.1–3.3, 5.1–5.4 |
| R6 mutation atomicity | F/K | H | admin/lifecycle specs | 3.4–3.5 |
| R7 safe configured read | A/B/E | E | protection/registry specs | 2.2, 5.1–5.4 |
| R8 Node zero legacy runtime | H/J | I01/I05 | readonly-driver/account-operation | 4.1–4.2, 4.4 |
| R9 Gateway zero legacy runtime | H/I | I02/I04 | Directory ingestion | 4.3–4.4 |
| R10 fail closed | C/D/H/L | I03/J | protection/runtime specs | 1.3–1.5, 3.6, 4.1–4.4, 6.3 |
| R11 non-observability | B/D/G | G11/M | all secret-bearing deltas | 1.5, 5.3–5.5, 7.5 |
| R12 Node+Gateway recovery | C/L | L03–L05 | recovery harness | 6.2–6.3 |
| R13 host migration | C/L | L06 | recovery harness | 6.4 |
| R14 fresh install target | A/K migration | K | protection/recovery | 2.1–2.3, 6.4 |
| R15 Node semantics unchanged | F/G/J/K | A02 | Node lifecycle/readonly | 3.5, 4.1–4.2 |
| R16 Gateway semantics unchanged | F/H/I/K | A02/I04 | Gateway lifecycle/Directory | 3.5, 4.3 |
| R17 exact replay/conflict | F/G | G | asset-admin-command | 3.1–3.3 |
| R18 Phase7 semantics unchanged | H/J | A03/I05 | account-admin-operation | 4.2 |
| R19 credential-free paths independent | H/L | A04/J | runtime specs | 4.4, 6.3 |
| R20 minimal frontend | E | A05/Browser A–C | asset-registry | 5.4–5.5 |
| R21 no premature infrastructure | all/YAGNI | A06 | protection scope/non-goals | 0.1, 7.6 |

## Architecture Traceability

| Decision | Frozen meaning | Delta / proof / task |
|---|---|---|
| A | one opaque sealed value on owning asset row | protection + lifecycle; E/H/K; 2.1, 3.4–3.5 |
| B | protected columns ACL-isolated | protection + runtime; E; 2.2 |
| C | K1/K2 separate responsibilities | protection/admin; B/C/G; 1.1–1.3, 3.2–3.3 |
| D | AES-256-GCM + asset/domain AAD | protection; D/M; 1.4–1.5 |
| E | credential write fields, configured-only reads | registry/lifecycle; F/E/Browser; 3.1, 5.1–5.5 |
| F | credential mutation in existing command transaction | admin/lifecycle; G/H; 3.2–3.5 |
| G | K1 commitment + intent v2 | admin; G; 3.2–3.3 |
| H | narrow cipher/Node/Gateway resolvers | protection/runtime; I/J; 4.1–4.4 |
| I | Gateway Directory contract otherwise unchanged | Directory; A02/I04; 4.3 |
| J | Phase6/7 Node behavior otherwise unchanged | readonly/account operations; A03/I05; 4.1–4.2 |
| K | Retire/Replace predecessor erase | lifecycle; H; 3.5 |
| L | feature-level K2 fail closed | protection/recovery; J/L; 1.3, 3.6, 4.4, 6.2–6.3 |

## Base TCCR Corrective Crosswalk

| Proof ID | Frozen fact carried into this change | Delta / Tasks |
|---|---|---|
| B01 | K2是32-byte regular、non-symlink、safe-owner/mode file | protection K2 structural contract; 1.1 |
| B02 | Control runtime never silently generates K2 | protection runtime scenario; 1.1–1.2 |
| B03 | bootstrap以OS CSPRNG create once；repeat不replace；failure无usable partial key | protection bootstrap scenarios; 1.2, 6.1 |
| B04 | raw K2仅path配置且不可观察 | protection non-observability; 1.1, 1.5 |
| B05 | process load once、no hot reload | protection K2 lifecycle; 1.1, 6.3 |
| B06 | one sanitized warning且unrelated features可用 | protection startup warning; 1.5 |
| B07 | supported provisioning provenance；runtime不推断entropy | protection provisioning; 1.1–1.2, 6.1 |
| C01 | exact commitment derivation | protection commitment; 1.3 |
| C02 | matching K2 available；wrong-valid K2在Seal/Open前拒绝 | protection existing match/mismatch; 1.3 |
| C03 | wrong K2时zero protected mutation、zero authenticated outbound | protection/runtime fail-closed; 1.3, 4.1–4.3 |
| C04 | fresh-init atomicity，包括K2-A/K2-B race与exactly one winner | protection fresh-init; 1.3 |
| C05 | commitment absent + sealed state fail closed | protection invalid DB scenario; 1.3 |
| C06 | commitment write-once且value不进入normal/test/acceptance surfaces；内部observer仅输出redacted boolean/PASS-FAIL evidence | protection non-observability + shared scanner + acceptance observer with value-redacted evidence; 1.3, 1.5, 2.6, 7.5 |
| D01 | opaque `12-byte nonce || ciphertext+tag` layout；malformed/tampered/truncated fail | protection sealed layout; 1.4, 2.1 |
| D02 | nonce来自crypto RNG；RNG failure无partial mutation | protection cipher; 1.4, 3.4 |
| D03 | exact Node/Gateway AAD domain + 16-byte binary asset UUID binding | protection AAD; 1.4 |
| D04 | credential 0/1/4096/4097、multibyte、NUL/CR/LF boundaries | admin/lifecycle validation; 3.1 |
| D05 | exact bytes preserved；no trim/no normalization | admin/lifecycle validation; 3.1, 3.3 |
| F01 | Register missing/string/null tri-state | admin/lifecycle; 3.1, 5.1–5.3 |
| F02 | Edit missing/string/null tri-state | admin/lifecycle; 3.1, 5.1–5.3 |
| F03 | Replace missing/string/null及no inheritance | admin/lifecycle; 3.1, 3.5, 5.1–5.3 |
| F04 | legacy `reader_secret_ref` write input rejected | API/static cutover; 2.3, 5.1–5.3 |
| F05 | no crypto-specific public error taxonomy | asset-admin API delta; 5.3a |
| G01–G04 | intent v2、K1 commitment equivalence与domain behavior | admin/lifecycle; 3.3 |
| G05 | different actor precedence | admin actor-first; 3.2 |
| G06 | same actor exact replay without K2 | admin replay; 3.2–3.3 |
| G07 | same actor different intent conflict without K2 | admin conflict; 3.2–3.3 |
| G08 | same actor existing command + semantic-invalid credential先做existing-command classification | admin Node/Gateway scenarios; 3.2 |
| G09 | genuinely-new invalid credential | validation matrix; 3.1–3.2 |
| G10 | genuinely-new valid Set + K2 unavailable | command fail-closed matrix; 3.6, 5.3 |
| G11 | raw credential/recoverable ciphertext不进入canonical/registry/receipt/audit | admin/protection non-observability; 1.5, 3.3, 7.5 |

## Compatibility Addendum Traceability

| ID | OpenSpec requirement | Future owning proof | Task |
|---|---|---|---|
| O01 | protection: class-4 signed manifest v1 accepted | `internal/compatgate` + gate CLI integration | 2.4 |
| O02 | floor 4 requires Migration 00051 | PostgreSQL + compatgate | 2.4 |
| O03 | Migration 00051 requires floor >=4 | PostgreSQL + compatgate | 2.4 |
| O04 | signed class-3 rejected before Control starts | compatgate/CLI/wrapper acceptance | 2.5 |
| O05 | reject/restore leaves sealed state+commitment unchanged | real compatibility acceptance + PostgreSQL observer | 2.6, 7.4 |

## Canonical Spec Impact Review

| Canonical capability | Impact | Delta |
|---|---|---|
| `asset-registry` | write fields、configured-only read与最小UI改变 | MODIFIED |
| `asset-admin-command` | actor-first与canonical intent既有contract被MODIFIED；credential validation/atomicity/K2矩阵为ADDED | MODIFIED + ADDED |
| `gateway-asset-lifecycle` | Gateway credential tri-state/erase改变 | MODIFIED |
| `relay-node-asset-lifecycle` | Node credential tri-state/erase改变 | MODIFIED |
| `gateway-account-directory-ingestion` | credential source与protected fencing read改变 | MODIFIED |
| `cliproxyapi-readonly-driver` | authenticated Node credential source改变 | MODIFIED |
| `account-admin-operation` | Phase7 authenticated consumer source改变；state machine不变 | CREATED |
| `runtime-acceptance-harness` | class4/O01–O05/provenance/secret acceptance改变 | CREATED |
| `runtime-recovery-harness` | DB+K2 recovery set与wrong/missing K2改变 | CREATED |
| `asset-management-credential-protection` | 新增K2/crypto/DB/compatibility capability | CREATED (NEW) |

未创建其他delta；Gateway/Node product repositories、data-plane、Provider credential semantics、generic Secret infrastructure均不受影响。

## Canonical Scenario Parity Review

```text
Canonical scenario parity:
PASS
```

已人工逐项核对canonical baseline scenario set与MODIFIED scenario set：asset-registry endpoint/no-network、asset-admin replay/K1、Gateway HTTP mapping、Node v1 historical intent、Directory fetch/fencing/secret与readonly-driver scenarios均保留其identity并只应用批准的Stage 0 delta。Directory“只存在reader_secret_ref”不再作为合法Stage 0 runtime scenario；为满足MODIFIED whole-block parity，其scenario identity被改写为Migration 00051 pre-runtime hard guard，并由F04 old write-field rejection、static zero-`FileSecretResolver` proof及fresh DB/re-register transition共同覆盖。Directory requirement标题为保持OpenSpec MODIFIED identity而保留canonical文字，正文明确supersede旧Secret-reference存储语义。

## Planning Completeness Review

- Proposal 明确Phase、outcome、repo ownership、truth sources、Migration 00051、class/floor 4、security/rollback/data-plane及non-goals。
- Design覆盖K1/K2、AES-GCM/AAD、tri-state、actor-first、atomicity、Migration/ACL、compatgate、runtime cutover、API/UI、recovery与proof ownership。
- Tasks按0–7依赖顺序拆分，每项包含验证证据；全部保持未勾选。
- 未创建`implementation-validation.md`，未创建Migration 00051，未修改API、generated、product、frontend、deploy或runtime config。
- Implementation status保持`NOT AUTHORIZED`；下一步是独立Implementation Readiness re-review，不是apply。

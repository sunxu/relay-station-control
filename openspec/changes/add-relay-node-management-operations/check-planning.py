"""Read-only planning checks; run from the Control repository root."""
from pathlib import Path
import hashlib
import re

ROOT = Path('openspec')
CHANGE = ROOT / 'changes/add-relay-node-management-operations'
STAGE2 = ROOT / 'changes/add-relay-node-asset-lifecycle-management'
PATTERN = re.compile(r'^### Requirement: ([^\n]+)\n.*?(?=^### Requirement: |\Z)', re.M | re.S)


def requirements(path):
    return {m[1]: m[0] for m in PATTERN.finditer(path.read_text())}


def scenarios(text):
    return set(re.findall(r'^#### Scenario: (.+)$', text, re.M))


count = 0
for delta in sorted((CHANGE / 'specs').glob('*/spec.md')):
    if '## MODIFIED Requirements' not in delta.read_text():
        continue
    prior_path = STAGE2 / 'specs' / delta.parent.name / 'spec.md'
    prior = requirements(prior_path)
    baseline_path = ROOT / 'specs' / delta.parent.name / 'spec.md'
    baseline = requirements(baseline_path) if baseline_path.exists() else {}
    for title, text in requirements(delta).items():
        assert title in prior, f'Unknown approved dependency title: {title}'
        if delta.parent.name == 'asset-registry':
            assert title in baseline, f'Unknown baseline title: {title}'
            assert scenarios(baseline[title]) <= scenarios(text), title
        assert scenarios(prior[title]) <= scenarios(text), f'Stage2 scenarios lost: {title}'
        count += 1
        print(f'PASS heading + scenario coverage: {delta.parent.name}: {title}')

expected = {
    'enable': (90, 'f41f88d2693f203399544c5cd210d48057abfd845f3d09ef3c0f5ca276cb25fe'),
    'disable': (92, 'b226ed7bfcc3cf1ec1c10eaf4084965217f3ce2fa914eef338b01c71732fbaf2'),
}
for action, (size, digest) in expected.items():
    encoded = (f'[1,"node.monitoring_{action}","11111111-1111-4111-8111-111111111111",'
               f'"administrator_{action}"]').encode('utf-8')
    assert len(encoded) == size and hashlib.sha256(encoded).hexdigest() == digest
    print(f'PASS canonical v1 bytes/hash: {action}')
assert not re.search(r'^- \[[xX]\]', (CHANGE / 'tasks.md').read_text(), re.M)
all_planning = '\n'.join(path.read_text() for path in [
    CHANGE / 'proposal.md',
    CHANGE / 'design.md',
    CHANGE / 'planning-validation.md',
    CHANGE / 'tasks.md',
    CHANGE / 'specs/asset-registry/spec.md',
    CHANGE / 'specs/relay-node-asset-lifecycle/spec.md',
    CHANGE / 'specs/relay-node-management-operations/spec.md',
])
normative_planning = '\n'.join(path.read_text() for path in [
    CHANGE / 'proposal.md',
    CHANGE / 'design.md',
    CHANGE / 'tasks.md',
    CHANGE / 'specs/asset-registry/spec.md',
    CHANGE / 'specs/relay-node-asset-lifecycle/spec.md',
    CHANGE / 'specs/relay-node-management-operations/spec.md',
])
for forbidden in (
    '因此可创建新的future activation',
    '不负责invalidate尚未持久化的operational scheduling intent',
    'details仅result/reason/latency_ms',
    'close/cancel→generation',
    'committed_at DESC, command_id DESC',
    'UUID 是同一 DB instant 的稳定 tie-break',
    '同committed_at UUID tie-break',
):
    assert forbidden not in normative_planning, f'Forbidden superseded normative contract: {forbidden}'
for required in (
    'asset_admin_command_receipts_node_disable_fence_idx',
    'F1 IS DISTINCT FROM F0',
    'monitoring_disable_fence_conflict',
    'already-disabled no-op',
    'instance_id,result,reason,latency_ms',
    'future-only cancellation是domain mutation但不锁定/更新generation',
    'A.committed_at < B.committed_at',
    "previous + interval '1 microsecond'",
    'ORDER BY committed_at DESC LIMIT 1',
    'MUST NOT UPDATE、DELETE或TRUNCATE',
    'Independent readiness review round 6',
):
    assert required in all_planning, f'Missing P1 hardening contract: {required}'
print(f'PASS {count} MODIFIED titles; implementation tasks completed = 0')
print('PASS strict Disable receipt ordering/retention, probe audit target, and conditional generation guards')
print('Normative body/approved semantics preservation still requires manual review.')

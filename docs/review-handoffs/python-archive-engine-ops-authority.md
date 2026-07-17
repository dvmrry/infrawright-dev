# Adversarial review handoff: final Python engine-ops authority

## Scope

This slice freezes the exact Python authority delegated by the final three
live Node differential suites. It adds one resurrection generator and three
fixtures. It changes no consumer test, runtime behavior, Make target, CI job,
or Python implementation.

Baseline: `a00510b46b04767d371bf7c05286d13b52784253`

Authority: CPython 3.13.13, UCD 15.1, Node 24.

## Frozen evidence

Exactly 47 delegated calls are recorded:

- assessment CLI: 8 records, SHA-256
  `015f0314d9200a54734e40be11fd2dfb19b5386e2c34ae60813d37823a43a057`
- differential: 30 records, SHA-256
  `339d1cd700aaa15d508292a4bdce651b912fd3485914a9174d4426ca1dbfc739`
- plan CLI: 9 records, SHA-256
  `e9244e12e7d5ef83f2a545bb21aeb23f784b516423345680a1595218d5251605`

Each record binds raw arguments, stdin, a fixed environment, material input
filesystem evidence, exact status/stdout/stderr, and report artifacts. Node
outputs are never recorded. Normalization is limited to generated workspace
and temporary-root prefixes.

The generator pins every blob in the 739-file baseline tree and embeds its own
source. The final archive updated the detached-worktree resurrection command to
recover the identical generator from owning commit `a3e39f3…` and verify its
recorded SHA-256 before installing pinned dependencies with `npm ci --ignore-scripts`,
regenerates all three fixtures, and compares them byte-for-byte.

## Adversarial findings and remediation

The initial review requested changes because the recorded resurrection lacked
dependency installation and the deleted-deployment symlink case omitted its
lexical workspace evidence. Both were fixed and the exact clean-checkout flow
passed all three comparisons.

Patch review then found an off-by-one guard that checked the related
deleted-overlay record. The guard now identifies human record 24 by exact
arguments, deployment environment, and stdin; validates both symlink targets
and both lexical/resolved trees; and includes a deliberate negative self-test.

Final patch re-review approved the authority with no remaining findings.

## Verification

- Two independent generations were byte-identical.
- Exact clean-checkout resurrection passed all three byte comparisons.
- The unmodified focused Node suites passed 33/33.
- `tests.test_ops` passed 119/119.
- The normalized record payload leak scan was clean.

The next slice may convert the three consumers only if it continues comparing
current Node results with these frozen Python outputs and never replaces them
with Node-vs-Node self-comparison.

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
  `cc611aa957d925c2dfb48b57caccbacb8eb5364a8a4b624f84ca17d14d9f6a36`
- differential: 30 records, SHA-256
  `a77718b7710feea17a5dd82818e4c1acd7cf31a5779d22520fb9a7a173017dc4`
- plan CLI: 9 records, SHA-256
  `54f2a3f6011a43e13b44e34a9caf25625ff112ed6ccbb8af8d5bdc0f08501359`

Each record binds raw arguments, stdin, a fixed environment, material input
filesystem evidence, exact status/stdout/stderr, and report artifacts. Node
outputs are never recorded. Normalization is limited to generated workspace
and temporary-root prefixes.

The generator pins every blob in the 739-file baseline tree, embeds its own
source, and records a detached-worktree resurrection command. That exact
command installs pinned dependencies with `npm ci --ignore-scripts`,
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

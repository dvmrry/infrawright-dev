# Adversarial review handoff: final Python oracle conversion

## Scope

This slice converts the final three live Python-oracle Node suites to the
reviewed frozen engine-operations authority. It removes the shared test-only
Python resolver and the test-suite exclusion that kept those files outside the
normal Node gate. It changes no production runtime, pack, Terraform, provider,
artifact, or deployment behavior.

Base: `fede4ea5ad74f37097c2468181d65b113481f609`

## Evidence preservation

- `assessment-cli.test.ts` consumes all 8 assessment records and compares
  exact status, stdout, stderr, and report bytes.
- `differential.test.ts` consumes all 30 topology, scope, plan-root, and
  root-catalog records. Root-catalog cases still derive current Node output and
  compare the committed catalog, rather than comparing frozen bytes to
  themselves.
- `plan-cli.test.ts` consumes all 9 CLI records and retains the historical
  assertion boundaries for invalid deployment and invalid root cases.
- Each suite pins the complete fixture SHA-256, validates its kind, version,
  suite, count, and ordered invocation mapping, and verifies referenced blob
  hashes before comparison.
- Normalization is limited to recorded ephemeral workspace prefixes.

All 47 frozen records are consumed exactly once in recorded order. The
generator, complete provenance, and exact clean-checkout resurrection command
remain available in the preceding authority commit and Git history.

## Test selection

The three converted tests now join the normal Node suite. Their real pack
requirements are declared explicitly for physically reduced profiles. The
selector no longer recognizes or reports Python-oracle exclusions, but it
retains the permanent fail-closed check for hardcoded Python subprocess calls
in selected Node tests.

## Verification

- `npm run build:test`
- the three converted suites plus selector suite with
  `PYTHON=/usr/bin/false`: 39/39 passed
- full-profile selector: 70 selected, 0 excluded
- whitespace check

## Review focus

Verify that every former Python delegation still compares current Node output
with frozen Python output, that path normalization cannot hide material value
differences, that root-catalog freshness is not a self-comparison, that pack
requirements do not hide the suites in applicable profiles, and that removing
the shared oracle resolver/exclusion does not weaken the hardcoded-Python
tripwire.

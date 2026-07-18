# Block C4 Dispatch and Differential Adversarial Review

## Blocking Findings

### Assert-adoptable production dispatch was not exercised

- Finding: the first corpus covered `assert-adoptable` only in the pure
  Terraform-gate predicate. Removing or misrouting the production switch case
  could leave every new built-binary test green.
- Source evidence: `go/cmd/iw/main.go` added the dispatch case while the first
  `TestBlockC4DispatchParseDifferentialAgainstNodeOracle` table had no
  `assert-adoptable` entry.
- Impact: the four-command wiring claim was not fully regression-protected.
- Required change: one bounded, mode-distinguishing built-binary differential.
- Fix: added `assert-adoptable --policy policy.json --help`. The command stays
  credential- and filesystem-free, fails if dispatch is absent, and detects
  accidental `assert-clean` routing because only `assert-adoptable` accepts
  `--policy`.
- Regression verification: the focused Node/Go differential passes byte for
  byte. The same fresh reviewer rechecked only this surface and changed the
  verdict from Request changes to Approve.

No blocking findings remain.

## Non-Blocking Risks

- One reviewer noted that no direct built-binary case forces a downstream
  `ProcessFailure` through `legacyPlanLifecycleCommand`. This is non-blocking:
  the shim is unchanged, its source-pinned nine-code set has unit coverage,
  every new branch visibly uses it, and the direct corpus already pins usage
  exit `2`. Expanding the corpus for another copy of lower-layer behavior was
  deliberately declined.
- The first handoff wording overstated the clean REPORT differential as the
  marker-producing proof. It now records the actual complementary evidence:
  direct clean REPORT bytes match Node, while existing
  `TestProjectionScopeMarkersNeverEnterReportBytes` constructs exponent
  guidance and rejects `projection_omit_if`, `float:*`, and `integer:*`
  markers in rendered REPORT bytes.

## Source Evidence Review

- Diff inspected: committed base `6d48af435bf98ecfc391bcdd6076e182770fc2aa`
  against `go/cmd/iw/main.go` and the complete new
  `go/cmd/iw/block_c4_differential_test.go`.
- Handoff inspected: yes; final scoped hashes recorded there.
- Node source inspected: `node-src/cli/main.ts` and the frozen Node bundle.
- Scanner comparison: value-option and flag sets, prefix walk, value
  consumption, `modules generate` exception, and stop conditions match Node.
- Platform-gate comparison: command predicate, state-aware staging,
  standalone-help exception, and pre-dispatch placement match Node;
  `clean-plans` remains ungated.
- Dispatch comparison: all four commands map to their corresponding reviewed
  command functions through the unchanged legacy shim.
- Legacy classification: source-pinned codes and exit-2 conversion match Node;
  unsupported-platform failure remains outside the shim.
- Provider schemas, OpenAPI contracts, and provider source: N/A.
- Missing evidence or review gaps: none blocking.

## Generated Artifact Review

- Reports reviewed: temporary clean assessment REPORT matches Node byte for
  byte.
- Schemas reviewed: N/A.
- Fixtures reviewed: mode-restricted temporary pack, deployment, module, and
  fake-Terraform inputs.
- Snapshots reviewed: `tfplan` and `tfplan.sources` match Node byte for byte;
  cleanup removes only the pair and preserves an unrelated file.
- Terraform argv: exact except for one whole private randomized assessment
  snapshot argument. The normalizer requires both the bounded assessment-dir
  and plan-name components; executable, CWD, `show`, `-json`, order, count,
  streams, and exit remain exact.
- Artifact drift accepted or rejected: no product artifact drift accepted.

## Verification

- Focused Block C4 direct differential: pass.
- Focused review-fix differential: pass.
- `go test -race ./cmd/iw -count=1`: pass before the one-table-entry fix; rerun
  after the fix is part of final integration verification.
- `go test ./... -count=1`: pass before the one-table-entry fix; rerun after the
  fix is part of final integration verification.
- `go vet ./...`: pass.
- Windows amd64 compile: pass.
- `gofmt` and `git diff --check`: clean.
- Reviewers did not edit the worktree.

## Verdict

**Approve.**

Two independent fresh read-only reviews were run. The dispatch/source reviewer
approved with a non-blocking direct-failure nit. The differential/report
reviewer requested one bounded test, then approved after the focused fix and
recheck. No unresolved blocking finding remains.

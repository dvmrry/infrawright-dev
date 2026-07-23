# Fresh Adversarial Review: Go PR 247 Parity Triage

## Blocking Findings

None.

## Non-Blocking Risks

- Risk: Go's internal numeric scope marker uses
  `strconv.FormatFloat(..., 'g', -1, 64)`, while Node uses `String(number)`;
  their textual exponent formatting can differ.
- Source evidence: the marker feeds only
  `projection_omit_if\x00<path>\x00<markers>` same-run duplicate detection in
  policy validation. Repository search found no artifact, report, or
  cross-language consumer. Integral, fractional, signed-zero, and unsafe
  integer equivalence cases collapse consistently within Go and match Node's
  equivalence classes.
- Why it is non-blocking: the marker is process-local validation state and its
  literal bytes are unobservable in the current runtime contract.
- Suggested follow-up: when block C adds Go plan-report or assert-adoptable
  reporting, re-confirm the internal scope marker does not enter report bytes.

## Source Evidence Review

- Diff inspected:
  `821e9b4c251c10af333990460b88f29793f4865e..5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1`.
- Handoff inspected:
  `docs/review-handoffs/go-pr247-parity-triage.md`.
- Provider schemas inspected: N/A; no provider schema behavior changed.
- OpenAPI/API contracts inspected: N/A.
- Provider source inspected: N/A.
- Pack metadata inspected: no pack metadata changed; the review verified the
  deferred pack/user merge remains scoped to Node Adopt and explicitly recorded
  for the future Go block C implementation.
- Fixtures or snapshots inspected: no fixture or snapshot changed.
- Other source evidence inspected:
  - Node PR 247 commit `a96746ed2b599779fedffa052c8f2950d40c73dd`.
  - Node and Go Terraform environment count/byte constants.
  - Node `isSupportedDriftPolicyVersion`, `numericScalarMarker`, and
    `jsonScalarMarker` against their Go ports.
  - Repository-wide marker consumer search.
- Missing evidence or review gaps: the post-247 candidate still requires the
  documented work-machine rerun. The real read-only provider leg remains
  outside this credential-free review.

## Generated Artifact Review

- Reports reviewed: none changed.
- Schemas reviewed: none changed.
- Fixtures reviewed: none changed.
- Snapshots reviewed: none changed.
- Count/coverage deltas reviewed: focused Go regressions and the expanded Node
  suite were reviewed; no readiness count changed.
- Artifact drift accepted or rejected: the ignored Node bundle changed as
  expected from PR 247 and reproduced with SHA-256
  `fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a`.
  RootCatalog, Transform, Topology, and Generation remained byte-identical and
  were accepted.

## Verification Re-run by Reviewer

- Go formatting and vet: pass.
- Full Go suite: 17 packages pass.
- Focused Terraform-environment and drift-policy regressions: pass.
- Four artifact byte-gates: pass against the rebuilt oracle.
- Oracle digest: reproduced as `fd4593c…`.
- Branch state reviewed at `c17bfee`: clean and synchronized with origin.

## Verdict

**Approve.**

Verdict rationale: the 4,096-entry Terraform environment bound faithfully
retains the 256-KiB byte cap; the lossless numeric drift-policy behavior is a
faithful port; the marker's spelling divergence is demonstrably inert in the
current tree; and the pack/user adoption-policy merge is correctly deferred to
the not-yet-authorized Go Adopt block rather than silently dropped or
prematurely invented.

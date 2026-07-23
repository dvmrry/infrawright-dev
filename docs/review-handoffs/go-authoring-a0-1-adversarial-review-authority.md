# Adversarial review: Go authoring A0.1 fixture authority

## Blocking Findings

The initial review independently found the same missing semantic join between
the manifest's `reviewed_not_applicable` values and report rows. The correction
now requires exact bidirectional correspondence and rejects values outside the
selected resource set. No blocking findings remain after re-review.

## Non-Blocking Risks

- `sdk_source_missing` proves at the contract layer that the valid import path
  has no manifest-bound SDK owner; it does not independently prove the provider
  AST imports or calls that path. This is non-blocking because the outcome is
  `no_source`, is non-viable, cannot enter success numerators, and cannot carry
  SDK or endpoint evidence. A1 must derive it only from a bound provider import
  and callsite.

## Source Evidence Review

- Diff, handoff, unchanged synthetic provider/schema source, report schema, and
  all source-first fixture authorities inspected.
- Exact filter-to-row authorization checked for verified and unverified input
  branches.
- Missing, unselected, authorized-but-applicable, unlisted-not-applicable,
  duplicate, and empty cases checked.

## Generated Artifact Review

- Canonical SHA-256 chain accepted:
  `8d65…b99f -> 04ef…a40f -> b98e…83bc -> e323…ca2`.
- After removing the authorized filter and transitive digest fields, the source
  report and OpenAPI diagnostics are semantically unchanged.
- Count and coverage drift: none.

## Verdict

**Approve with a bounded A1 follow-up:** derive `sdk_source_missing` only from
the captured, manifest-bound provider AST import/call.

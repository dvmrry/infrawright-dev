# Adversarial Review Record: data-only referents branch

Branch: claude/data-only-referents
Base: faa132b1 (main). Final head: a32b369a3417d74cdbff4c2c6b54998893c92b0c.
Reviewer: gpt-5.6-sol at max reasoning effort, fresh codex context per
round, per the AGENTS.md adversarial-review workflow. Builder: Claude
(Fable 5) coordinating gpt-5.6-luna implementation workers.

## Series summary

| Round | Scope | Verdict | Outcome |
|---|---|---|---|
| Gate | whole branch | Request changes (7 blockers) | all fixed |
| Recheck 1 | remediation | Request changes (5) | all fixed; -refresh=false threat later disproven empirically |
| Recheck 2 | remediation | Request changes (5) | all fixed; prior_state established as the true evidence container |
| Recheck 3 | remediation | Request changes (3) | all fixed; refresh-flag independence proven |
| Recheck 4 | closure | Request changes (4, tooling) | all fixed |
| Recheck 5 | closure | Request changes (4, tooling) | all fixed |
| Recheck 6 | closure | Request changes (3, tooling) | all fixed |
| Recheck 7 | closure | Request changes (3, tooling) | 2 fixed (a32b369a3417d74cdbff4c2c6b54998893c92b0c); 1 scoped out (below) |

Every round's full report is preserved in the session scratchpad and its
findings, fixes, and proof cycles are recorded in the branch's commit
messages and handoffs under docs/reviews/.

## Credited sound across the final rounds

The engine surfaces (registry/topology/modules/transform/envgen/
assessment/plan-roots), the kind-bound planned/prior-state evidence
contract, the ZIA pack wiring, the provider double, and all seven
committed captures were credited sound and unweakened for four
consecutive rounds. The refresh-flag-independence and prior-state
freshness claims are empirically proven by committed captures.

## Maintainer decision items (builder recommendation: accept)

1. Validator contract parity: recheck 7 asks the Python pre-promotion
   validator to also refuse structures production authorization
   refuses. Scoped out deliberately: the validator is documented as a
   fixture sanity layer; go/internal/plan/contract.go and its Go
   regressions (which consume these same fixtures) are the gate of
   record, so contract parity in Python is duplicate authority.
2. Documented trust boundaries (unchanged from earlier rounds): the
   attestation authenticates engine-created plans, not a writer who can
   forge plan and sidecar (same class as tfplan.sources); same-case
   duplicate names introduced tenant-side post-publication remain a
   provider-source residual.

## Maintainer verdict (Dave, 2026-08-02): ACCEPTED, with strip

Both decision items accepted. Additionally, the maintainer directed
stripping the fixture-tooling escalation accumulated over rechecks 5-7:
validate_captures.py, the TRANSACTION/--recover machinery, and
capture_validation_test.go are deleted; gen-captures.sh is a plain
regeneration script with environment sanitation, a version gate, and a
JSON/version sanity check. The Go plan-contract regressions (which
consume the fixtures and fail on any bad regeneration) are the gate of
record — this supersedes the tooling claims in the recheck handoffs.
Standing roster guidance recorded from this: match reviewer effort to
surface criticality; max-effort review of low-stakes tooling
manufactures disproportionate safeguards.

## Strip review (Sol medium, 2026-08-02)

One finding: the simplified script drops a vestigial -refresh=false plan
invocation the old refresh_true recipe ran before its captured plan.
Accepted by the builder: the invocation produced an unused plan file,
plans do not mutate state, and the regenerated pair still proves
byte-identity with the full plan suite green. Deletion clean otherwise;
no production or Go-side hardening touched.

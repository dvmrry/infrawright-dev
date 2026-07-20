# Block D1 Oracle Foundation — Adversarial Review

## Blocking Findings

None remain after correction.

The first review requested changes for two blockers:

1. The initial `terraform-exec` adapter bypassed the existing trusted-path,
   platform, bounded-stream, and process-tree boundary.
2. The initial test set did not provide the required fixture and production
   runner proof.

The builder replaced the adapter with `terraformcmd` for every phase, removed
`terraform-exec` from source and the module, and added the missing runner,
fixture, refusal, corrected-plan, policy, cleanup, timeout, and descendant
containment coverage. The corrected diff was then reviewed again.

## Non-Blocking Risks

- Risk: D1 intentionally reverses the earlier `terraform-exec` dependency
  steer.
- Source evidence: `terraform-exec v0.25.2` buffers stderr internally without
  the project bound and does not preserve the existing Darwin process-tree
  contract; `oracle_runner.go` now delegates to `terraformcmd`.
- Why it is non-blocking: the corrected implementation restores the already
  qualified execution boundary and does not build a new process stack.
- Suggested follow-up: later parcels must not reintroduce `terraform-exec`
  unless a future version or wrapper proves equivalent containment. The Block
  D dependency plan records this decision.

- Risk: the shared fixture assertion samples address and ID rather than every
  nested value and sensitivity-mask member.
- Source evidence: `oracle_fixture_test.go` also sends the full fixture through
  exact accepted-plan/state extraction, whose implementation compares complete
  values and masks.
- Why it is non-blocking: the exact extractors and adversarial inline vectors
  cover the safety decision; the sampled assertion is not the only gate.
- Suggested follow-up: expand full-object assertions if the frozen structural
  fixture grows.

## Source Evidence Review

- Diff inspected: uncommitted D1 files against `b6f6e66`.
- Handoff inspected: `go-block-d1-oracle-review.md`.
- Provider schemas inspected: the committed Terraform 1.15.4 structural
  fixture and focused local schema.
- OpenAPI/API contracts inspected: N/A.
- Provider source inspected: N/A.
- Pack metadata inspected: provider source/pin, Oracle provider block, and
  `drop_if_default` paths used by D1.
- Fixtures or snapshots inspected:
  `node-tests/fixtures/terraform-import-structure-v1.15.4.json`.
- Missing evidence or review gaps: no live-provider evidence is expected or
  permitted for this parcel.

The corrected review verified that both the typed
`tfjson.Plan.Complete != nil && *Complete` gate and the lossless raw literal
`complete === true` gate precede scratch Apply. Accepted-plan mode cannot
request Apply or state show.

## Generated Artifact Review

- Reports reviewed: None.
- Schemas reviewed: no generated schema changes.
- Fixtures reviewed: the committed Terraform structural fixture; no bytes
  changed.
- Snapshots reviewed: None changed.
- Count/coverage deltas reviewed: exact plan and state address coverage,
  including multi-resource and incomplete-coverage refusals.
- Artifact drift accepted or rejected: rejected; RootCatalog, Transform,
  Topology, and Generation all passed byte-identically with zero skips.

Independent safe checks passed: gofmt, vet, focused tests, race tests, full Go
suite, `go mod tidy -diff`, `go mod verify`, and all four standing differential
gates. No Terraform binary, provider API, credentials, tenant, or live/local
Apply was invoked during review.

## Verdict

**Approve with nits**

The corrected D1 closes both blockers, restores the qualified execution
boundary, retains the typed-plus-lossless complete gate, and adds sufficient
fixture and safety coverage to proceed to the coordinator commit.

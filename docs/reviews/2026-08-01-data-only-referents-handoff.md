# Builder Review Handoff: data-only referents branch

Per docs/review-handoff-template.md. Builder: Claude (Fable 5)
coordinating gpt-5.6-luna implementation workers with per-task
gpt-5.6-sol trailing reviews. This handoff covers the WHOLE branch for
the AGENTS.md adversarial gate.

## Intent

- What problem does this change solve? Tenant objects exposed only as
  Terraform data sources (motivating case: ZIA location groups) become
  first-class reference targets: name-keyed tokens, lookup sidecars,
  data modules publishing the standard root output contract, and a
  CI/CD-consumable dependency projection. Spec:
  docs/superpowers/specs/2026-08-01-data-only-referents-design.md
  (including two amendments made during review rounds).
- What user-visible behavior should change? `zia_location_groups` is
  fetchable/transformable/generable as a read-only data root; the four
  ZIA rule types resolve location-group references through
  tokens/lookups/remote state; plan-roots JSON carries
  `data_referents`.
- What behavior must stay unchanged? All committed-artifact bytes for
  existing generated types; every reference-machinery surface (tokens,
  binding derivation, resolvers, state-aware fallback) for generated
  referents; state-blind rendering bytes everywhere; managed-resource
  assessment contract semantics.

## Base / Head

- Base: `faa132b1c1177ea34a24ba669b9448cc7f77441e` (main)
- Head: branch `claude/data-only-referents` (this handoff's commit;
  spec/plan commits `a98aca3..fcb9e58b` precede seven implementation
  commits `57ce943c..f84d62bc`)
- Diff command: `git diff faa132b1...HEAD`

## Files Changed

- Files: engine — metadata (registry surface + load-time semantic
  pass), roots (topology admission, plan-roots projection,
  authoritative renderer, full-surface qualification), refedges (new
  shared edge resolver), envgen (referent acceptance, data-referent
  probing exception, delegation to refedges, full-profile tests),
  modulesgen (data modules, ActiveModuleResourceTypes), transform/
  transformrun/tfrender (data-lane ArtifactMode through the shared
  lifecycle, nested lookup shape, key discipline), plan (kinded
  assessment contract, lifecycle tests), assessment (kind threading,
  schema typing via data_source_schemas), cmd/iw (plan-roots emission,
  qualification admission); pack — packs/zia registry/pack.json; docs —
  spec, plan (with two correction blocks), this handoff.
- Files intentionally left untouched: fingerprint scanner; all other
  packs; zia_location_management's own static/dynamic location-group
  drops; the four rule overrides (net-zero after the drops correction).

## Source Inputs Consulted

- Provider schemas: vendored packs/zia data_source_schemas
  (zia_location_groups shape: optional name argument, numeric id,
  predefined flag).
- OpenAPI/API contracts: EXTERNAL evidence — zia.openapi.json GET
  /locations/groups (page/pageSize, max 1000) in the separate
  zscaler-skill repository's vendor tree, not in this repository.
- Provider source files: none directly; the recorded cassette
  TestLocationGroup.yaml is likewise external (zscaler-skill vendor).
  Residual unverified claims: exact pinned-provider pagination limits
  and provider-side duplicate/not-found behavior (mitigated: the data
  lane refuses non-bijective IDs, and a vanished group fails its
  per-name data-source read loudly).
- Pack metadata: packs/zia registry/pack.json; full.packset.json.
- Existing docs or design records: the spec and plan on this branch;
  docs/terraform-expression-bindings.md; state-topology docs.
- Other source evidence: acknowledged-drops sweep across all packs
  (recorded in the spec's verification results).

## Generated Artifacts

- Reports: none changed.
- Schemas: none changed.
- Fixtures: offline terraform show -json capture added
  (go/internal/plan/testdata/offline_remote_state_capture/) as the
  positive data-mode contract fixture.
- Snapshots: environment_roots_compatibility.json regenerated 453->456
  records — exactly the three files of the new zia_location_groups
  root added, zero existing records changed/removed; plan-roots.stdout
  authority golden gains the data_referents field (one line);
  full-surface backend-key digest re-pinned for the +1 root.
- Artifact drift intentionally expected: only the above. The transform
  authority tree was verified byte-identical (the reference wiring
  itself moves no committed bytes).

## Expected Delta

- Expected behavior change: data-referent registry mode end to end.
- Expected report/count changes: full-profile counts 151->152; module
  and root enumerations include the data type.
- Expected generated-output changes: the new data module and its root
  only.
- Expected no-op areas: every existing generated type's artifacts and
  resolvers; managed assessment evidence; state-blind bytes.

## Invariants Claimed

- Evidence must not be silently dropped: the assessment contract is
  kind-bound per reference-output type — managed accepts only
  module.<type>.<type>.this instances, data only
  module.<type>.data.<type>.items with canonical-address equality,
  index-key equality, scalar IDs; mixed modes refuse; empty modules
  require the kind's exact configuration.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: refedges is the
  single edge-qualification walk for envgen and plan-roots; manifest
  precedence pinned to transform.MergedTransformReferences.
- Ambiguity must stay classified: data referent in neither
  resource_schemas nor data_source_schemas fails closed; registry
  data_referent requires a real fetch object and excludes
  generate/adopt/derive.
- Provider-readiness counts must stay explainable: 151->152 with the
  single added type named.
- Adoption safety invariants: data-lane publication runs through the
  shared lifecycle; never writes imports/moves; retires all six stale
  artifact classes on a generated->data flip, leaving nothing
  stageable.

## Tests Run

- Commands: `go build ./...`, `go vet ./...`, `gofmt -l`,
  `go test ./... -count=1` (full corpus, green at head, run by the
  coordinator locally including the loopback-listener test workers
  cannot run), `make check-pack PACK=zia`.
- Focused regression and pre-fix/unsafe-mutation proof: every review
  round's findings carry recorded proof cycles (six per-task Sol
  reviews, all initially FAIL, all findings accepted and fixed);
  highlights — seeded-stale-artifact lifecycle regression proven
  against the pre-fix writer; empty-fetch nested-lookup bytes proven
  against the flat pre-fix shape; mode-agnostic contract mutation
  independently re-verified by the coordinator; module-interface
  cross-package regression proven pre-fix; schema-typing synthetic
  regression proven pre-fix; refedges self-root/dedup mutations; probe
  counting test proven pre-fix.
- Promotion efficiency: one working session (2026-08-01); full-corpus
  sweeps: 4 (one FAIL during Task 7 triage, rest green; no interrupted
  attempts).
- Tests not run and why: live-tenant lanes and external ZPA audits
  (skip by design in standard runs).

## Known Deferrals

- Managed-branch address parsing keeps its historical loose-prefix
  behavior (the data branch is exact); flagged as follow-up.
- ResourceSet lane cannot compute DataReferents (documented + pinned);
  the lane itself is slated for retirement.
- Seven surveyed latent data-only referents (time windows, device
  groups, devices, departments, groups, proxy gateway, zpa extranet
  partner) are follow-on pack work.
- List/filter-shaped data sources and cross-pack references are out of
  scope per the spec.
- Predefined URL categories migration is a distinct follow-on.

## Review Focus

- Highest-risk files: go/internal/plan/contract.go (evidence
  authorization), go/internal/transformrun/runner.go +
  go/internal/tfrender/transform_artifacts.go (data-lane lifecycle),
  go/internal/envgen/environment_generator.go (data-referent probing
  exception), go/internal/refedges/refedges.go (shared graph),
  packs/zia (first real data referent).
- Specific assumptions to attack: the kind-bound contract cannot be
  constructed kind-less; the probing exception changes no state-blind
  bytes and no generated-referent behavior; the regenerated manifest
  contains ONLY the three claimed records; the lifecycle's data mode
  cannot leak into generated-lane bytes; refedges parity with the
  pre-extraction envgen behavior (its refusal strings are pinned).
- Generated artifacts to compare: environment_roots_compatibility.json
  (456 records), plan-roots.stdout, the offline show.json capture.
- Edge cases that could silently overclaim/drop/weaken evidence: plan
  JSON shaped to exploit the data branch (address/index/id forgery);
  a generated->data type flip mid-lifecycle; empty tenants; tokens
  present with absent data-root state.

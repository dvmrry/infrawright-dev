# ZIA Predefined URL Category Root Membership Review Handoff

## Intent

- Prevent the generate-only `zia_url_categories_predefined` module from making
  the automatic `zia_url` root require configuration that Fetch, Transform,
  and Adopt cannot produce.
- Keep the predefined module available as a standalone generated root and as
  an explicitly grouped member when an operator deliberately supplies config.
- Keep every other resource's automatic and explicit root membership
  unchanged.

## Base / Head

- Base: `ba5df8598ab3533fe2073823a7b7f1ff517ebce6` (PR #213 head)
- Head: `feature/fix-generate-only-root-membership` (use the frozen commit in
  the review request)
- Diff command:
  `git diff ba5df8598ab3533fe2073823a7b7f1ff517ebce6...feature/fix-generate-only-root-membership`

## Files Changed

- Pack policy: `packs/zia/registry.json`
- Python/Node registry validation and root resolution
- Root-catalog producer, schema, and committed Zscaler catalog
- Focused Python/Node topology, registry, differential, and generated-root tests
- Small transition-catalog documentation update
- Files intentionally left untouched: provider source, generated Terraform
  modules, artifact rendering, Fetch, Transform, Adopt, Oracle, plan, and Apply

## Source Inputs Consulted

- Provider schemas: `packs/zia/schemas/provider/zia.json` only to confirm the
  generated resource remains present; no schema change
- OpenAPI/API contracts: N/A
- Provider source files: N/A
- Pack metadata: `packs/zia/registry.json`, especially
  `zia_url_categories_predefined`, `zia_url_categories`,
  `zia_url_filtering_rules`, and
  `zia_url_filtering_and_cloud_app_settings`
- Existing docs or design records: `docs/node-process-api.md`,
  `docs/schemas/root-catalog.schema.json`
- Other source evidence: Python and Node root-resolution implementations and
  generated-root differential tests

## Generated Artifacts

- Reports: None
- Schemas: additive optional `slug_group` boolean in root-catalog schema v1
- Fixtures: committed `catalogs/zscaler-root-catalog.v1.json` regenerated
- Snapshots: None
- Demo or lab outputs: Python/Node `zia_url` generated-root differential
- Artifact drift intentionally expected: one `slug_group: false` fact and the
  root-catalog source digest; the automatic `zia_url/main.tf` omits only the
  predefined module/variable

## Expected Delta

- Expected behavior change: automatic slug grouping leaves
  `zia_url_categories_predefined` in its standalone root instead of adding it
  to `zia_url`.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: automatic `zia_url` roots no longer
  declare or instantiate `zia_url_categories_predefined`; its standalone
  generated module/root remains available.
- Expected no-op areas: all other resource grouping, explicit groups, artifact
  bytes, module generation, Fetch, Transform, Adopt, Oracle, plan, and Apply.

## Invariants Claimed

- Evidence must not be silently dropped: the predefined type remains in the
  generated inventory and root catalog.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: pack registry owns the
  opt-out and the root catalog records it.
- Ambiguity must stay classified instead of being coerced to success: N/A.
- Provider-readiness counts must stay explainable: no readiness change.
- Adoption safety invariants: no empty default is introduced, so a missing
  configuration cannot silently become an empty desired collection; explicit
  groups remain authoritative.

## Tests Run

- `python3 -m unittest tests.test_artifacts tests.test_registry tests.test_gen_env tests.test_group_bindings tests.test_ops`
- `npm run build`
- `npm test`
- Focused Node topology/catalog/metadata/differential/environment tests
- Relevant output summary: 239 affected Python tests passed; complete Node
  suite passed; Python/Node generated-root trees and topology bytes agree.
- Tests not run and why: no live provider or Terraform test; the change is
  deterministic metadata/root composition and does not contact either.

## Known Deferrals

- Deferred work: classify any other generate-only resource that should opt out
  of automatic slug grouping.
- Reason it is safe to defer: `generate: true` alone does not prove a resource
  is reference-only; broad inference would incorrectly remove legitimate
  operator-configurable modules.
- Follow-up owner or trigger: add `slug_group: false` only with resource-specific
  evidence of the same missing-materialization condition.

## Review Focus

- Highest-risk files or paths: `engine/artifacts.py`,
  `node-src/domain/roots.ts`, `packs/zia/registry.json`, and the versioned root
  catalog/schema.
- Specific assumptions to attack: catalog-v1 backward compatibility; explicit
  group precedence; whether the excluded resource can still be selected and
  generated standalone; whether another resource is accidentally excluded.
- Source evidence the reviewer should verify: the predefined registry entry has
  no Fetch or Derive source and remains `generate: true`.
- Generated artifacts the reviewer should compare: automatic `zia_url` root,
  standalone predefined root, and the committed root catalog.
- Edge cases: old v1 catalogs without `slug_group`; explicit groups containing
  the opted-out resource; selectors for the opted-out resource; Python/Node
  parity; accidental `{}` defaults or silent state-destruction behavior.

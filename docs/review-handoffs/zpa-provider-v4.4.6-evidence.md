# Builder Handoff: ZPA Provider v4.4.6 Evidence

## Intent

- Freeze the provider-source facts the Node adoption lane needs before it can
  safely model the 16 fetch-backed ZPA resources.
- Add a machine-readable evidence matrix and a compact audit that fail closed
  when the local ZPA pack, schema, pinned provider checkout, or cited source
  bytes drift.
- Make the per-resource import grammar, Read identity behavior, state shape,
  sensitive-input boundary, and known exceptions reviewable without inferring
  that static source proves Terraform runtime compatibility.
- No adoption, import, projection, Terraform, Makefile, or Node runtime behavior
  changes in this slice.

## Base / Head

- Base: `7fe7b6c7e5263f5708d7fc378a15ea03d3c6a984`
- Head: checked-out `feature/node-zpa-provider-evidence` branch; resolve after
  the builder checkpoint with `git rev-parse HEAD`.
- Diff command: `git diff 7fe7b6c7e5263f5708d7fc378a15ea03d3c6a984...HEAD`

## Files Changed

- Files:
  - `docs/evidence/zpa-provider-v4.4.6.json`
  - `docs/zpa-provider-evidence.md`
  - `docs/review-handoffs/zpa-provider-v4.4.6-evidence.md`
  - `tools/zpa_provider_evidence.py`
  - `tests/test_zpa_provider_evidence.py`
  - `tests/pack-test-requirements.json`
- Files intentionally left untouched:
  - Node source, catalogs, schemas, process operations, and CLI entry points.
  - Python adoption/oracle implementation and existing ZPA pack behavior.
  - ZCC and ZIA evidence, projections, fixtures, and contracts.
  - Terraform-generated configuration and live tenant artifacts.

## Source Inputs Consulted

- Provider schemas:
  - `packs/zpa/schemas/provider/zpa.json`
- OpenAPI/API contracts:
  - None. This slice records Terraform provider-source behavior, not API
    completeness or endpoint semantics.
- Provider source files:
  - Official `zscaler/terraform-provider-zpa` tag `v4.4.6`, commit
    `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`.
  - `zpa/common.go` and the 16 relevant `zpa/resource_zpa_*.go` files listed
    with complete-file hashes in the evidence matrix.
- Pack metadata:
  - `packs/zpa/pack.json`
  - `packs/zpa/registry.json`
  - Nine relevant files in `packs/zpa/overrides/`, all named and hashed in the
    evidence matrix.
- Existing docs or design records:
  - `docs/adversarial-review.md`
  - `docs/review-handoff-template.md`
  - Existing Zscaler pack metadata, schema, and transform tests.
- Other source evidence:
  - Clean local checkout
    `/Users/dm/src/gh/dvmrry/zscaler-skill/vendor/terraform-provider-zpa`
    at the exact pinned commit for the source-backed audit.

## Generated Artifacts

- Reports:
  - `docs/evidence/zpa-provider-v4.4.6.json` is a curated,
    machine-readable evidence matrix. The audit validates its pins and local
    derivations; it does not generate or semantically parse the Go claims.
- Schemas:
  - None.
- Fixtures:
  - None.
- Snapshots:
  - None.
- Demo or lab outputs:
  - None; no tenant values, Terraform state, credentials, or live API output
    are committed.
- Artifact drift intentionally expected:
  - One new 16-resource evidence matrix and its builder handoff. Existing
    generated catalogs, schemas, and fixtures must remain byte-identical.

## Expected Delta

- Expected behavior change:
  - None. Maintainers gain a pinned evidence seam and audit command for the
    future ZPA Node port.
- Expected report/count/coverage changes:
  - 16 fetch-backed resource rows.
  - 16 generated-config runtime evidence gates.
  - 14 numeric-or-alternate custom importers and 2 passthrough importers.
  - 1 resource with provider-sensitive input paths.
  - 3 resources whose schema `id` attribute is not populated by an explicit
    provider-source assignment.
  - Schema-derived totals of 238 input attributes and 27 nested input blocks.
- Expected generated-output changes:
  - Only the new curated matrix. No existing generated output changes.
- Expected no-op areas:
  - All runtime import, oracle, projection, planning, and transform behavior.

## Invariants Claimed

- Evidence must not be silently dropped:
  - The exact registry fetch set must equal the matrix resource set.
  - Every import, Read-identity, and exception claim must cite an inclusive
    SHA-256-bound provider-source range.
- Generic matcher evidence must not outrank source-backed evidence:
  - No generic matching behavior is added. The matrix binds resource-specific
    claims to exact upstream source bytes.
- Source precedence/provenance must remain explicit:
  - The official provider repository, tag, commit, full source-file hashes,
    source-range hashes, and local input hashes are mandatory.
- Ambiguity must stay classified instead of being coerced to success:
  - All 16 generated-config rows remain
    `terraform_runtime_evidence_required`; entitlement-optional fetch statuses
    do not qualify or suppress oracle failures.
- Provider-readiness counts must stay explainable:
  - Matrix summary counts are recomputed from the rows and schema-derived
    shapes, not accepted as independent assertions.
- Adoption safety invariants:
  - The tool deliberately does not parse Go or infer provider semantics.
  - Static evidence does not authorize a ZPA Node catalog entry.
  - Provider-sensitive inputs remain identified and are not synthesized,
    persisted, or transported.
  - No secret values, live tenant outputs, or Terraform artifacts are present.

## Tests Run

- Commands:
  - `python3 tools/zpa_provider_evidence.py --provider-root /Users/dm/src/gh/dvmrry/zscaler-skill/vendor/terraform-provider-zpa`
  - `python3 -m unittest -v tests.test_zpa_provider_evidence`
  - `ZPA_PROVIDER_SOURCE=/Users/dm/src/gh/dvmrry/zscaler-skill/vendor/terraform-provider-zpa python3 -m unittest -v tests.test_zpa_provider_evidence`
  - `python3 -m engine.audit_vendor_boundary`
  - `make test`
  - `git diff --check`
- Relevant output summary:
  - Local and external source audits validate all 16 rows.
  - Focused suite: 7/7 pass with the external provider source; without the
    opt-in environment variable, 6 pass and the external-source case skips.
  - Vendor boundary: 192 allowed files, 0 violations.
  - Full Python suite: 1,372 tests passed, 1 intentional external-source skip,
    0 failures.
  - Diff whitespace check passes.
- Tests not run and why:
  - No live Terraform import, `-generate-config-out`, or tenant operations were
    run; those are explicitly the evidence gates this static slice preserves.
  - Node tests were not required because no Node source, contract, or build
    artifact changes.

## Known Deferrals

- Deferred work:
  - Per-resource live Terraform import, state, and generated-config evidence
    for all 16 rows.
  - ZPA Node catalogs, state validators, import/oracle operations, and Python
    byte-parity fixtures.
  - Live entitlement behavior for optional fetch statuses.
  - The broader ZPA reference graph beyond the facts represented here.
- Reason it is safe to defer:
  - No runtime support is enabled by this change, and all unsupported behavior
    remains explicitly gated rather than inferred from static source.
- Follow-up owner or trigger:
  - The next ZPA Node implementation slice, after this evidence change receives
    a fresh adversarial review and the required runtime fixtures exist.

## Review Focus

- Highest-risk files or paths:
  - `docs/evidence/zpa-provider-v4.4.6.json`
  - `tools/zpa_provider_evidence.py`
- Specific assumptions to attack:
  - The 14 custom importers' base-10 parse and alternate-lookup behavior.
  - App connector group, application server, and application segment Read
    identity classifications.
  - The negative claim that BA certificate, emergency access, and inspection
    profile have no explicit source assignment to the schema `id` attribute.
  - Inspection profile's undeclared `profile_id` importer write.
  - Sensitive-input classification for PRA credential controller.
  - The assertion that every generated-config row remains runtime-gated.
- Source evidence the reviewer should verify:
  - Every curated claim against the cited range at the exact pinned commit,
    especially negative/no-assignment claims that cannot be established by a
    single positive line.
  - Complete source-file hashes and source-range hashes against a clean
    official checkout.
- Generated artifacts the reviewer should compare:
  - Matrix resource set against the exact fetch-backed registry set.
  - State-shape rows and totals against the committed provider schema.
  - Local input hashes against the pack, registry, schema, and overrides.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - Treating nonnumeric import text as an opaque Terraform ID instead of an
    alternate lookup.
  - Applying a global `values.id` rule to all ZPA resources.
  - Treating SDK behavior or generated HCL as proven by source inspection.
  - Allowing optional collection statuses to become adoption/oracle skips.
  - Treating the compact audit as a semantic Go parser or an independent proof
    of the curated interpretations.

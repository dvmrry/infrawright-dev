# ZPA Provider 4.4.9 Review Handoff

## Intent

- Refresh only the ZPA pack and its source-bound evidence from provider `4.4.6`
  to `4.4.9`.
- Close the existing `device_posture_failure_notification_enabled` raw-field
  drop for `zpa_policy_access_rule`, where the new schema and exact provider
  source both show Read and expand wiring. Expose the same schema-backed module
  input for `zpa_policy_access_rule_v2`, whose provider source has the same
  wiring.
- Regenerate module types for the reviewed list-to-set, capabilities, and portal
  schema transitions without adding provider-specific behavior to the generic
  transform or field-lineage engines.
- Preserve the portal capability block as an at-most-one module input even
  though the `4.4.9` schema removed `max_items: 1`: the provider still consumes
  only element zero. Express that fail-closed constraint as a strict optional
  one-element tuple declared by pack metadata and interpreted by the generic
  module generator, without naming ZPA resources or fields in generic code.
- Keep ZTC, registry membership, API mappings, and evidence-free browser
  empty-string omissions unchanged.

## Base / Head

- Base: `origin/main` at `51a6577aa5036a6e3c094df3d1b79cb4d2f8735c`.
- Original implementation: branch `feature/zpa-provider-4.4.9` at
  `21517493d95abb320b8fc9a602da22cc3a408c7e`.
- First-review handoff: `fa970eeef28e00b60ea389f33817a757d3711aa0`.
- First correction, rejected by focused recheck:
  `f4466252b45434fc8087af7cd2d05335175d456d`.
- Second correction: `2596ae04a28c4405aebb9c4578f79056fc396bb5`.
- Handoff: the branch tip has a documentation-only commit updating this file
  after the second correction.
- Diff command:
  `git diff 51a6577aa5036a6e3c094df3d1b79cb4d2f8735c...2596ae04a28c4405aebb9c4578f79056fc396bb5`.

## Files Changed

- Files: the ZPA pack and extractor pins; the checked-in ZPA provider schema;
  the access-rule override and generated demo output; the ZPA source-evidence
  matrix and endpoint fixture; bounded schema/source tests; module and parity
  compatibility fixtures; the full-demo `DROPS_CHECK` gate; current-pin
  documentation and changelog; the generic `module_single_blocks` override
  validator and module-generator overlay; the portal pack override; focused
  generator and Terraform-cardinality tests; this handoff.
- Files intentionally left untouched: ZPA registry membership and fetch/API
  mappings; the generic transform, projection, field-lineage, and
  source-analysis implementations; every ZIA and ZTC pack file; both browser
  overrides apart from tests proving no new default omission exists. The
  checked-in provider schema remains byte-for-byte the signed upstream schema.

## Source Inputs Consulted

- Provider schemas: a fresh `terraform providers schema -json` extraction from
  the signed `registry.terraform.io/zscaler/zpa` `4.4.9` binary. The sorted
  checked-in schema SHA-256 is
  `b1edfac52dea6abf014328f4cd0eebc64619fdc96b31f7d6e0a975f2bc712508`.
- OpenAPI/API contracts: none; this refresh does not claim new API mappings or
  resource coverage.
- Provider source files: `zscaler/terraform-provider-zpa` tag `v4.4.9`, commit
  `1d4f43cc4c59a24d8380f0c655a07b6da7199465`, plus the exact `v4.4.6`
  tag for the version-to-version comparison. The optional bound-source tests
  replay the complete provider file and cited range hashes. A pinned-source AST
  assertion also verifies that `expandPrivilegedPortalCapabilitiesRule`
  consumes `privCapsList[0]` exactly once and does not iterate the collection.
- Pack metadata: ZPA pack manifest, 54-entry registry, overrides, checked-in
  schema, seven available raw demo fixtures, parity fixture, and generated
  module compatibility corpus.
- Existing docs or design records: `docs/zpa-provider-evidence.md`, the shared
  override contract, absent/default normalization policy, and repository
  adversarial-review contract.
- Other source evidence: `zscaler-sdk-go/v3` `v3.8.42`; its captured subset
  tree SHA-256 is
  `68ae8ed86f785f03d762228d9c7ab70b4882098843a6b31ec8bc2d1aafad0384`.
  The refreshed endpoint analyzer still observes 15 SDK calls and one
  deliberately ambiguous policy-rule result; it makes no raw HTTP endpoint
  claims.

## Generated Artifacts

- Reports: the ZPA endpoint source-evidence report is re-pinned and
  independently replayed. Its SHA-256 is
  `ec3fada6f362a38491b08e86dfa10920a81507e1bd95cdf8d62b425ce2c53e2e`.
- Schemas: `packs/zpa/schemas/provider/zpa.json`; inventory remains 55 resource
  schemas and 71 data-source schemas. The schema is not edited to restore the
  removed portal `max_items`; the constraint is a pack-owned module-generation
  overlay.
- Fixtures: the endpoint fixture moves from `zpa-v4.4.6-endpoint-v1` to
  `zpa-v4.4.9-endpoint-v1`. Its source manifest SHA-256 is
  `84fa0b2b2002888fc96f69910e97b6a62f78f74057cc13792435e82dcd77425f`.
  The access-rule demo and V2 transform golden now retain
  `device_posture_failure_notification_enabled: false` on both sample items.
- Snapshots: module-HCL compatibility and transform/adopt parity are refreshed;
  their SHA-256 values are respectively
  `5fb57177483130e360ccf5ccc20fe5c0f30eeaecbf700121b918cb1a159d7a37`
  and `baeeca9097824387c1779f2dba3e05be5e3e2c2480c94c1cf29cf6270332d106`.
  The portal generated module deliberately renders an optional one-element
  tuple rather than exposing the schema's now-unbounded list or Terraform's
  lossy singleton-object conversion.
- Demo or lab outputs: committed JSON demo output was regenerated. No
  credentialed tenant output is retained.
- Artifact drift intentionally expected: 16 effective schema transitions, ZPA
  module provider constraints `4.4.6 -> 4.4.9`, the admitted access-rule bool,
  and source-version/hash/line provenance changes.

## Expected Delta

- Expected behavior change: raw access-policy transforms retain
  `device_posture_failure_notification_enabled`, including `false`. Generated
  modules for access rule and access-rule-v2 accept and render the bool.
- Expected report/count/coverage changes: provider and SDK provenance advances;
  the source matrix remains 16 fetch-backed resources; provider inventory
  remains 55/71; pack registry remains 54; raw ZPA exercise remains 7/54.
- Expected generated-output changes: the 16 schema transitions are one
  Optional+Computed-to-Optional bool, three list-to-set nested blocks, six
  appearances of the device-posture bool, two capability booleans, removal of
  one nested-block `max_items`, and three portal sandbox booleans. The raw
  schema transition is retained, but the portal module interface becomes a
  strict optional one-element tuple because the provider still ignores all
  elements after index zero.
- Expected no-op areas: no new resources become fetch, adoption, or API-mapping
  targets; transform, projection, field-lineage, and source-analysis behavior
  does not change; parity values remain unchanged; ZIA and ZTC are untouched.
  The only generic production behavior added is the pack-declared,
  generator-only singleton overlay.

## Invariants Claimed

- Evidence must not be silently dropped: the one closed access-rule hold has
  both schema and provider `d.Get`/`d.Set` evidence, and the generated demo
  proves the false value survives transform. `DROPS_CHECK=1` gates the full
  exercised demo corpus. Portal capability cardinality is constrained before
  provider execution: one tuple element preserves its configured boolean in a
  mocked plan, while a second element and a keyed-object collection bypass are
  both rejected.
- Generic matcher evidence must not outrank source-backed evidence: no matcher
  or lineage implementation changed. The exact pinned-source AST assertion is
  test-only and verifies only the device-posture field's direct provider
  wiring.
- Source precedence/provenance must remain explicit: schema, provider, SDK,
  matrix, manifest, report, and compatibility-corpus digests are pinned.
- Ambiguity must stay classified instead of being coerced to success: the
  existing ambiguous policy-rule endpoint remains ambiguous, and the four
  inherited-but-unwired device-posture schemas are documented as unproven
  upstream-schema surface rather than behavioral support.
- Provider-readiness counts must stay explainable: catalog, registry, fetched
  evidence, and raw-fixture counts are reported separately; none is promoted
  into another coverage claim.
- Adoption safety invariants: none of the three list-to-set resources uses
  `key_field`, `import_id`, or `sort_lists` identity derived from the changed
  blocks, so the change affects generated values/types rather than Terraform
  resource addresses. No adoption classification or import path changes. The
  singleton overlay deep-clones the schema used for module generation and does
  not mutate the cached provider schema used by other consumers.

## Tests Run

- Commands: exact qualified endpoint replay with
  `ZPA_PROVIDER_SOURCE=/tmp/infrawright-zpa-449.HL2cBK/provider` and
  `ZPA_SDK_SOURCE=$GOMODCACHE/github.com/zscaler/zscaler-sdk-go/v3@v3.8.42`;
  focused `zpacorpus`, `modulesgen`, parity, and V2 transform tests;
  `make check-pack PACK=zpa`; `make check-pack-set check-modules
  check-tfvars-fmt check-pack`; `make check`; `go vet ./...`; repository-wide
  `gofmt -d`; and `git diff --check`.
- Relevant output summary: all commands pass. `make check` passes on the exact
  second correction commit `2596ae04a28c4405aebb9c4578f79056fc396bb5`.
  The external replay validates all pinned provider
  and SDK bindings and reproduces the 15-observed/one-ambiguous report. The
  full Go suite passes.
- Commands: with Terraform `v1.15.4`,
  `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-terraform-plugin-cache
  INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run
  '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`.
- Relevant output summary: 151/151 generated-module semantics, 20/20 demo-root
  semantics, and the HCL-tfvars deployment case pass. A wrapper around the
  generated portal module proves a one-element tuple preserves
  `delete_file = true` in the mocked provider plan, while a two-element tuple
  and the keyed-object counterexample are rejected before provider execution.
  One preceding sweep had a transient `zpa_application_server` init failure;
  the exact retry passed that module and the complete 151/151 surface.
- Tests not run and why: no credentialed ZPA tenant fetch/import/no-op-plan or
  upstream acceptance suite was run because credentials and tenant mutation
  were not authorized.

## Adversarial Review Corrections

- Finding: the first fresh, read-only review requested changes because the
  `4.4.9` schema removed `max_items: 1` from
  `privileged_portal_capabilities`, while the provider expand function still
  reads only element zero. Trusting the raw cardinality would expose a module
  input whose additional values are silently discarded.
- Root cause: generated module cardinality followed the provider schema without
  a way for source-backed pack evidence to impose a stricter fail-closed
  boundary.
- First correction attempt: add a validated generic `module_single_blocks`
  override and render the constrained block as the generator's existing
  singleton object. The first focused recheck rejected this because Terraform
  accepted `{first = {...}, second = {...}}`, discarded both unknown keys
  during all-optional object conversion, and planned one empty block.
- Second correction: retain the pack declaration and deep-cloned schema view,
  but render a constrained list/set block as an optional one-element tuple.
  This retains a collection-shaped input while rejecting both multiple elements
  and the exact keyed-object bypass. No ZPA resource or field name is hardcoded
  in generic production code, and transform behavior is unchanged.
- Regression proof: unit tests cover override validation, top-level and dotted
  paths, missing/stale paths, conflicting minimum cardinality, strict tuple HCL
  rendering, and cached-schema immutability; the pinned provider-source AST test
  guards the element-zero behavior; the Terraform checkpoint inspects the
  mocked one-element plan and rejects the two lossy shapes.
- Correction verification: focused Go tests, external provider/SDK replay,
  `make check-pack PACK=zpa`, full `go test -count=1 ./...`, `go vet ./...`,
  repository-wide `gofmt -d`, `git diff --check`, `make check`, and the complete
  Terraform semantic checkpoint all pass.

## Known Deferrals

- Deferred work: add `drop_if_default` for browser-access `ext_domain` or
  `ext_label` when empty.
- Reason it is safe to defer: there is no retained raw or runtime witness that
  distinguishes provider-default empties from intentional empty configuration;
  the repository policy forbids guessing. Both browser overrides are asserted
  to contain no such rule.
- Follow-up owner or trigger: a sanitized raw/provider-state fixture or live
  no-op-plan that proves a field-specific omission discriminator.
- Deferred work: claim support for
  `device_posture_failure_notification_enabled` on forwarding, redirection,
  timeout, or nested LSS policy schemas.
- Reason it is safe to defer: the provider's shared schema exposes the field,
  but exact source inspection finds direct `d.Get` and `d.Set` wiring only in
  access rule and access-rule-v2. The generated schema-backed inputs are not
  represented as source-verified round-trip support.
- Follow-up owner or trigger: provider source wiring or credentialed
  create/import/read/no-op-plan evidence for each resource.
- Deferred work: credentialed true-to-false update verification for
  `zpa_policy_access_rule_v2`, whose SDK field is tagged `omitempty`, and the
  pre-existing portal Read condition that gates capabilities using a different
  field.
- Reason it is safe to defer: both are disclosed upstream-runtime risks rather
  than regressions introduced by this branch. The first needs a live tenant
  round trip; the second does not weaken the new write-side cardinality guard.
- Follow-up owner or trigger: a sanitized provider-state fixture, upstream
  provider fix, or authorized credentialed no-op-plan test.
- Deferred work: broaden the ZPA raw corpus and perform any ZTC refresh.
- Reason it is safe to defer: the 7/54 ZPA and 0/16 ZTC raw-fixture limits are
  explicit, and no provider-wide `DROPS_CHECK` completeness claim is made.
- Follow-up owner or trigger: separate fixture-first ZPA and ZTC work.

## Review Focus

- Highest-risk files or paths: the regenerated provider schema; access-rule
  override and demo drift; source matrix and endpoint fixture provenance;
  `provider_refresh_test.go`; the metadata override validator; module-generator
  clone/overlay logic; the portal override; the pinned-source AST assertion;
  the V2 Terraform cardinality test; module/parity compatibility snapshots.
- Specific assumptions to attack: the schema diff has exactly the documented
  16 transitions; only access rule and access-rule-v2 directly read and expand
  the device-posture bool; list-to-set changes do not influence item identity;
  no evidence-free browser omission was added; the full-demo drop gate is not
  vacuously presented as 54-resource coverage; the singleton overlay cannot
  mutate shared schema state or silently become stale; one portal capability
  survives the plan; both a two-element list and the prior keyed-object bypass
  fail before the provider can discard data.
- Source evidence the reviewer should verify: the signed `4.4.9` schema digest;
  exact `v4.4.9` provider `d.Get` and `d.Set` sites; the `v4.4.6 -> v4.4.9`
  source/range differences; SDK `v3.8.42` bindings; matrix and endpoint report
  hashes.
- Generated artifacts the reviewer should compare: reproduce the provider
  schema digest and 55/71 counts; inspect the 16 transition sites; compare
  parity outputs for behavior drift; verify module types and access-rule demo
  changes match the schema.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  false-valued booleans disappearing; dotted nested `drop_if_default` paths;
  treating shared-schema inheritance as provider behavior; list/set ordering;
  mistaking generated module exposure for source-backed support; treating seven
  raw fixtures as provider-wide coverage; stale or over-broad singleton
  overrides; lossy Terraform collection-to-object coercion; accidental ZTC or
  transform/lineage-engine drift.

# Adoption Roadmap

This is a checkpoint after the import-oracle, static advisory, provider-lab,
diagnostic, and audit-hardening work. It is not a remediation design, and it
does not certify any provider for production adoption by itself.

## What Is Proven

The current adoption stack is viable across several non-trivial provider
surfaces:

- Key-only inventory can drive import-oracle adoption:
  `key/import_id -> Terraform/OpenTofu import -> provider state projection`.
- Static advisory reports restore the "what is Terraform blind to?" signal when
  raw detail JSON is available.
- Provider labs can isolate failure classes without committing raw API details,
  state, plans, credentials, logs, or temporary roots.
- Diagnostic tools can classify the major lab findings without changing
  projection, rendering, drift policy, provider config, or Terraform execution.
- Oracle safety hardening is in place for local scratch state, backend/cloud
  rejection, timeouts, redacted errors, strict keep-workdir parsing, malformed
  JSON, and path grammar.

## Provider-Lab Evidence

| Lab | Main signal | Carry-forward |
|---|---|---|
| Kubernetes smoke | Real Terraform and OpenTofu import smoke | Engine mechanism works outside synthetic fixtures. |
| NetBox | Absent/default placeholder drift | Provider-specific empty, zero, and null semantics need diagnostics before normalization. |
| Grafana | Sensitive-but-required nested blocks | Sensitive provider state can also be structurally required for valid config. |
| Cloudflare | Dynamic schema attrs, identity aliases, singleton/default drift | Pack metadata must represent identity aliases, and dynamic schema paths need deliberate strategy. |
| Google Cloud | Historical provider-config attribution drift, billing/API boundaries, API/provider shape drift | Provider config requirements are a distinct pack concern, not drift tolerance. A later retest did not reproduce the old attribution-label drift, so it is not current live validation for guidance annotations. |
| AWS | Core adoption loop across CloudWatch Logs, S3, IAM role, IAM policy, and security group; no provider-config blocked path from `default_tags` or `ignore_tags` | Empty prefix/reference placeholders need absent/default classification before metadata. Prefix fields are mutually-exclusive conflict candidates, distinct from NetBox-style empty enum/default placeholders. |

The evidence set is enough to stop asking whether the oracle approach can work.
The next phase is deciding which diagnosed classes become pack metadata,
provider-specific policy, or explicit non-automatable boundaries.

## Diagnostic Classes Implemented

| Class | Status | Purpose |
|---|---|---|
| Static advisory diff | Implemented | Compare raw API leaf paths, oracle provider state paths, projected tfvars paths, and policy omissions. |
| Sensitive blocked | Implemented | Derive sensitive provider-observed paths from oracle-state `sensitive_values` when projection omits them. |
| Sensitive present | Implemented | Derive sensitive provider-observed paths from oracle-state `sensitive_values` when projection includes them. |
| Block/container policy omissions | Implemented | Classify provider-observed descendants under block-level `projection_omit` entries without hiding raw-only paths. |
| Identity alias metadata | Implemented | Map raw, import, and state identity fields explicitly without name inference. |
| Dynamic schema diagnostics | Implemented | Classify map keys, dynamic values, open object members, computed-only fields, and unknown schema paths. |
| Absent/default diagnostics | Implemented | Classify placeholder-shaped projected values and saved-plan absent/default drift candidates. |
| Absent/default assert-adoptable guidance | Implemented | Annotate blocked plan paths that match manual-review absent/default metadata while keeping plans blocked. |
| Sensitive-required diagnostics | Implemented | Separate schema-required sensitive paths, validation-required paths, optional sensitive candidates, and projected sensitive paths. |
| Provider-config diagnostics | Implemented | Map saved-plan drift paths to explicit `provider_config.requirements` metadata. |
| Provider-config assert-adoptable guidance | Implemented | Annotate matching blocked plan paths while keeping plans blocked. |
| Shared schema/path helpers | Implemented | Normalize `[]` selectors, quoted map selectors, container paths, and Terraform schema status lookups. Used consistently by absent/default, dynamic-schema, and sensitive-required validators for rule identity and sensitive-path checks. |

These tools are diagnostic-only unless a future PR explicitly promotes one class
into narrow, reviewed behavior.

## Known Guidance, Behavior, And Reporting Classes

Open classes should remain separate until lab evidence proves the smallest safe
behavior:

- Provider-config guidance for blocked drift; validator metadata is implemented
  in [Provider Config Requirement Guidance](provider-config-remediation.md), but
  provider-config rendering and mutation are out of scope.
- Provider-specific absent/default normalization rules; design proposed in
  [Absent/Default Normalization Design](absent-default-normalization.md), not
  implemented. Any future omit behavior must reuse the existing
  `projection_omit` path and remain blocked until runtime discriminator and
  kind/action constraints are proven.
- AWS absent/default classification; design proposed in
  [AWS Absent/Default Placeholder Classification](aws-absent-default-classification.md),
  with manual-review-only metadata committed under `provider_absent_placeholder`.
  AWS `name_prefix` and `bucket_prefix` findings are represented
  conservatively as mutually-exclusive prefix-conflict variants in rule reason
  text. No omit behavior is authorized without a future runtime discriminator.
- Dynamic schema remediation strategy for opaque maps, open objects, and
  dynamic attributes.
- Sensitive-required remediation, manual override, or explicit cannot-adopt
  handling for structurally required secrets.
- `nested_type` and object projection support.
- Schema-aware set diffing.

## What Not To Automate Yet

The current evidence argues against global automation for these cases:

- Do not auto-generate secret placeholders for sensitive required blocks.
- Do not normalize empty, zero, false, null, list, or map values globally.
- Do not keep or drop dynamic schema paths without pack-owned intent.
- Do not render or mutate provider config from diagnostics.
- Do not treat provider labs or static advisory reports as production
  certification.

Each behavior PR should cite provider-lab evidence, add focused fixtures, and
preserve the existing fail-loud behavior outside its narrow class.

## Recommended Next Implementation Order

1. Re-review absent/default normalization semantics after the `projection_omit`
   relationship, runtime discriminator requirement, kind/action matrix, and V1
   path namespace are explicit.
2. Design a runtime discriminator for AWS absent/default prefix conflicts and
   optional reference placeholders before proposing any omit behavior. Committed
   AWS metadata is manual-review-only and does not imply projection or omission.
3. Propose dynamic schema remediation semantics.
4. Run a billing-enabled Google Cloud lab or another focused AWS/Azure lab.

After each behavior proposal, run at least one provider lab that originally
exposed the failure class before generalizing the behavior.

## Pack Metadata Checkpoint

Lab-derived adoption metadata is now committed in pack manifests:

| Provider | Metadata class | Evidence | Status |
|---|---|---|---|
| Google Cloud | `provider_config.requirements` | `docs/provider-labs/gcp-pr38.md` | Historical lab evidence, guidance-only. Current retest did not reproduce the old drift. |
| NetBox | `absent_defaults.rules` | `docs/provider-labs/netbox-pr22.md` | Validated, manual-review only. |
| Cloudflare | `absent_defaults.rules` + `dynamic_schema.rules` | `docs/provider-labs/cloudflare-free-tier-pr32.md` | Validated, manual-review only. `cloudflare_zone_hold` is intentionally classified as `provider_server_side_singleton_default`, distinct from NetBox-style `provider_absent_placeholder`. |
| Grafana | unclassified | `docs/provider-labs/grafana-pr24.md` | Sensitive-required design, contract, and validator landed; pack metadata still pending. |
| AWS | `absent_defaults.rules` | `docs/provider-labs/aws-free-core-pr77.md` | Validated, manual-review only. Prefix placeholders are conservatively represented under `provider_absent_placeholder` as mutually-exclusive prefix-conflict variants. No omit behavior is authorized; a runtime discriminator is still required before any omit action. |

## Validator Contract Documentation

The V1 validator contracts for all three adoption metadata lanes are now documented with the same rigor:

- `docs/sensitive-required-remediation.md` — frozen V1 validator contract (already complete).
- `docs/absent-default-normalization.md` — V1 validator contract backfilled: accepted keys, required fields, conditional `observed_value`, kind/action enum, matrix, path canonicalization, identity/conflict rules, provider/resource checking, error categories, and test matrix.
- `docs/dynamic-schema-remediation.md` — V1 validator contract backfilled: accepted keys, required fields, kind/ownership enum and matrix, path canonicalization, provider-version rule, identity/conflict rules, provider/resource checking, error categories, and test matrix.

These contract sections are documentation-only. No validator behavior changed.

The sensitive-required error-category contract wording now matches the sibling
validator contracts: categories are logical/documentation/test categories, and
the runtime raises prose `ValueError` messages rather than structured error
objects.

## Adoption Metadata Inventory

A read-only cross-class inventory report now aggregates committed metadata:

- `engine/adoption_inventory_report.py` normalizes `provider_config.requirements`, `absent_defaults.rules`, `dynamic_schema.rules`, and `sensitive_required.rules` into a single inventory.
- `python -m engine.adoption_inventory_report` emits JSON or markdown for humans/operators and supports `--class sensitive_required`.
- The report is read-only: it does not project, omit, change drift policy, alter `assert-adoptable`, render provider configuration, render placeholder values or blocks, run Terraform/OpenTofu, or enforce cross-class rules.
- It includes cross-class overlap diagnostics (warnings and info), but it is not an adoption decision engine and does not enforce cross-design rules.
- Sensitive-required rules are now integrated into the inventory as a read-only visibility lane, with warning-level overlap diagnostics against `absent_default` and `dynamic_schema` paths.
- Sensitive-required pack metadata remains pending; no sensitive-required rules have been committed to pack manifests yet.

## Absent/Default Assert-Adoptable Guidance

`assert-adoptable` now annotates blocked saved-plan paths that exactly match
committed manual-review `absent_defaults.rules` for the same provider and
resource type. The annotation is informational only: the plan remains blocked,
and no omission, normalization, projection mutation, drift tolerance,
provider-config behavior, or Terraform/OpenTofu execution is authorized.

## Dynamic-Schema Assert-Adoptable Guidance

`assert-adoptable` now annotates blocked saved-plan paths that exactly match
committed manual-review `dynamic_schema.rules` for the same provider and
resource scope. The annotation is informational only: the plan remains blocked,
and no dynamic-schema projection, omission, raw API fallback, drift tolerance,
provider-config behavior, or Terraform/OpenTofu execution is authorized.

Dynamic-schema guidance uses only value-drift and `after_unknown` plan paths. It
does not annotate sensitivity-only `before_sensitive` or `after_sensitive`
paths. The output displays each rule's `provider_version_constraint`; V1 does
not enforce that constraint because `assert-adoptable` does not currently have a
provider-version source.

## Sensitive-Required Design Checkpoint

The sensitive-required failure class is now documented in `docs/sensitive-required-remediation.md`.

- It is distinct from `provider_config`, `absent_defaults`, `dynamic_schema`, `raw_api_only_provider_blind`, `projection_omit`, and `assert-adoptable` downgrade.
- The design preserves the absolute safety invariant: never synthesize, guess, echo, persist, or project sensitive values.
- The V1 validator contract is now frozen in `docs/sensitive-required-remediation.md`: accepted keys, required fields, value-carrying field rejection, closed enums, kind/sensitivity/structural matrix with kind specificity rules, canonical path identity, deterministic provider-version strings, rule identity/conflict rules, sensitive-path static matching, provider/resource checking, cross-class deferral, error categories, and a test matrix are all specified.
- The V1 validator is implemented in `engine/sensitive_required_validator.py` and exposed through `packs.sensitive_required_rules(provider=None)` in `engine/packs.py`.
- The validator only validates metadata; it does not project, render, omit, or change any behavior.
- The Grafana illustrative example uses `one_of_block_required` to match the lab error.
- `grafana_contact_point.webhook` remains manual-review/unclassified in pack metadata; no sensitive-required pack metadata has been committed yet.

## Next Phase

- Provider-config assert-adoptable guidance annotations are implemented as
  unit-tested, fail-closed, annotation-only output. A current GCP
  attribution-label retest did not reproduce the old drift, so a current live
  provider-config blocked-path lab is still required before describing that lane
  as live-lab validated or generalizing it further.
- The next provider-config lab target should be a provider/config setting that
  reliably produces provider-driven drift on an existing blocked plan path.
- Commit sensitive-required pack metadata for a concrete provider lab finding
  once the class is narrowly defined and safe.
- Run another provider lab that proves a narrow, safe sensitive-required class
  before any behavior PR.

No projection, omission, drift tolerance, provider-config rendering, provider
config mutation, or status-changing `assert-adoptable` behavior is authorized.
Provider-config `assert-adoptable` guidance annotations are implemented as
unit-tested, fail-closed, annotation-only output; they are not currently
live-lab validated.

## Provider-Config Assert-Adoptable Guidance

Provider-config guidance behavior is documented in
`docs/provider-config-assert-guidance.md`:

- Additive guidance annotations for blocked `assert-adoptable` output when a
  blocked drift path matches a `provider_config.requirements` entry.
- Exact plan-path matching in V1, no provider rendering, no mutation, no plan
  status change.
- Required evidence before live-validation claims or generalization: a current
  provider-config lab that produces a blocked plan path matching committed
  metadata and shows the annotation while the plan remains blocked. The
  historical GCP attribution-label case no longer satisfies this by itself.
- Synthetic/unit test coverage and rollback plan are specified.

The annotation behavior is implemented, but provider-config guidance must not be
described as live-lab validated until a current provider-config lab proves it on
a real blocked matching path.

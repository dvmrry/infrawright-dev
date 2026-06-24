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
| Google Cloud | Provider-config attribution drift, billing/API boundaries, API/provider shape drift | Provider config requirements are a distinct pack concern, not drift tolerance. |

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
| Sensitive-required diagnostics | Implemented | Separate schema-required sensitive paths, validation-required paths, optional sensitive candidates, and projected sensitive paths. |
| Provider-config diagnostics | Implemented | Map saved-plan drift paths to explicit `provider_config.requirements` metadata. |
| Provider-config assert-adoptable guidance | Implemented | Annotate matching blocked plan paths while keeping plans blocked. |
| Shared schema/path helpers | Implemented | Normalize `[]` selectors, quoted map selectors, container paths, and Terraform schema status lookups. |

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
2. Propose dynamic schema remediation semantics.
3. Run a billing-enabled Google Cloud lab or a focused AWS/Azure lab.

After each behavior proposal, run at least one provider lab that originally
exposed the failure class before generalizing the behavior.

## Pack Metadata Checkpoint

Lab-derived adoption metadata is now committed in pack manifests:

| Provider | Metadata class | Evidence | Status |
|---|---|---|---|
| Google Cloud | `provider_config.requirements` | `docs/provider-labs/gcp-pr38.md` | Validated, guidance-only. |
| NetBox | `absent_defaults.rules` | `docs/provider-labs/netbox-pr22.md` | Validated, manual-review only. |
| Cloudflare | `absent_defaults.rules` + `dynamic_schema.rules` | `docs/provider-labs/cloudflare-free-tier-pr32.md` | Validated, manual-review only. |
| Grafana | unclassified | `docs/provider-labs/grafana-pr24.md` | Pending sensitive-required design. |

## Adoption Metadata Inventory

A read-only cross-class inventory report now aggregates committed metadata:

- `engine/adoption_inventory_report.py` normalizes `provider_config.requirements`, `absent_defaults.rules`, and `dynamic_schema.rules` into a single inventory.
- `scripts/adoption-inventory-report.py` emits JSON or markdown for humans/operators.
- The report is read-only: it does not project, omit, change drift policy, alter `assert-adoptable`, render provider configuration, or run Terraform/OpenTofu.
- It includes cross-class overlap diagnostics (warnings and info), but it is not an adoption decision engine and does not enforce cross-design rules.
- Sensitive-required remediation remains future work.

## Sensitive-Required Design Checkpoint

The sensitive-required failure class is now documented in `docs/sensitive-required-remediation.md`.

- It is distinct from `provider_config`, `absent_defaults`, `dynamic_schema`, `raw_api_only_provider_blind`, `projection_omit`, and `assert-adoptable` downgrade.
- The design preserves the absolute safety invariant: never synthesize, guess, echo, persist, or project sensitive values.
- V1 is design-only; no validator, no behavior, no placeholder rendering, no omission, no drift tolerance.
- `grafana_contact_point.webhook` remains manual-review/unclassified in pack metadata until the design survives review and a validator-only implementation PR is planned.

## Next Phase

- Close sensitive-required design review.
- Implement sensitive-required validator only after the design contract is accepted.
- Run another provider lab that proves a narrow, safe sensitive-required class before any behavior PR.

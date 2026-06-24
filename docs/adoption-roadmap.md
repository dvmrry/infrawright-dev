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
| Shared schema/path helpers | Implemented | Normalize `[]` selectors, quoted map selectors, container paths, and Terraform schema status lookups. |

These tools are diagnostic-only unless a future PR explicitly promotes one class
into remediation behavior.

## Known Remediation And Reporting Classes

Open classes should remain separate until lab evidence proves the smallest safe
behavior:

- Provider-config remediation or provider-config rendering from pack metadata;
  proposed in [Provider Config Remediation Design](provider-config-remediation.md),
  not implemented.
- Provider-specific absent/default normalization rules.
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
- Do not render provider config from diagnostics until the metadata and
  precedence rules are designed.
- Do not treat provider labs or static advisory reports as production
  certification.

Each remediation PR should cite provider-lab evidence, add focused fixtures, and
preserve the existing fail-loud behavior outside its narrow class.

## Recommended Next Implementation Order

1. Review the provider-config remediation design, then implement a narrow
   renderer/validator only if the design is accepted.
2. Propose absent/default normalization semantics.
3. Propose dynamic schema remediation semantics.
4. Run a billing-enabled Google Cloud lab or a focused AWS/Azure lab.

After each remediation proposal, run at least one provider lab that originally
exposed the failure class before generalizing the behavior.

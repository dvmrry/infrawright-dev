# Absent/Default Normalization Design

This is a design note, not implemented behavior. The current engine can
diagnose absent/default-shaped values, but it does not normalize them.

The problem is deliberately dangerous. Values such as `false`, `0`, `"0"`,
`""`, `null`, `[]`, and `{}` can be real configuration. A future normalization
rule must therefore be pack-owned, path-specific, provider-evidence-backed, and
blocked by default until explicit behavior exists.

## Motivating Cases

Provider labs have already exposed absent/default drift:

- NetBox showed placeholder-style drift involving empty string, zero, and
  null-like provider/API behavior.
- Cloudflare showed default and singleton drift for resources such as
  `cloudflare_zone_hold`, including fields like `hold`, `hold_after`, and
  `include_subdomains`.

The existing [Absent/Default Diagnostics](absent-default-diagnostics.md)
command classifies these shapes. That diagnostic evidence is not a
normalization rule.

## Goals

- Keep falsey values safe by default.
- Separate similar-looking failure classes before choosing behavior.
- Require provider/resource/path-specific evidence before a rule exists.
- Define a future metadata shape that can be validated before it changes
  projection.
- Preserve fail-loud adoption behavior until a future explicit projection rule
  handles one narrow class.

## Non-Goals

- Do not globally drop or rewrite `false`, `0`, `"0"`, `""`, `null`, `[]`, or
  `{}`.
- Do not infer absence from value shape alone.
- Do not apply normalization across resource types globally.
- Do not tolerate drift silently.
- Do not downgrade `assert-adoptable` based on a diagnostic candidate.
- Do not hide raw-only or provider-blind API fields.
- Do not apply absent/default normalization to sensitive values without the
  separate sensitive-required design.

## Failure Classes

Absent/default work must distinguish these cases:

- `api_absent`: the field is absent from raw API readback.
- `api_explicit_default`: the API returns a default value even when the user did
  not configure it.
- `provider_absent_placeholder`: imported provider state reports a concrete
  placeholder for an absent backend field.
- `terraform_schema_optional_default`: Terraform/provider schema optional,
  computed, or default behavior affects plan output.
- `real_configured_falsey`: the user or API intentionally configured `false`,
  `0`, `"0"`, `""`, `null`, `[]`, or `{}`.
- `provider_server_side_singleton_default`: a singleton or server-owned remote
  object exposes default fields that must not be projected unless explicitly
  owned.
- `paid_disabled_or_api_boundary_default`: the value shape is caused by a paid
  feature, disabled product, unsupported API, or tenant boundary rather than
  normalization.

These classes can look identical in a saved plan. The rule author must prove
which class applies.

## Required Evidence

A future rule must cite lab evidence showing all relevant sides of the drift:

- Raw API value or raw API absence.
- Oracle-imported provider state value.
- Projected tfvars value or projected absence.
- Saved plan drift path.
- Terraform schema status when relevant.
- Before/after plan behavior when the candidate value is omitted or preserved.
- Why the value is not a real configured falsey value.
- Cleanup and safety notes from the lab.

Evidence should be summarized in committed lab docs or sanitized fixtures. Do
not commit raw state, plans, secrets, tenant identifiers, provider logs, or
temporary roots just to prove a normalization candidate.

## Proposed Metadata Shape

This is illustrative only. It is not loaded by the engine today.

```json
{
  "absent_defaults": {
    "rules": [
      {
        "id": "netbox_site_empty_string_slug_placeholder",
        "provider": "netbox",
        "resource_type": "netbox_site",
        "path": "slug",
        "kind": "provider_absent_placeholder",
        "observed_value": "",
        "action": "omit_when_provider_placeholder",
        "evidence": "docs/provider-labs/netbox-pr22.md",
        "reason": "Provider reports empty string for absent backend slug; projecting it causes drift."
      }
    ]
  }
}
```

Required fields for a future rule should include:

- `id`: stable rule identifier.
- `provider`: provider short name.
- `resource_type` or `resource_prefix`: explicit scope.
- `path`: normalized provider/projected/plan path under review.
- `kind`: one of the failure classes above.
- `observed_value`: the placeholder/default value when applicable.
- `action`: proposed narrow action.
- `evidence`: committed lab report or sanitized fixture path.
- `reason`: human-readable justification.

## Allowed Future Actions

Future actions should be deliberately narrow:

- `diagnostic_only`: classify and explain, but do not change projection.
- `omit_when_absent_in_api`: omit only when evidence proves the API field was
  absent.
- `omit_when_provider_placeholder`: omit only when evidence proves the provider
  emitted a placeholder for an absent backend field.
- `preserve_explicit_falsey`: document that a falsey value is real
  configuration and must not be normalized.
- `manual_review_required`: block automation and require a human decision.

Avoid broad actions such as `drop_empty_values`, `drop_falsey`, or
`normalize_defaults`. They are too coarse for this failure class.

## Safety Constraints

Any future normalization behavior must:

- Preserve user-authored falsey values.
- Require explicit provider/resource/path metadata.
- Require evidence; value shape alone is never enough.
- Apply before plan as a projection transformation, not after plan as drift
  tolerance.
- Keep `assert-adoptable` blocked unless an explicit future projection rule has
  already transformed the config before the plan was created.
- Leave raw-only and provider-blind advisory paths visible.
- Defer sensitive paths to the sensitive-required design.
- Fail loudly when metadata is ambiguous, duplicated, missing evidence, or
  broader than the proven scope.

## Validator-Only First Stage

The first behavior PR, if any, should be validator-only:

- Parse and validate the metadata shape.
- Reject unsafe or unknown actions.
- Reject missing `id`, scope, `path`, `kind`, `action`, `evidence`, or
  `reason`.
- Reject rules that scope globally across providers or resource types.
- Reject rules that infer absence from value shape alone.
- Render nothing.
- Normalize nothing.
- Change no projection behavior.
- Change no drift policy behavior.
- Do not alter `assert-adoptable` status.

## Future Assert-Adoptable Guidance

Matching an absent/default candidate may eventually annotate blocked drift with
guidance. That annotation must keep the plan blocked until a future explicit
projection rule exists and has transformed the projected config before plan
generation.

Absent/default diagnostics must not become drift tolerance.

## Open Questions

- How do we prove API absence versus an explicit server default without
  committing raw API payloads?
- How should before/after evidence be represented without committing raw state,
  plans, logs, or tenant identifiers?
- Should a rule attach to the raw API path, provider-state path, projected path,
  saved-plan path, or several of them?
- How should map and list element paths be represented?
- How should set hashing and order-insensitive collections be handled?
- How should absent/default rules compose with provider-config guidance?
- How should absent/default rules compose with sensitive-required diagnostics?
- Can tenant overlays disable or replace a pack normalization rule?

## Recommended Next Step

After this design, the next work should be external review or validator-only
metadata validation. Do not implement normalization behavior until the metadata
contract and evidence requirements survive review.

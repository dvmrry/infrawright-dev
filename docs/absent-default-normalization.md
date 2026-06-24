# Absent/Default Normalization Design

This is a design note, not implemented behavior. The current engine can
diagnose absent/default-shaped values, but it does not normalize them.

The problem is deliberately dangerous. Values such as `false`, `0`, `"0"`,
`""`, `null`, `[]`, and `{}` can be real configuration. A future normalization
rule must therefore be pack-owned, path-specific, provider-evidence-backed, and
blocked by default until explicit behavior exists.

## Relationship To Existing `projection_omit`

The engine already has projection-time omission through
`DriftPolicy.projection_omit`. Future absent/default behavior must not create a
second independent omission authority.

Any future omit behavior must either:

- remain diagnostic/manual-review only, or
- feed or reuse the existing projection omission path so required-path guards
  and advisory omission accounting stay intact.

NetBox placeholder drift was already handled by `projection_omit`, not by a new
`absent_defaults.rules` omission system. A future absent/default rule may
explain why a pack-owned omission is safe, but the omission itself must preserve
the same fail-loud behavior and reporting visibility as the existing projection
path.

## Motivating Cases

Provider labs have already exposed absent/default drift:

- NetBox showed placeholder-style drift involving empty string, zero, and
  null-like provider/API behavior.
- Cloudflare showed default and singleton drift for resources such as
  `cloudflare_zone_hold`, including fields like `hold`, `hold_after`, and
  `include_subdomains`.

`cloudflare_zone_hold` is not the same class as NetBox projection-time
placeholder omission. It is better classified as
`provider_server_side_singleton_default` / plan-update drift until a later lab
proves that omitting or preserving a field yields a neutral plan and does not
lose ownership of a server-owned setting.

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
- Do not create a separate absent/default omission engine parallel to
  `projection_omit`.

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

## Runtime Discriminator Requirement

Lab evidence can prove that a value was safe to omit in one tenant/run. It does
not prove that the same-looking value is safe to omit in a different tenant/run.

Actions such as `omit_when_absent_in_api` and
`omit_when_provider_placeholder` are unsafe unless the engine can check a
concrete runtime discriminator for the current object. Examples of possible
runtime discriminators include raw API absence, provider/backend absence
evidence, or another explicit signal captured during the same adoption run.

If that runtime evidence is not available at projection time, omit actions are
invalid and must be reduced to `diagnostic_only` or `manual_review_required`.
Value shape alone is never a discriminator. A rule must not behave as "path
equals empty string, zero, false, null, empty list, or empty object, so omit."

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
        "id": "netbox_device_empty_rack_face_placeholder",
        "provider": "netbox",
        "resource_type": "netbox_device",
        "path": "rack_face",
        "kind": "provider_absent_placeholder",
        "observed_value": "",
        "action": "manual_review_required",
        "evidence": "docs/provider-labs/netbox-pr22.md",
        "reason": "Provider reported an empty string placeholder for an absent optional rack face in the NetBox lab. Any future omit must route through projection_omit and require a runtime discriminator."
      }
    ]
  }
}
```

Required fields for a future rule should include:

- `id`: stable rule identifier.
- `provider`: provider short name.
- `resource_type` or `resource_prefix`: explicit scope.
- `path`: V1 primary normalized projected/provider-state path under review.
- `kind`: one of the failure classes above.
- `observed_value`: the placeholder/default value when applicable.
- `action`: proposed narrow action.
- `evidence`: committed lab report or sanitized fixture path.
- `reason`: human-readable justification.
- `plan_path`: optional saved-plan path evidence when it differs from `path`.
- `raw_api_path`: optional raw API path evidence when it differs from `path`.
- `provider_state_path`: optional provider-state path evidence when it differs
  from `path`.

V1 validation should treat `path` as the projected/provider-state path because
that is the namespace used by projection and advisory omission accounting. Raw
API paths and saved-plan paths are evidence fields, not the primary V1 rule key.
Multi-namespace matching is future design.

## Allowed Future Actions

Future actions should be deliberately narrow:

- `diagnostic_only`: classify and explain, but do not change projection.
- `omit_when_absent_in_api`: omit only when runtime evidence proves the API
  field is absent for the current object.
- `omit_when_provider_placeholder`: omit only when runtime evidence proves the
  provider emitted a placeholder for an absent backend field for the current
  object.
- `preserve_explicit_falsey`: document that a falsey value is real
  configuration and must not be normalized.
- `manual_review_required`: block automation and require a human decision.

Avoid broad actions such as `drop_empty_values`, `drop_falsey`, or
`normalize_defaults`. They are too coarse for this failure class.

## Kind/Action Legality

Validator-only work must reject out-of-matrix kind/action pairings.

| Kind | Allowed actions |
|---|---|
| `api_absent` | `diagnostic_only`, `manual_review_required`; `omit_when_absent_in_api` only if runtime API absence is checkable. |
| `provider_absent_placeholder` | `diagnostic_only`, `manual_review_required`; `omit_when_provider_placeholder` only if runtime provider/backend absence is checkable and the omission routes through `projection_omit`. |
| `real_configured_falsey` | `preserve_explicit_falsey`, `diagnostic_only`, `manual_review_required` only. |
| `paid_disabled_or_api_boundary_default` | `diagnostic_only`, `manual_review_required` only. |
| `provider_server_side_singleton_default` | `diagnostic_only`, `manual_review_required` unless a later lab proves a narrow projection-time transform. |
| `api_explicit_default` | `diagnostic_only`, `manual_review_required` by default. |
| `terraform_schema_optional_default` | `diagnostic_only`, `manual_review_required` by default. |

This matrix is intentionally conservative. It prevents a rule from treating a
real configured falsey value or server-owned singleton default as an omission
candidate merely because the value shape looks empty.

## Safety Constraints

Any future normalization behavior must:

- Preserve user-authored falsey values.
- Require explicit provider/resource/path metadata.
- Require evidence; value shape alone is never enough.
- Apply before plan as a projection transformation, not after plan as drift
  tolerance.
- Reuse or feed the existing projection omission path for any future omit
  behavior rather than creating a second omit engine.
- Keep `assert-adoptable` blocked unless an explicit future projection rule has
  already transformed the config before the plan was created.
- Leave raw-only and provider-blind advisory paths visible.
- Never apply omit behavior to sensitive or sensitive-overlapping paths.
- Fail loudly on sensitive overlap rather than omitting.
- Keep every omission visible in advisory accounting, reusing or extending
  `omitted_by_policy` or equivalent.
- Fail loudly when metadata is ambiguous, duplicated, missing evidence, or
  broader than the proven scope.

## Validator-Only First Stage

The first behavior PR, if any, should be validator-only:

- Parse and validate the metadata shape.
- Reject unknown `kind`.
- Reject unknown `action`.
- Reject out-of-matrix kind/action pairings.
- Reject omit actions that lack a concrete runtime discriminator.
- Reject missing `id`, scope, `path`, `kind`, `action`, `evidence`, or
  `reason`.
- Reject missing `observed_value` for actions or kinds that require matching a
  concrete placeholder/default value.
- Reject provider/resource-type mismatch.
- Reject ambiguous path namespace.
- Reject sensitive path targets when statically known.
- Reject duplicate or conflicting rules.
- Reject rules that scope globally across providers or resource types.
- Reject rules that infer absence from value shape alone.
- Reject any broad `drop_empty_values`, `drop_falsey`, or
  `normalize_defaults` action.
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
- How should future multi-namespace matching relate raw API paths,
  provider-state paths, projected paths, and saved-plan paths?
- How should map and list element paths be represented?
- How should set hashing and order-insensitive collections be handled?
- How should absent/default rules compose with provider-config guidance?
- How should absent/default rules compose with sensitive-required diagnostics?
- Can tenant overlays disable or replace a pack normalization rule?

## Recommended Next Step

After this patched design, run external review again. Do not proceed to
validator-only metadata validation until the relationship to `projection_omit`,
runtime discriminator requirement, kind/action matrix, and V1 path namespace
survive review.

Do not implement normalization behavior until the metadata contract and evidence
requirements survive validator-only review and at least one provider lab proves
the narrow class being promoted.

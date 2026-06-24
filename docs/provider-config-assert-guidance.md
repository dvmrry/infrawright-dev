# Provider-Config Assert-Adoptable Guidance Annotations

This is a design-only document. It specifies the exact future behavior for
additive provider-config guidance annotations in blocked `assert-adoptable`
output. No behavior is implemented by this PR.

The adoption metadata framework is now intentionally boxed: validators,
guidance, and reporting exist for `provider_config.requirements`,
`absent_defaults.rules`, `dynamic_schema.rules`, and `sensitive_required.rules`,
but committed pack metadata must not authorize projection, omission, drift
tolerance, provider rendering, assert-adoptable downgrade, secret handling, or
placeholder rendering. This design is the first behavior candidate for a narrow,
additive annotation only.

## Problem Statement

Some provider-level settings affect planned resource drift after import. The
adoption oracle detects blocked drift paths in the saved plan. Pack metadata can
declare provider-level settings known from lab evidence. When a blocked drift
path matches a declared provider-config requirement, `assert-adoptable` should be
able to annotate the blocked output with operator guidance. The plan remains
blocked.

Concrete example from the Google Cloud lab
(`docs/provider-labs/gcp-pr38.md`):

- Provider setting: `add_terraform_attribution_label = false`
- Blocked drift path: `terraform_labels.goog-terraform-provisioned`
- Evidence: `docs/provider-labs/gcp-pr38.md`

Without the annotation, the operator sees a blocked plan with a provider-created
label and no guidance. With the annotation, the operator sees the same blocked
plan plus an informational note explaining the provider setting that removes the
drift.

## Safety Invariant

Provider-config guidance annotations must never:

- render provider HCL
- mutate provider configuration
- alter env-root files
- alter oracle scratch files
- alter generated tfvars
- alter import blocks
- alter projected config
- change drift policy
- suppress drift
- tolerate drift
- downgrade `assert-adoptable` results
- infer provider settings without metadata
- execute Terraform/OpenTofu
- apply settings automatically

The annotation is informational only. A blocked plan remains blocked; a dirty
plan remains dirty.

## Allowed Future Behavior

When `assert-adoptable` has already identified blocked drift and one or more
blocked plan paths match a `provider_config.requirements` entry's `plan_paths`,
the output may include an annotation containing:

- `provider`: the provider short name
- `setting`: the provider argument name
- `expected_value`: the value from the requirement when present
- `mode`: `required_external` or `renderable_default`
- `matched_plan_path`: the blocked plan path that triggered the match
- `reason`: human-readable explanation from the requirement
- `evidence`: committed lab report or sanitized fixture path
- `status_effect`: a fixed statement that the plan remains blocked

Allowed modes for annotation:

- `required_external`
- `renderable_default`

`renderable_default` is still guidance-only in this design. No provider block
rendering is authorized.

## Matching Semantics

Matching is intentionally conservative in V1:

- Match only against explicit `plan_paths` in committed provider-config metadata.
- Match only blocked drift paths already reported by `assert-adoptable`.
- Use exact path match in V1.
- No fuzzy matching.
- No wildcard matching.
- No inferred provider settings.
- No matching from raw API.
- No matching from provider-state paths unless the plan path is explicitly
  present.
- If multiple requirements match, list all annotations deterministically, sorted
  by provider, setting, and matched plan path.
- If no requirement matches, output is unchanged.

Provider match:

- A requirement only matches resources/plans for the same provider.
- No cross-provider matching.
- If the blocked path is provider-ambiguous, do not annotate.

A match occurs when:

1. `assert-adoptable` reports a blocked update path.
2. The path is in the requirement's `plan_paths`.
3. The provider of the blocked resource matches the requirement's `provider`.

If a requirement has `resource_types` or `resource_prefixes`, the resource must
also satisfy one of those scopes. If neither is present, the requirement applies
to every resource for that provider.

## Output Shape

The output is additive. There is no existing formal `assert-adoptable` output
schema in this design, so the following shapes are illustrative and must be
aligned with whatever schema the implementation PR uses.

### Human-readable text example

```text
Provider configuration guidance:
- provider: google
  setting: add_terraform_attribution_label
  expected value: false
  mode: required_external
  matched plan path: terraform_labels.goog-terraform-provisioned
  reason: Google provider injects attribution labels unless disabled.
  evidence: docs/provider-labs/gcp-pr38.md
  status: informational only; plan remains blocked
```

### JSON-ish example

```json
{
  "provider_config_guidance": [
    {
      "provider": "google",
      "setting": "add_terraform_attribution_label",
      "expected_value": false,
      "mode": "required_external",
      "matched_plan_path": "terraform_labels.goog-terraform-provisioned",
      "reason": "Google provider injects attribution labels unless disabled.",
      "evidence": "docs/provider-labs/gcp-pr38.md",
      "status_effect": "none_plan_remains_blocked"
    }
  ]
}
```

`status_effect` must always be `none_plan_remains_blocked` in V1. The
implementation may choose a different field name or shape, but the semantics must
remain unchanged.

## Fail-Closed Behavior

The implementation must be fail-closed:

- If metadata loading fails, do not annotate; preserve existing blocked output.
- If a requirement is invalid, the validator should fail where metadata is loaded
  today; do not use invalid metadata.
- If guidance matching errors, preserve existing blocked output and emit no
  annotation.
- Annotation failure must not affect plan status.
- Annotation absence must not alter blocked status.
- Annotation presence must not alter blocked status.

In all failure cases, `assert-adoptable` behaves exactly as it does today.

## Evidence Requirements

Before the first implementation PR merges, the following evidence must exist:

- The existing GCP lab evidence in `docs/provider-labs/gcp-pr38.md` remains the
  basis for the attribution-label annotation.
- A GCP lab re-run showing the annotation appears for the known attribution-label
  drift when the behavior is implemented.

Before expanding beyond this narrow provider-config guidance class, a second
provider-config lab is required, preferably AWS or Azure. This design does not
require the second lab before being accepted; it only requires the second lab
before the behavior is generalized.

## Required Tests For Future Implementation

The future implementation PR must include tests covering:

- No matching provider-config metadata -> existing blocked output unchanged.
- Matching `required_external` requirement -> guidance annotation appears.
- Matching `renderable_default` requirement -> guidance annotation appears, but
  no provider rendering.
- Plan remains blocked when annotation appears.
- Plan remains blocked when annotation does not appear.
- Multiple matching requirements produce deterministic annotations.
- Non-matching plan paths do not annotate.
- Matching path from wrong provider does not annotate.
- Provider-ambiguous drift does not annotate.
- Invalid metadata does not produce annotation.
- Annotation code does not modify generated files.
- Annotation code does not execute Terraform/OpenTofu.
- Annotation code does not render provider config.
- Existing `assert-adoptable` tests remain unchanged except for additive
  annotation checks.

These tests are not added by this design PR.

## Explicit Non-Goals

This design does not authorize:

- provider config rendering
- provider config mutation
- `.tf` or `.tf.json` generation
- env-root modification
- oracle scratch modification
- plan-status downgrade
- drift tolerance
- omission
- projection mutation
- dynamic-schema behavior
- absent/default behavior
- sensitive-required behavior
- raw API inference
- automatic remediation

## Rollback Plan

The behavior is additive output only. Reverting the annotation code removes the
guidance text but preserves existing blocked behavior. No state, metadata,
provider config, or generated config migration is needed.

## Relationship To Sibling Docs

- [Provider Config Requirement Guidance](provider-config-remediation.md) defines
  the validator metadata contract and guidance modes.
- [Provider Config Diagnostics](provider-config-diagnostics.md) defines the
  static diagnostic classification that this design extends into
  `assert-adoptable` output.
- `docs/provider-labs/gcp-pr38.md` contains the lab evidence that justifies the
  first annotation.

## Out Of Scope For This PR

This PR does not implement:

- The annotation code.
- Any change to `assert-adoptable` output.
- Any change to `engine.provider_config`.
- Any change to the provider-config validator.
- Any pack metadata change.
- Any test addition.
- Any projection, omission, drift tolerance, or rendering behavior.

## Recommended Next Step

After this design PR is accepted, the next PR is a narrow implementation of the
annotation behavior against the existing Google Cloud provider-config metadata.
That implementation PR must remain additive, fail-closed, and annotation-only.

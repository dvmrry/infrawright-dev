# Sensitive-Required Remediation Design

This is a design note, not implemented behavior. The current engine already fails
loud on sensitive provider-state paths that cannot be projected safely. This
document defines the class of values and blocks that are both sensitive and
structurally required for Terraform/OpenTofu to produce a clean plan, and it
proposes a future metadata contract for pack-owned guidance.

The safety invariant is absolute: the system must never synthesize, guess, echo,
persist, or project sensitive values. Existing fail-loud sensitive handling
remains the baseline until a later design proves a narrow, safe behavior.

## The Failure Class

Sensitive-required means a provider schema marks a value or block as sensitive,
but the provider also requires enough structural configuration for
Terraform/OpenTofu to plan cleanly. The oracle/adoption path cannot safely
project the real sensitive value, but omitting the whole structure may cause:

- invalid Terraform configuration,
- provider validation errors,
- persistent plan drift, or
- a blocked `assert-adoptable` result that cannot be explained by another class.

The motivating example is `grafana_contact_point` from PR24. The provider marks
the entire `webhook` block sensitive, yet Terraform validation requires one
notifier block to be present. Omitting the block from projected tfvars produces a
structurally invalid configuration, while projecting the block would require
writing sensitive provider-state values into generated files.

## Distinct From Other Classes

Sensitive-required is not:

- `provider_config`: provider-level auth or settings are provider-config
  guidance, not resource-path sensitivity.
- `absent_defaults`: falsey/default/placeholder semantics are absent/default.
  Sensitive-required is not about empty strings, zeros, or server-owned defaults.
- `dynamic_schema`: schema-unknown or provider-state paths are dynamic-schema.
  Sensitive-required is about known sensitivity plus structural requirement.
  If a path is both schema-unknown and sensitive-required, classify it as
  sensitive-required only after evidence proves the sensitivity/structure issue
  dominates; otherwise leave it unclassified.
- `raw_api_only_provider_blind`: raw-only provider-blind paths are advisory-only
  classification. They are not sensitive and do not block projection of the
  writeable provider-state surface.
- `projection_omit`: sensitive-required must not create a second omission
  authority. Omission of sensitive structures is unsafe unless a later design
  proves it and routes through existing projection/omit safety mechanisms.
- `assert-adoptable` downgrade: matching a sensitive-required rule is guidance,
  not tolerance. It must not downgrade a blocked plan to clean or tolerated.

## Safety Invariant

The system must never:

- synthesize a secret,
- guess a sensitive value,
- echo a sensitive value from provider state into projected tfvars,
- persist a sensitive value in generated config or fixtures,
- project a sensitive value because a rule says it is "required",
- render a fake secret,
- downgrade `assert-adoptable` for a sensitive-related block,
- silently omit a sensitive structure to make a plan look clean.

Existing fail-loud sensitive behavior remains the baseline. Any future change
must preserve this invariant and prove it per provider, per resource, per path.

## V1 Position

V1 is design-only and diagnostic-only. No behavior changes are allowed. No
placeholder rendering is allowed. No sensitive value projection is allowed. No
auto-omit is allowed. No `assert-adoptable` downgrade is allowed.

The V1 design only defines the failure class, the metadata shape, the evidence
requirements, and the boundaries with sibling systems. A future PR may add a
validator. A later future PR may add behavior only after the validator and
evidence contract survive review and a provider lab proves a narrow, safe class.

## Proposed Future Metadata Shape

This is illustrative only. It is not loaded by the engine today.

```json
{
  "sensitive_required": {
    "rules": [
      {
        "id": "grafana_contact_point_webhook_required_sensitive",
        "provider": "grafana",
        "provider_version_constraint": ">= 3.0.0",
        "resource_type": "grafana_contact_point",
        "path": "webhook",
        "kind": "sensitive_required_block",
        "sensitivity": "contains_sensitive_fields",
        "structural_requirement": "block_required_for_valid_config",
        "action": "manual_review_required",
        "evidence": "docs/provider-labs/grafana-pr24.md",
        "reason": "The provider requires a webhook block shape for contact-point adoption, but sensitive fields cannot be projected from state."
      }
    ]
  }
}
```

`path` is the provider-state path under review. `sensitivity` and
`structural_requirement` describe the two sides of the failure class. `action` is
guidance-only in V1. No behavior is authorized by this shape.

## Possible Future Kinds

Use conservative, descriptive names:

- `sensitive_required_block`: a nested block is sensitive and structurally
  required (e.g. `grafana_contact_point.webhook`).
- `sensitive_required_attribute`: a scalar attribute is sensitive and required
  by the schema.
- `sensitive_write_only_attribute`: a write-only attribute that the provider never
  returns in state, but the configuration requires it to be supplied.
- `sensitive_nested_secret`: a secret nested inside a larger block where the rest
  of the block is not sensitive.
- `sensitive_structural_placeholder_required`: a block or attribute must be
  present in configuration but the actual sensitive value must be supplied by the
  operator after adoption.

Keep the set small. Do not add a kind until a provider lab proves the case.

## Possible Future Actions

V1 allowed actions are guidance-only:

- `diagnostic_only`: classify and explain, but do not change projection or
  assert-adoptable.
- `manual_review_required`: block automation and require a human decision.

The following actions are reserved for a future design only and are invalid in
V1:

- `render_placeholder_block`: reserved for a future design that proves a safe,
  non-secret, structural placeholder block shape.
- `render_placeholder_attribute`: reserved for a future design that proves a safe,
  non-secret placeholder attribute.
- `preserve_structure_without_secret`: reserved for a future design that keeps
  the required block structure while redacting or omitting only the secret
  leaves.
- `operator_supplied_value_required`: reserved for a future design that marks
  the field as explicitly requiring operator-supplied input after adoption.

## Forbidden Actions

Reject broad/unsafe behavior explicitly:

- `project_sensitive`
- `copy_sensitive_from_state`
- `guess_secret`
- `suppress_sensitive_drift`
- `omit_sensitive_block`
- `accept_sensitive_unknown`
- `downgrade_assert_adoptable`
- `render_fake_secret`

These are never valid in V1 and should remain forbidden in any future design
unless a very narrow, provider-proven alternative is reviewed separately.

## Required Evidence

A future rule must cite evidence showing both sides of the failure class:

- Provider version constraint.
- Resource type.
- Sensitive provider-state path.
- Terraform schema sensitivity marker.
- Whether the path is an attribute or a block.
- Whether the parent block is structurally required.
- Saved-plan behavior when the structure is omitted.
- Saved-plan behavior when the structure is present without the secret, if safely
  testable.
- Whether the value is user-supplied, provider-computed, write-only, or unknown.
- Why no existing class (`provider_config`, `absent_defaults`, `dynamic_schema`,
  `raw_api_only_provider_blind`) applies.
- Cleanup and safety notes from the lab.

Evidence must not include raw secrets, state files with secrets, provider logs
with secrets, tenant identifiers, raw plans with secrets, or temporary roots.
Summarize findings in the lab report and reference the lab report path as
evidence.

## Relationship To Sibling Systems

### `provider_config`

Provider-level authentication or settings (e.g. API tokens, endpoints,
regions) remain provider-config guidance, not sensitive-required rules.
Sensitive-required rules target resource paths.

### `absent_defaults`

Falsey/default/placeholder semantics belong to absent/default. A sensitive field
that happens to be empty is not automatically an absent/default candidate. The
structural requirement and sensitivity must be proven first.

### `dynamic_schema`

Schema-unknown paths belong to dynamic-schema. A path that is both sensitive and
schema-unknown should be classified as sensitive-required only when the
sensitivity/structure issue is the dominant failure class. Otherwise leave it
unclassified or in dynamic-schema.

### `projection_omit`

Sensitive-required must not create a second omission authority. Any future
omission of a sensitive structure must route through the existing
`DriftPolicy.projection_omit` path with the same fail-loud behavior and advisory
visibility. In V1, omitting a sensitive-required structure is rejected.

### `advisory_report`

Sensitive-required may eventually appear in advisory/reporting output, but V1
design does not change advisory behavior. The existing advisory report already
flags sensitive provider-observed paths that were omitted from projection.

### `assert-adoptable`

Sensitive-required must not downgrade a blocked plan to clean or tolerated in
V1. A future design may add guidance annotations while keeping the plan blocked.

## Validator-Only Future Path

A later validator-only PR, if any, should:

- Parse and validate the metadata shape.
- Reject unknown keys.
- Reject unknown `kind`.
- Reject unknown `sensitivity` values.
- Reject unknown `structural_requirement` values.
- Reject unsafe/reserved actions.
- Require `evidence` and `reason`.
- Require `provider_version_constraint` as a non-empty string.
- Require exactly one resource scope: `resource_type` or `resource_prefix`.
- Check provider/resource consistency through `provider_prefixes`.
- Reject cross-class duplicates only after a cross-design identity rule exists.
- Render nothing.
- Project nothing.
- Omit nothing.
- Change no projection behavior.
- Change no drift policy behavior.
- Do not alter `assert-adoptable` status.

## What This Is Not

This design is not:

- A secret manager integration.
- A credential capture system.
- HCL generation.
- Placeholder rendering.
- Drift tolerance.
- An `assert-adoptable` downgrade.
- Remediation execution.
- A second `projection_omit` path.
- A dynamic-schema extension.

## Recommended Next Step

External review of this design, followed by correction PRs if needed. Only after
the design survives review should a validator-only implementation PR be
planned. No sensitive-required behavior should be implemented until the metadata
contract, evidence requirements, and safety invariant are accepted and at least
one provider lab proves a narrow, safe class.

Until then, `grafana_contact_point.webhook` remains manual-review/unclassified in
pack metadata.

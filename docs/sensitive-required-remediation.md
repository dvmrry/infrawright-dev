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
        "structural_requirement": "one_of_block_required",
        "action": "manual_review_required",
        "evidence": "docs/provider-labs/grafana-pr24.md",
        "reason": "One of the contact-point notifier blocks must be present for valid config. The webhook block is the lab-proven sensitive-required example: the provider marks it sensitive, but omitting it leaves the configuration structurally invalid. Sensitive fields cannot be projected from state."
      }
    ]
  }
}
```

`path` is the provider-state path under review. `sensitivity` and
`structural_requirement` describe the two sides of the failure class. `action` is
guidance-only in V1. No behavior is authorized by this shape.

## Accepted Keys

The future V1 accepted key set for `sensitive_required.rules` is:

- `id`
- `provider`
- `provider_version_constraint`
- `resource_type`
- `resource_prefix`
- `path`
- `kind`
- `sensitivity`
- `structural_requirement`
- `action`
- `evidence`
- `reason`
- `raw_api_path`
- `projected_path`
- `plan_path`

A future validator rejects any unknown key. `raw_api_path`, `projected_path`,
and `plan_path` are evidence-only fields and cannot replace `path`. They may be
used to show the mapping between raw API, projected, and plan namespaces, but
identity is always based on the provider-state `path`.

The future pack metadata key is `sensitive_required.rules`. A future accessor
would be `packs.sensitive_required_rules(provider=None)`. No accessor is
implemented in this PR.

## Required V1 Fields

A future validator must require:

- `id`
- `provider`
- `provider_version_constraint`
- exactly one resource scope: `resource_type` or `resource_prefix`
- `path`
- `kind`
- `sensitivity`
- `structural_requirement`
- `action`
- `evidence`
- `reason`

## Forbid Value-Carrying Fields

Sensitive-required metadata must never contain sensitive values or placeholder
values. A future validator rejects the following value-carrying keys with a
distinct `forbidden_value_carrying_key` error:

- `value`
- `observed_value`
- `placeholder_value`
- `secret`
- `secret_value`
- `sensitive_value`

Any other unaccepted key is rejected as `unknown_key`. Reviewers should still
reject attempts to smuggle sensitive values through accepted text fields such as
`reason` or `evidence`.

Sensitive values must never be copied from provider state, raw API responses,
saved plans, logs, fixtures, or generated config. This rule is absolute and is
not relaxed by any future V1 action.

## Possible Future Kinds

The V1 `kind` enum is closed and conservative:

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

Keep the set small. A future validator rejects any unknown `kind`. Do not add a
kind until a provider lab proves the case.

## Possible Future Sensitivity

The V1 `sensitivity` enum is closed:

- `sensitive_attribute`: the Terraform schema marks the attribute sensitive.
- `sensitive_block`: the Terraform schema or provider state treats the whole block
  as sensitive.
- `contains_sensitive_fields`: the block is not necessarily wholly sensitive, but
  one or more children are sensitive.
- `write_only_sensitive`: the value is required in config but not recoverable from
  provider state.

A future validator rejects any unknown `sensitivity` value.

## Possible Future Structural Requirement

The V1 `structural_requirement` enum is closed:

- `block_required_for_valid_config`: the block shape must be present for valid
  provider/Terraform config.
- `attribute_required_for_valid_config`: the attribute must be present for valid
  config.
- `one_of_block_required`: one block among a provider-defined set must be present.
- `parent_block_required`: the parent structure is required even when sensitive
  leaves cannot be projected.
- `operator_input_required_for_valid_config`: valid config requires an
  operator-supplied value that cannot be recovered safely from state.

A future validator rejects any unknown `structural_requirement` value.

## Kind / Sensitivity / Structural Requirement Matrix

All V1 kinds allow only `diagnostic_only` and `manual_review_required`. No kind
allows projection, omission, rendering, drift tolerance, or `assert-adoptable`
downgrade.

| Kind | Allowed sensitivity | Allowed structural_requirement |
|---|---|---|
| `sensitive_required_block` | `sensitive_block`, `contains_sensitive_fields` | `block_required_for_valid_config`, `one_of_block_required`, `parent_block_required` |
| `sensitive_required_attribute` | `sensitive_attribute` | `attribute_required_for_valid_config` |
| `sensitive_write_only_attribute` | `write_only_sensitive` | `attribute_required_for_valid_config`, `operator_input_required_for_valid_config` |
| `sensitive_nested_secret` | `contains_sensitive_fields` | `parent_block_required`, `block_required_for_valid_config` |
| `sensitive_structural_placeholder_required` | `sensitive_block`, `contains_sensitive_fields`, `write_only_sensitive` | `block_required_for_valid_config`, `parent_block_required`, `operator_input_required_for_valid_config` |

A future validator rejects any out-of-matrix combination.

### Kind specificity

If more than one kind permits the same `sensitivity` + `structural_requirement`
pair, rule authors must choose the most specific kind proven by lab evidence.
`sensitive_structural_placeholder_required` is a fallback classification and is
valid only when no more specific kind fits the evidence. A future validator does
not infer kind precedence from the matrix alone; it only validates that the chosen
kind is in-matrix. Specificity is enforced during lab/design review unless a
future contract adds deterministic precedence. Do not use multiple rules with
different kinds for the same provider/version/scope/path; rule identity conflict
handling rejects that.

### Provisional enum scope

The enum values beyond the Grafana-proven `sensitive_required_block` /
`one_of_block_required` case are reserved classifications for the validator
contract, not behavior permissions. A rule using any kind or structural
requirement must cite lab evidence proving that exact class. The validator can
only check shape and matrix membership; reviewers must reject rules whose
evidence does not prove the chosen class. No behavior is authorized by the
presence of a kind.

## Possible Future Actions

V1 allowed actions are guidance-only:

- `diagnostic_only`: classify and explain, but do not change projection or
  assert-adoptable.
- `manual_review_required`: block automation and require a human decision.

The following actions are reserved for a future design only and are rejected in
V1 with a distinct "rejected in V1" error:

- `render_placeholder_block`: reserved for a future design that proves a safe,
  non-secret, structural placeholder block shape.
- `render_placeholder_attribute`: reserved for a future design that proves a safe,
  non-secret placeholder attribute.
- `preserve_structure_without_secret_candidate`: reserved for a future design
  that keeps the required block structure while redacting or omitting only the
  secret leaves.
- `operator_input_required_candidate`: reserved for a future design that marks
  a field as explicitly requiring operator-supplied input after adoption.

Operator input is modeled as the structural requirement
`operator_input_required_for_valid_config`, not as a V1 action. The action
`operator_input_required_candidate` is only a reserved placeholder for future
behavior discussion.

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

## Path Namespace

V1 `path` is the provider-state path. It uses the engine normalized path syntax.
A future validator canonicalizes `path` using `schema_paths.parse_report_path` then
`schema_paths.format_path`. Accepted `[0]` and `[*]` normalize to `[]` through
`schema_paths.parse_report_path` / `format_path`. Bare wildcard path segments and
unsupported syntax are rejected.

Rule identity uses the canonicalized provider-state path. V1 adds no new path
syntax. `raw_api_path`, `projected_path`, and `plan_path` are evidence-only and
are not identity keys unless a future contract says so.

## Provider Version Determinism

`provider_version_constraint` is required. V1 validates it only as a non-empty
string after `str.strip()`. V1 does not parse or semantically evaluate version
constraints. Identity comparison uses exact stripped string equality.
Semantically equivalent but string-different ranges are distinct in V1. Version
parsing and evaluation are future engine-integrated behavior.

## Rule Identity And Conflicts

Rule identity is:

```
provider + stripped provider_version_constraint + resource scope + canonical path
```

Resource scope is either `("type", resource_type)` or `("prefix", resource_prefix)`.

A future validator must reject:

- duplicate identical identity,
- same identity with different `kind`,
- same identity with different `sensitivity`,
- same identity with different `structural_requirement`,
- same identity with different `action`,
- same identity with different `evidence`,
- same identity with different evidence-only path fields if those fields are accepted.

`reason` is required but is not part of the conflict-field set in V1. Same
identity with only `reason` differing is still rejected as a duplicate or
same-identity ambiguity, not accepted as a merge. A future contract PR must
specify the exact error category.

Overlapping scope: reject exact `resource_type` and matching `resource_prefix`
overlap when provider, stripped version string, and canonical path are equal. Do
not reject overlap when version strings differ. Do not semantically compare
version ranges. No merge or precedence rule exists in V1.

## Action Rejection

A future validator splits actions into four categories and rejects everything
outside the allowed set:

- **Allowed V1 actions** (`diagnostic_only`, `manual_review_required`) are the
  only valid actions.
- **Reserved actions** are rejected in V1 with a distinct `rejected_in_v1_action`
  error:
  - `render_placeholder_block`
  - `render_placeholder_attribute`
  - `preserve_structure_without_secret_candidate`
  - `operator_input_required_candidate`
- **Forbidden actions** are rejected with a `forbidden_action` error:
  - `project_sensitive`
  - `copy_sensitive_from_state`
  - `guess_secret`
  - `suppress_sensitive_drift`
  - `omit_sensitive_block`
  - `accept_sensitive_unknown`
  - `downgrade_assert_adoptable`
  - `render_fake_secret`
- **Unknown actions** are rejected with an `unknown_action` error.

All reserved, forbidden, and unknown actions are invalid in V1.

## Sensitive Path Handling

If a static `sensitive_paths` set is supplied, a future validator canonicalizes
each entry with `schema_paths.parse_report_path` then `schema_paths.format_path`.
The rule `path` is canonicalized with the same process. The validator rejects the
rule if the canonicalized rule path is not present in the canonicalized sensitive
path set. If no static set is supplied, the validator skips this check. Exact
canonical match only. Ancestor/descendant matching is future engine-integrated
behavior. Do not invent a new sensitivity resolver.

## Cross-Class Overlap Deferral

Cross-class overlap with `dynamic_schema`, `absent_defaults`, or `provider_config`
is not machine-enforced in V1. It is deferred until a cross-design identity rule
exists. Human reviewers should avoid filing the same provider/resource/path
under multiple classes unless the boundary is explicit.

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

The evidence checklist is enforced during lab/design review, not by the static
validator. A future validator only checks that `evidence` is a non-empty string
and that `reason` is present; it does not semantically validate the checklist
unless future structured evidence fields are added.

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

After this correction, run external review. If the design survives review, the
next PR is a V1 validator-contract PR that specifies message text, error
categories, and the exact rejection behavior. Do not implement the validator or
any behavior in this correction PR.

No sensitive-required behavior should be implemented until the metadata contract,
evidence requirements, and safety invariant are accepted and at least one
provider lab proves a narrow, safe class.

Until the validator contract and validator-only implementation exist,
`grafana_contact_point.webhook` remains manual-review/unclassified in pack
metadata.

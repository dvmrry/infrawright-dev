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
- AWS showed empty-string placeholder paths in
  [AWS Absent/Default Placeholder Classification](aws-absent-default-classification.md).
  Its `name_prefix` and `bucket_prefix` findings are mutually-exclusive field
  conflict candidates, not the same shape as NetBox-style empty enum/default
  placeholders. `aws_cloudwatch_log_group.kms_key_id = ""` is an absent optional
  reference candidate with a different safety argument again.

`cloudflare_zone_hold` is not the same class as NetBox projection-time
placeholder omission. It is better classified as
`provider_server_side_singleton_default` / plan-update drift until a later lab
proves that omitting or preserving a field yields a neutral plan and does not
lose ownership of a server-owned setting.

The [Absent/Default Diagnostics](absent-default-diagnostics.md) classifications
describe these shapes. They are not a normalization rule or a standalone
command.

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

### V1 Omit Actions Are Invalid

No structured runtime discriminator mechanism exists yet. There is no
`runtime_discriminator` field in the metadata shape, and no engine path that
checks per-object absence at projection time. Because the V1 validator cannot
confirm that an omit action has a checkable runtime discriminator, it must not
accept one.

Therefore, in V1 validator-only metadata:

- `omit_when_absent_in_api` is always rejected.
- `omit_when_provider_placeholder` is always rejected.

These two actions remain documented only as future reserved actions. They become
valid candidates only after a later design adds a checkable
`runtime_discriminator` field and routes the resulting omission through
`projection_omit`. Until then, omit actions are invalid regardless of evidence,
which resolves the earlier ambiguity around "only if runtime absence is
checkable": V1 never treats runtime absence as checkable.

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
- `preserve_explicit_falsey`: document that a falsey value is real
  configuration and must not be normalized.
- `manual_review_required`: block automation and require a human decision.

The following two actions are **reserved for a future design only** and are
invalid in V1 (see "V1 Omit Actions Are Invalid" above):

- `omit_when_absent_in_api`: reserved. Would omit only when a checkable runtime
  discriminator proves the API field is absent for the current object.
- `omit_when_provider_placeholder`: reserved. Would omit only when a checkable
  runtime discriminator proves the provider emitted a placeholder for an absent
  backend field for the current object, and the omission routes through
  `projection_omit`.

Avoid broad actions such as `drop_empty_values`, `drop_falsey`, or
`normalize_defaults`. They are too coarse for this failure class.

## Kind/Action Legality

Validator-only work must reject out-of-matrix kind/action pairings. Because V1
omit actions are always invalid, the V1 matrix contains no omit actions at all.

| Kind | Allowed V1 actions |
|---|---|
| `api_absent` | `diagnostic_only`, `manual_review_required` |
| `provider_absent_placeholder` | `diagnostic_only`, `manual_review_required` |
| `real_configured_falsey` | `preserve_explicit_falsey`, `diagnostic_only`, optionally `manual_review_required` |
| `paid_disabled_or_api_boundary_default` | `diagnostic_only`, `manual_review_required` |
| `provider_server_side_singleton_default` | `diagnostic_only`, `manual_review_required` |
| `api_explicit_default` | `diagnostic_only`, `manual_review_required` |
| `terraform_schema_optional_default` | `diagnostic_only`, `manual_review_required` |

`omit_when_absent_in_api` and `omit_when_provider_placeholder` are reserved for a
later design with runtime discriminator support. They are not allowed for any
kind in V1, so the V1 validator rejects them regardless of the declared kind.

This matrix is intentionally conservative. It prevents a rule from treating a
real configured falsey value or server-owned singleton default as an omission
candidate merely because the value shape looks empty.

## Rule Identity And Conflicts

A V1 validator needs a deterministic identity so it can reject duplicate and
conflicting rules. Rule identity is:

```text
provider + resource scope + path
```

- Resource scope is either an exact `resource_type` or a `resource_prefix`.
- Identical identity with identical `kind` and `action` (and `observed_value`,
  when present) is a **duplicate** and is rejected.
- Identical identity with a different `kind`, `action`, or `observed_value` is a
  **conflict** and is rejected.
- An exact `resource_type` and a `resource_prefix` that match the same provider
  and `path` are treated as **overlapping scope** and are rejected unless a
  future precedence rule exists.
- There is no merge rule in V1. The validator never combines two rules; it
  rejects the ambiguity instead.

## Observed Value Requirements And Matching

`observed_value` records the concrete placeholder/default value a rule depends
on. Its presence is required or optional depending on the claim:

- Required for `provider_absent_placeholder`, `api_explicit_default`, and
  `terraform_schema_optional_default` when the claim depends on a concrete
  observed placeholder/default value.
- Required for `preserve_explicit_falsey`, because the rule documents a specific
  real configured falsey value.
- Optional for `diagnostic_only` and `manual_review_required` only when the rule
  is class-level guidance and is not tied to a concrete value.

Matching is type-strict. The validator must not coerce between `false`, `0`,
`"0"`, `""`, `null`, `[]`, and `{}`; each is a distinct observed value. If the
absent/default diagnostic taxonomy exposes a `value_kind`, future validators
should preserve that distinction rather than collapsing falsey shapes together.

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
- Reject every omit action. `omit_when_absent_in_api` and
  `omit_when_provider_placeholder` are reserved for a future design and are
  always invalid in V1, because no checkable runtime discriminator mechanism
  exists yet.
- Reject missing `id`, scope, `path`, `kind`, `action`, `evidence`, or
  `reason`.
- Reject missing `observed_value` per the "Observed Value Requirements And
  Matching" rules, and reject coercion between distinct falsey shapes.
- Reject provider/resource-type mismatch.
- Reject rules that omit `path`.
- Reject rules that use `raw_api_path`, `provider_state_path`, or `plan_path` as
  the primary rule key; those are evidence fields only. The V1 `path` is always
  the projected/provider-state path. Where projected/provider-state path
  validation is available, validate `path` against that namespace. Multi-namespace
  matching is future design.
- Reject sensitive path targets when statically known.
- Reject duplicate or conflicting rules per "Rule Identity And Conflicts",
  including overlapping exact-`resource_type` and `resource_prefix` scope.
- Reject rules that scope globally across providers or resource types.
- Reject rules that infer absence from value shape alone.
- Reject any broad `drop_empty_values`, `drop_falsey`, or
  `normalize_defaults` action.
- Render nothing.
- Normalize nothing.
- Change no projection behavior.
- Change no drift policy behavior.
- Do not alter `assert-adoptable` status.

## Assert-Adoptable Guidance

Matching an absent/default candidate can annotate blocked drift with guidance
when committed pack metadata declares a manual-review rule for the same
provider, resource scope, and plan path. That annotation keeps the plan blocked
until a future explicit projection rule exists and has transformed the projected
config before plan generation.

Absent/default diagnostics must not become drift tolerance.

## Open Questions

- How do we prove API absence versus an explicit server default without
  committing raw API payloads?
- How should before/after evidence be represented without committing raw state,
  plans, logs, or tenant identifiers?
- How should future multi-namespace matching relate raw API paths,
  provider-state paths, projected paths, and saved-plan paths?
- What checkable `runtime_discriminator` field and `projection_omit` routing
  would a later design add before omit actions can become valid?
- How should map and list element paths be represented?
- How should set hashing and order-insensitive collections be handled?
- How should absent/default rules compose with provider-config guidance?
- How should absent/default rules compose with sensitive-required diagnostics?
- Can tenant overlays disable or replace a pack normalization rule?

## Recommended Next Step

With the V1 validator contract now deterministic, external review can proceed,
followed by validator-only implementation planning. The contract is intended to
be directly implementable: V1 omit actions are invalid, the kind/action matrix
has no conditional cells, rule identity and conflict handling are defined,
`observed_value` requirements and type-strict matching are defined, and the V1
`path` namespace is fixed.

# V1 Validator Contract

This section freezes the exact V1 static validator contract for
`absent_defaults.rules`. The contract is directly implementable from the existing
validator code; no new design decisions should be made in the validator-only
implementation PR.

## V1 Validator Scope

The V1 absent/default validator:

- Validates metadata shape only.
- Returns normalized metadata (shallow copies with canonicalized `path`).
- Does not project.
- Does not omit.
- Does not change drift policy.
- Does not change `assert-adoptable` status.
- Does not execute Terraform/OpenTofu.
- Does not enforce cross-class duplicates.
- Does not authorize behavior.
- Does not create a second omission path parallel to `projection_omit`.
- May be used by `assert-adoptable` for additive manual-review guidance while
  preserving blocked plan status.

## Pack Metadata Key

- Top-level: `absent_defaults`
- Rule list: `absent_defaults.rules`
- Accessor: `packs.absent_default_rules(provider=None)`

## Accepted Keys

The closed V1 accepted key set for each rule is:

- `id`
- `provider`
- `resource_type`
- `resource_prefix`
- `path`
- `kind`
- `observed_value`
- `action`
- `evidence`
- `reason`
- `plan_path`
- `raw_api_path`
- `provider_state_path`

Any key outside this set is rejected as `unknown_key`.

## Required Fields

A V1 validator must require:

- `id`
- `provider`
- `path`
- `kind`
- `action`
- `evidence`
- `reason`

It must also require exactly one resource scope:

- `resource_type` or `resource_prefix`

All required string fields must be strings and non-empty after `str.strip()`.
Normalized metadata should strip these fields where appropriate.

### Conditional `observed_value` Requirement

`observed_value` is required when:

- `kind` is `api_explicit_default`, `provider_absent_placeholder`, or
  `terraform_schema_optional_default`; or
- `action` is `preserve_explicit_falsey`.

`observed_value` is type-strict: the validator preserves the exact JSON value
and rejects coercion between distinct falsey shapes (e.g., `0`, `"0"`, `false`,
`""`, `null`, `[]`, `{}`).

## Enum Constants

V1 `kind` enum:

- `api_absent`
- `api_explicit_default`
- `provider_absent_placeholder`
- `terraform_schema_optional_default`
- `real_configured_falsey`
- `provider_server_side_singleton_default`
- `paid_disabled_or_api_boundary_default`

V1 allowed actions:

- `diagnostic_only`
- `manual_review_required`
- `preserve_explicit_falsey`

V1 rejected actions (rejected with `rejected_in_v1_action`):

- `omit_when_absent_in_api`
- `omit_when_provider_placeholder`
- `drop_empty_values`
- `drop_falsey`
- `normalize_defaults`

Any other action is `unknown_action`.

### Kind/Action Matrix

| Kind | Allowed actions |
|---|---|
| `api_absent` | `diagnostic_only`, `manual_review_required` |
| `provider_absent_placeholder` | `diagnostic_only`, `manual_review_required` |
| `api_explicit_default` | `diagnostic_only`, `manual_review_required` |
| `terraform_schema_optional_default` | `diagnostic_only`, `manual_review_required` |
| `real_configured_falsey` | `diagnostic_only`, `manual_review_required`, `preserve_explicit_falsey` |
| `provider_server_side_singleton_default` | `diagnostic_only`, `manual_review_required` |
| `paid_disabled_or_api_boundary_default` | `diagnostic_only`, `manual_review_required` |

A V1 validator rejects out-of-matrix `kind` + `action` combinations.

## Path Namespace And Canonicalization

V1 `path` is the projected/provider-state path. `plan_path`, `raw_api_path`, and
`provider_state_path` are evidence-only fields and cannot replace `path`. The
validator canonicalizes `path` using `schema_paths.parse_report_path(path)` then
`schema_paths.format_path(parsed)`. Accepted `[0]` and `[*]` normalize to `[]`.
Bare wildcard path segments and unsupported syntax are rejected. V1 adds no new
path syntax. Rule identity uses the canonicalized path.

### Sensitive Path Static Matching

Absent/default uses the **non-inverted** sensitive-path rule:

- If `sensitive_paths` is supplied, canonicalize each entry with the same path
  canonicalization flow.
- If the canonicalized rule `path` is present in the canonicalized sensitive-path
  set, reject the rule as `sensitive_path_target`.
- If no static set is supplied, skip this check.

Exact canonical match only. Ancestor/descendant matching is future
engine-integrated behavior. This is the opposite of the sensitive-required lane,
which accepts only paths that are in the sensitive set.

## Rule Identity And Conflicts

Rule identity is the tuple:

```
(provider, resource_scope, canonical_path)
```

where `resource_scope` is either `("type", resource_type)` or
`("prefix", resource_prefix)`.

Absent/default rules do not carry a `provider_version_constraint` field, so
identity does not include a version string. Different versions of the same rule
must be resolved by human review unless a future contract adds version tracking.

A V1 validator rejects:

- duplicate identical identity as `duplicate_rule`
- same identity with different `kind` as `conflicting_kind`
- same identity with different `action` as `conflicting_action`
- same identity with different `observed_value` as `conflicting_observed_value`

`evidence` and `reason` are required but are not conflict fields in V1. Same
identity with only `reason` or only `evidence` differing is still rejected as
`duplicate_rule`; it is never accepted as a merge. No merge rule exists in V1.

Overlapping scope:

- Exact `resource_type` and matching `resource_prefix` overlap with the same
  provider and canonical path is rejected as `overlapping_scope`.
- Prefix-prefix overlap is deferred unless a future contract explicitly
  specifies it.

## Provider / Resource Prefix Checking

If `provider_prefixes` is supplied:

- `resource_type` resolves to provider using longest-prefix match; mismatch is
  `provider_resource_mismatch`, unknown prefix is `provider_resource_unknown`.
- `resource_prefix` must exist in the map and map to the rule provider; mismatch
  is `provider_resource_mismatch`, unknown prefix is `provider_resource_unknown`.

If `provider_prefixes` is not supplied, the validator skips the provider/resource
consistency check.

Scope errors:

- both `resource_type` and `resource_prefix`: `both_resource_scopes`
- neither: `missing_resource_scope`

## Error Categories And Message Contract

The current implementation raises `ValueError` with stable message fragments
rather than structured error objects. The following logical categories are
documentation/test categories, not a structured runtime error type.

| Category | Trigger | Message fragment |
|---|---|---|
| `rules_not_list` | `rules` is not a list | `absent_defaults.rules must be a list` |
| `rule_not_object` | a rule is not an object | `must be an object` |
| `unknown_key` | key outside accepted set | `unknown rule key` |
| `missing_id` | no `id` | `missing id` |
| `missing_provider` | no `provider` | `missing provider` |
| `missing_path` | no `path` | `missing path` |
| `missing_kind` | no `kind` | `missing kind` |
| `missing_action` | no `action` | `missing action` |
| `missing_evidence` | no `evidence` | `missing evidence` |
| `missing_reason` | no `reason` | `missing reason` |
| `missing_resource_scope` | neither `resource_type` nor `resource_prefix` | `missing resource scope` |
| `both_resource_scopes` | both `resource_type` and `resource_prefix` | `cannot specify both resource_type and resource_prefix` |
| `field_must_be_string` | required string field is not a string or empty after strip | `missing <field>` |
| `missing_observed_value` | `kind` or `action` requires `observed_value` | `kind <kind> requires observed_value` / `action <action> requires observed_value` |
| `unknown_kind` | `kind` not in enum | `unknown kind` |
| `unknown_action` | `action` not in allowed/rejected | `unknown action` |
| `rejected_in_v1_action` | rejected action | `action <action> is rejected in V1` |
| `out_of_matrix_action` | `kind` + `action` not in matrix | `kind <kind> does not allow action <action>` |
| `invalid_path_syntax` | path cannot be parsed/canonicalized | `unsupported syntax` |
| `bare_wildcard_segment` | path segment is bare `*` | `bare wildcard segment` |
| `evidence_path_cannot_replace_path` | evidence path present without `path` | `<field> cannot replace path` |
| `sensitive_path_target` | rule path is in supplied sensitive set | `targets a known sensitive path` |
| `provider_resource_mismatch` | resource scope maps to a different provider | `resource_type <x> resolves to provider <actual>, not <expected>` / `resource_prefix <x> is declared for provider <actual>, not <expected>` |
| `provider_resource_unknown` | resource scope has no known prefix | `resource_type <x> is not declared in provider_prefixes` / `resource_prefix <x> is not declared in provider_prefixes` |
| `duplicate_rule` | identical identity tuple | `duplicate rule` |
| `conflicting_kind` | same identity, different `kind` | `conflicting kind` |
| `conflicting_action` | same identity, different `action` | `conflicting action` |
| `conflicting_observed_value` | same identity, different `observed_value` | `conflicting observed_value` |
| `overlapping_scope` | same type and matching prefix for same provider/path | `overlaps resource_prefix` |

## Test Matrix For V1 Validator

### Positive tests

- `None` rules -> empty normalized list.
- Empty rules -> empty normalized list.
- Valid `provider_absent_placeholder` + `manual_review_required` + empty `observed_value`.
- Valid `provider_server_side_singleton_default` + `diagnostic_only`.
- Valid `real_configured_falsey` + `preserve_explicit_falsey` + `False` observed value.
- `api_absent` without `observed_value`.
- `resource_prefix` scope accepted.
- Path canonicalization `[0]` -> `[]`.
- Path canonicalization `[*]` -> `[]`.
- Optional `plan_path`, `raw_api_path`, `provider_state_path` accepted.
- Provider/resource match accepted when `provider_prefixes` is supplied.
- Static sensitive-path rejection with canonicalized paths.
- Validator skips provider/resource check when `provider_prefixes` is `None`.

### Negative tests

- `rules` not a list.
- rule not an object.
- unknown key.
- missing each required field.
- non-string required string field.
- empty/whitespace required string field.
- both `resource_type` and `resource_prefix`.
- neither `resource_type` nor `resource_prefix`.
- missing `observed_value` for kinds/actions that require it.
- unknown `kind`.
- unknown `action`.
- each rejected action.
- out-of-matrix `kind` + `action`.
- unsupported path syntax.
- bare wildcard path segment.
- evidence path without `path`.
- provider/resource mismatch.
- unknown provider/resource prefix.
- duplicate identical rule.
- same identity conflicting `kind`, `action`, or `observed_value`.
- same identity differing only `reason` rejected as `duplicate_rule` (not merged).
- overlapping `resource_type`/`resource_prefix` same provider/path rejected.
- `sensitive_paths` supplied but rule path present rejected.
- no cross-class duplicate checks run.
- no behavior authorized.

## Recommended Next Step

With the V1 validator contract now frozen, the next step is external review of
the contract, followed by any validator-only hardening if needed. The contract
is intended to be directly implementable: V1 omit actions are invalid, the
kind/action matrix has no conditional cells, rule identity and conflict handling
are defined, `observed_value` requirements and type-strict matching are defined,
and the V1 `path` namespace is fixed.

Still do not implement normalization behavior. Do not implement it until the
metadata contract and evidence requirements survive validator-only review and at
least one provider lab proves a narrow, safe class.

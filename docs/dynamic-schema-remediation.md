# Dynamic Schema Remediation Design

This is a design note, not implemented behavior. The current engine can
diagnose dynamic-schema paths, but it does not authorize projection or omission
of those paths from pack metadata.

The problem is deliberately dangerous. When provider schema cannot statically
describe a path, the wrong move is to either project it blindly or ignore it
silently. A future remediation rule must be pack-owned, path-specific,
provider-evidence-backed, provider-version-aware, and blocked by default until
explicit behavior exists.

## Relationship To Existing `dynamic_schema` Diagnostics

The engine already has diagnostic classification for dynamic or open schema paths
through `engine.dynamic_schema`. Future dynamic-schema remediation must not reuse
that diagnostic signal as a projection or omission authority.

`engine.dynamic_schema` reports statuses such as `schema_known`,
`pack_schema_gap`, `schema_computed_only`, and `unknown_schema_path`. Those are
classifications, not permissions. A `pack_schema_gap` result means the pack
must decide what to do; it does not mean the engine may automatically keep or
drop the value.

Any future dynamic-schema behavior must either:

- remain diagnostic/manual-review only, or
- feed or reuse the existing projection paths so required-path guards and
  advisory omission accounting stay intact.

## The Real Question

When Terraform provider schema cannot statically describe a path, how does
Infrawright decide whether that path is:

1. safe to project,
2. unsafe and blocked,
3. raw-only advisory,
4. provider-observed but intentionally omitted, or
5. impossible to automate?

Right now the engine has diagnostics. It does not have a clean contract for
what a pack is allowed to say about dynamic or unknown schema paths.

## Failure Classes

Dynamic schema work must distinguish these cases before any projection rule is
considered:

| Kind | Meaning |
|------|---------|
| `provider_state_only` | The path appears only in imported provider state; it is not projectable from the schema and may not be user-configurable. |
| `provider_computed_map` | The provider returns a map whose keys are not declared in the schema; keys may be stable, computed, or tenant-specific. |
| `freeform_object` | The schema declares an object but leaves its members open (`{}`); the provider may add or remove members between versions. |
| `opaque_json_blob` | The provider accepts or returns a JSON string or nested structure that the schema treats as an opaque value. |
| `map_key_discovered_after_import` | A map key only appears after import because the provider populates it from remote state; it may not exist in user intent. |
| `unstable_collection_identity` | A set, list, or map element has no stable identity across plan/apply cycles, so projection ordering or membership is unsafe. |
| `schema_unknown_but_provider_observed` | The provider exposes a path in state that does not correspond to any declared schema member. |
| `raw_api_only_provider_blind` | The path is visible in raw API readback but never appears in provider state or projected tfvars. |
| `provider_observed_projection_unsafe` | The provider observes the path but the engine cannot prove that projecting it would be stable, safe, or user-owned. |

These classes can overlap in a saved plan. The rule author must prove which
class applies before a future remediation rule exists.

## What Is Safe Vs Unsafe

Likely handling per class:

| Case | Likely handling |
|------|-----------------|
| Provider-observed map key with stable key and scalar value | Possible future metadata candidate, but only with explicit ownership evidence. |
| Raw API only, provider never sees it | Advisory only; the engine must not project or omit from provider state. |
| Opaque JSON blob | Manual review; projection is unsafe without a schema contract. |
| Dynamic block with unstable set identity | Blocked or manual review; projection order/identity cannot be guaranteed. |
| Provider-computed-only dynamic path | Do not project; the user cannot own it. |
| User-owned freeform map | Possible only with explicit ownership metadata and stable-key evidence. |
| Unknown schema path that is provider-observed | Manual review until the schema contract is understood. |

No class is automatically safe just because the provider returned a value.

## No Global Dynamic Projection

The design must forbid these broad defaults:

- Do not project dynamic paths just because they exist.
- Do not omit dynamic paths just because schema is unknown.
- Do not infer ownership from provider state alone.
- Do not treat schema-unknown as equivalent to schema-optional.
- Do not apply a single heuristic across all resource types or providers.

Dynamic schema is per-provider, per-provider-version, per-resource, and
per-path. There is no global switch.

## Required Evidence

A future rule must cite evidence. V1 is guidance-only, so baseline evidence is
required for every rule. Future projection-changing actions require additional
elevated evidence.

### Baseline Evidence

Baseline evidence is required for `diagnostic_only` and `manual_review_required`:

- Provider version constraint that the rule was observed against.
- Provider-state path and value, or provider-state absence.
- Terraform schema diagnostic output for the path.
- Saved-plan path and drift behavior, if applicable.
- Raw API path and value, or raw API absence, if applicable.
- Ownership rationale (`user_owned`, `provider_computed`, `server_owned`, or `unknown`).
- Corroboration that the ownership rationale is not inferred from provider state
  alone. Lab evidence must tie ownership to a deliberate source such as user
  intent, schema documentation, provider behavior, or a controlled before/after
  plan. V1 cannot prove this inference chain from free-text evidence; it is
  enforced during design/lab review, not static validation.
- Sensitivity status or a statement that sensitivity is unknown/manual-review.
- Cleanup and safety notes from the lab.

### Elevated Evidence

Elevated evidence is required for any future projection-changing action:

- Proof of stable key or collection identity across plan/apply cycles.
- Before/after plan behavior with the path included.
- Proof the value is user-owned.
- Proof it is not provider-computed or server-owned.
- Proof it is non-sensitive.
- Proof it is safe to project in the provider-state namespace.

Evidence should be summarized in committed lab docs or sanitized fixtures. Do
not commit raw state, plans, secrets, tenant identifiers, provider logs, or
temporary roots just to prove a dynamic-schema candidate.

## Proposed Metadata Shape

This is illustrative only. It is not loaded by the engine today.

```json
{
  "dynamic_schema": {
    "rules": [
      {
        "id": "cloudflare_ruleset_action_parameters_dynamic_map",
        "provider": "cloudflare",
        "provider_version_constraint": ">= 4.0.0, < 5.0.0",
        "resource_type": "cloudflare_ruleset",
        "path": "rules[].action_parameters",
        "kind": "provider_observed_projection_unsafe",
        "ownership": "server_owned",
        "action": "diagnostic_only",
        "evidence": "docs/provider-labs/cloudflare-free-tier-pr32.md",
        "reason": "Provider exposes a dynamic nested map under action_parameters; the schema cannot prove stable projection semantics."
      }
    ]
  }
}
```

`ownership` is one of `user_owned`, `provider_computed`, `server_owned`, or
`unknown`. `manual_review_required` is an action, not an ownership value. The
validator rejects any ownership outside the enum.

### Accepted Keys

A future rule may contain these keys:

- `id`: stable rule identifier.
- `provider`: provider short name.
- `provider_version_constraint`: version range the rule was observed against.
- `resource_type` or `resource_prefix`: explicit scope.
- `path`: V1 primary key in the provider-state namespace.
- `kind`: one of the failure classes above.
- `ownership`: one of `user_owned`, `provider_computed`, `server_owned`, `unknown`.
- `action`: V1 guidance-only action.
- `evidence`: committed lab report or sanitized fixture path.
- `reason`: human-readable justification.
- `raw_api_path`: optional raw API path evidence when it differs from `path`.
- `projected_path`: optional projected tfvars path evidence when it differs from
  `path`.
- `plan_path`: optional saved-plan path evidence when it differs from `path`.

### Required V1 Fields

- `id`
- `provider`
- `provider_version_constraint`
- exactly one resource scope: `resource_type` or `resource_prefix`
- `path`
- `kind`
- `ownership`
- `action`
- `evidence`
- `reason`

Unknown keys are rejected. The metadata contract is intentionally small.

## Path Namespace

V1 `path` is always the provider-state path. Some dynamic-schema paths have no
projected path because the schema cannot describe them; that is why the
provider-state namespace is the V1 primary key.

`raw_api_path`, `projected_path`, and `plan_path` are evidence fields only. They
cannot replace `path`.

V1 path input must use the engine's normalized path syntax with `[]` collection
selectors. The validator may canonicalize inputs using the existing
`schema_paths.parse_report_path` / `format_path` behavior before identity
comparison. Inputs such as `[0]` or `[*]`, if accepted by the existing parser,
normalize to `[]`. Bare wildcard path segments or unsupported syntax are rejected.
Rule identity uses the canonicalized provider-state path.

V1 does not add new path syntax.

## Provider Version Constraint

Dynamic schema gaps can change across provider versions. `provider_version_constraint`
is required for every rule because dynamic-schema behavior is
provider-version-sensitive.

The V1 validator rejects rules missing `provider_version_constraint`. In V1 the
validator only checks that the value is a non-empty string after `str.strip()`.
It does not parse or semantically evaluate version constraints. The example uses
Terraform-style constraint syntax as an intended future grammar;
parsing/evaluating provider version constraints is future engine-integrated
behavior.

## Allowed V1 Actions

V1 allows only guidance/classification actions:

- `diagnostic_only`: classify and explain, but do not change projection.
- `manual_review_required`: block automation and require a human decision.

## Reserved Future Actions

The following actions are reserved for a future design only and are invalid in
V1:

- `preserve_observed_scalar`: reserved for a future design where the path is
  proven to be a stable, user-owned scalar with no schema ambiguity.
- `projection_omit_candidate`: reserved for a future design that routes through
  the existing `DriftPolicy.projection_omit` path with the same fail-loud
  behavior and reporting visibility.

These actions become valid only after a later design proves ownership/stability
and, for omission, routes through the existing projection omission path. Until
then, they are rejected regardless of evidence.

Avoid broad actions such as `project_dynamic`, `accept_unknown`, or
`ignore_schema_gap`. They are too coarse for this failure class.

## Kind/Action/Ownership Legality Matrix

The V1 validator must reject out-of-matrix combinations.

| Kind | Allowed V1 actions | Allowed ownership |
|------|--------------------|---------------------|
| `provider_state_only` | `diagnostic_only`, `manual_review_required` | `provider_computed`, `server_owned`, `unknown` |
| `provider_computed_map` | `diagnostic_only`, `manual_review_required` | `provider_computed`, `server_owned`, `unknown` |
| `freeform_object` | `diagnostic_only`, `manual_review_required` | `user_owned`, `provider_computed`, `server_owned`, `unknown` |
| `opaque_json_blob` | `diagnostic_only`, `manual_review_required` | `provider_computed`, `server_owned`, `unknown` |
| `map_key_discovered_after_import` | `diagnostic_only`, `manual_review_required` | `provider_computed`, `server_owned`, `unknown` |
| `unstable_collection_identity` | `diagnostic_only`, `manual_review_required` | `provider_computed`, `server_owned`, `unknown` |
| `schema_unknown_but_provider_observed` | `diagnostic_only`, `manual_review_required` | `user_owned`, `provider_computed`, `server_owned`, `unknown` |
| `raw_api_only_provider_blind` | `diagnostic_only`, `manual_review_required` | `unknown` |
| `provider_observed_projection_unsafe` | `diagnostic_only`, `manual_review_required` | `provider_computed`, `server_owned`, `unknown` |

`user_owned` is not globally allowed in V1. It is allowed only for `freeform_object`
and `schema_unknown_but_provider_observed`, and only with the guidance-only
actions `diagnostic_only` or `manual_review_required`. Those are the only classes
where "user-owned but schema unknown" plausibly makes sense without implying
projection behavior. For all other kinds, `user_owned` is either contradictory or
too vague.

Ownership is a classification, not a permission to project. Even `user_owned`
does not grant projection permission in V1. Future projection-changing actions
require elevated evidence and a later design regardless of ownership.

The validator must reject any `ownership` of `provider_computed`, `server_owned`,
or `unknown` combined with a projection-changing action in a future design.

## Rule Identity And Conflicts

A V1 validator needs a deterministic identity so it can reject duplicate and
conflicting rules. Rule identity is:

```text
provider + provider_version_constraint + resource scope + path
```

- `provider_version_constraint` is compared by exact string equality after
  `str.strip()`. Semantic equivalence of version ranges is not evaluated in V1.
  For example, `>= 4.0.0, < 5.0.0` and `>=4.0.0,<5.0.0` are distinct strings and
  therefore distinct identities unless a future canonicalization rule is designed.
- `path` is the canonicalized provider-state path.
- Resource scope is either an exact `resource_type` or a `resource_prefix`.
- Identical identity with identical `kind`, `ownership`, `action`, `evidence`,
  `raw_api_path`, `projected_path`, and `plan_path` is a **duplicate** and is
  rejected.
- Identical identity with a different `kind`, `ownership`, `action`, `evidence`,
  `raw_api_path`, `projected_path`, or `plan_path` is a **conflict** and is
  rejected.
- An exact `resource_type` and a `resource_prefix` that match the same provider,
  stripped version constraint string, canonical path, and scope are treated as
  **overlapping scope** and are rejected. Semantic overlap of version ranges is
  deferred to future engine-integrated behavior.
- There is no merge rule in V1. The validator never combines two rules; it
  rejects the ambiguity instead.

The V1 validator also rejects:

- both `resource_type` and `resource_prefix` present
- neither `resource_type` nor `resource_prefix` present
- provider/resource mismatch when a provider_prefixes map is available

## Sensitive Path Handling

Any future dynamic-schema behavior must fail loud on sensitive overlap rather
than projecting or omitting.

V1 static sensitive-path rejection uses exact string match against the supplied
sensitive-path set, using the same path canonicalization used for rule identity
if applicable. Descendant/ancestor sensitive overlap detection is future
engine-integrated behavior. Dynamic paths with ambiguous sensitivity must
remain `manual_review_required` or `diagnostic_only`. Do not invent a new
sensitivity resolver in V1; document the boundary and rely on existing or
caller-supplied static sensitivity metadata.

## Relationship To Sibling Systems

### `projection_omit`

Dynamic schema must not create a second omission authority. Any future omission
candidate must route through the existing `DriftPolicy.projection_omit` path. In
V1, the candidate action `projection_omit_candidate` is rejected. The omission
itself must preserve the same fail-loud behavior and reporting visibility as the
existing projection path.

### `absent_defaults`

Paths that are primarily falsey/default/placeholder value semantics belong to
absent/default normalization. Paths that are primarily schema-unknown or
provider-state ownership semantics belong to dynamic-schema remediation. A path
may be relevant to both, but a rule should be filed under the design that
best describes the failure class.

Cross-design duplicate handling is deferred until a cross-design identity rule
exists. Human reviewers should avoid declaring both `absent_defaults` and
`dynamic_schema` rules for the same provider/resource/path unless the ownership
boundary is explicit. If a future cross-design check is added, it must account
for absent-default rules lacking `provider_version_constraint`.

### `provider_config`

Provider-level settings remain provider-config guidance, not dynamic-schema
rules. Dynamic-schema rules target resource paths; provider-config rules target
provider arguments.

### Raw-only / provider-blind advisory

`raw_api_only_provider_blind` must remain visible in raw-only advisory
accounting. It is a classification-only kind in dynamic-schema remediation and
must never suppress raw-only advisory output. If a future design cannot keep
raw-only paths visible, the kind should be removed from dynamic-schema and left
in the raw-only advisory system.

## Validator-Only First Stage

The first behavior PR, if any, should be validator-only:

- Parse and validate the metadata shape.
- Reject unknown keys.
- Reject missing required fields.
- Reject unknown `kind`.
- Reject unknown `ownership`.
- Reject unknown `action`.
- Reject all reserved actions: `preserve_observed_scalar`, `projection_omit_candidate`.
- Reject out-of-matrix kind/action/ownership combinations.
- Reject missing `provider_version_constraint`.
- Reject sensitive path targets when statically known.
- Reject duplicate/conflicting rules by identity.
- Reject overlapping `resource_type`/`resource_prefix` scope.
- Reject provider/resource mismatch when a provider_prefixes map is available.
- Reject `raw_api_path`, `projected_path`, or `plan_path` used as the primary
  rule key.
- Render nothing.
- Project nothing.
- Omit nothing.
- Change no projection behavior.
- Change no drift policy behavior.
- Do not alter `assert-adoptable` status.

## What This Is Not

This design is not:

- Implementing dynamic path projection.
- Automatically accepting schema-unknown paths.
- Suppressing dynamic-schema diagnostics.
- Drift tolerance.
- HCL rendering.
- Provider-specific hacks.
- Generalized JSON blob support.
- A second omission engine parallel to `projection_omit`.

# V1 Validator Contract

This section freezes the exact V1 static validator contract for
`dynamic_schema.rules`. The contract is directly implementable from the existing
validator code; no new design decisions should be made in the validator-only
implementation PR.

## V1 Validator Scope

The V1 dynamic-schema validator:

- Validates metadata shape only.
- Returns normalized metadata (shallow copies with canonicalized `path` and stripped
  `provider_version_constraint`).
- Does not project dynamic-schema paths.
- Does not omit paths.
- Does not change projection behavior.
- Does not change drift policy.
- Does not change advisory behavior.
- Does not change `assert-adoptable` status.
- Does not execute Terraform/OpenTofu.
- Does not enforce cross-class duplicates.
- Does not authorize behavior.
- Does not create a second omission path parallel to `projection_omit`.

## Pack Metadata Key

- Top-level: `dynamic_schema`
- Rule list: `dynamic_schema.rules`
- Accessor: `packs.dynamic_schema_rules(provider=None)`

## Accepted Keys

The closed V1 accepted key set for each rule is:

- `id`
- `provider`
- `provider_version_constraint`
- `resource_type`
- `resource_prefix`
- `path`
- `kind`
- `ownership`
- `action`
- `evidence`
- `reason`
- `raw_api_path`
- `projected_path`
- `plan_path`

Any key outside this set is rejected as `unknown_key`.

## Required Fields

A V1 validator must require:

- `id`
- `provider`
- `provider_version_constraint`
- `path`
- `kind`
- `ownership`
- `action`
- `evidence`
- `reason`

It must also require exactly one resource scope:

- `resource_type` or `resource_prefix`

All required string fields must be strings and non-empty after `str.strip()`.
Normalized metadata should strip these fields where appropriate.

## Enum Constants

V1 `kind` enum:

- `provider_state_only`
- `provider_computed_map`
- `freeform_object`
- `opaque_json_blob`
- `map_key_discovered_after_import`
- `unstable_collection_identity`
- `schema_unknown_but_provider_observed`
- `raw_api_only_provider_blind`
- `provider_observed_projection_unsafe`

V1 `ownership` enum:

- `user_owned`
- `provider_computed`
- `server_owned`
- `unknown`

V1 allowed actions:

- `diagnostic_only`
- `manual_review_required`

V1 reserved actions (rejected with `rejected_in_v1_action`):

- `preserve_observed_scalar`
- `projection_omit_candidate`

Any other action is `unknown_action`.

### Kind/Ownership Matrix

| Kind | Allowed ownership |
|---|---|
| `provider_state_only` | `provider_computed`, `server_owned`, `unknown` |
| `provider_computed_map` | `provider_computed`, `server_owned`, `unknown` |
| `freeform_object` | `user_owned`, `provider_computed`, `server_owned`, `unknown` |
| `opaque_json_blob` | `provider_computed`, `server_owned`, `unknown` |
| `map_key_discovered_after_import` | `provider_computed`, `server_owned`, `unknown` |
| `unstable_collection_identity` | `provider_computed`, `server_owned`, `unknown` |
| `schema_unknown_but_provider_observed` | `user_owned`, `provider_computed`, `server_owned`, `unknown` |
| `raw_api_only_provider_blind` | `unknown` |
| `provider_observed_projection_unsafe` | `provider_computed`, `server_owned`, `unknown` |

A V1 validator rejects out-of-matrix `kind` + `ownership` combinations.

## Path Namespace And Canonicalization

V1 `path` is the provider-state path. `raw_api_path`, `projected_path`, and
`plan_path` are evidence-only fields and cannot replace `path`. The validator
canonicalizes `path` using `schema_paths.parse_report_path(path)` then
`schema_paths.format_path(parsed)`. Accepted `[0]` and `[*]` normalize to `[]`.
Bare wildcard path segments and unsupported syntax are rejected. V1 adds no new
path syntax. Rule identity uses the canonicalized path.

### Sensitive Path Static Matching

Dynamic-schema uses the **non-inverted** sensitive-path rule:

- If `sensitive_paths` is supplied, canonicalize each entry with the same path
  canonicalization flow.
- If the canonicalized rule `path` is present in the canonicalized sensitive-path
  set, reject the rule as `sensitive_path_target`.
- If no static set is supplied, skip this check.

Exact canonical match only. Ancestor/descendant matching is future
engine-integrated behavior. Do not invent a new sensitivity resolver in V1.

## Provider Version Rule

`provider_version_constraint` is required, must be a string, and must be
non-empty after `str.strip()`. The normalized value is the stripped string. V1
does not parse or semantically evaluate version constraints. Rule identity uses
exact stripped string equality. Semantically equivalent but string-different
ranges are distinct identities.

## Rule Identity And Conflicts

Rule identity is the tuple:

```
(provider, stripped_provider_version_constraint, resource_scope, canonical_path)
```

where `resource_scope` is either `("type", resource_type)` or
`("prefix", resource_prefix)`.

A V1 validator rejects:

- duplicate identical identity as `duplicate_rule`
- same identity with different `kind` as `conflicting_kind`
- same identity with different `ownership` as `conflicting_ownership`
- same identity with different `action` as `conflicting_action`
- same identity with different `evidence` as `conflicting_evidence`
- same identity with different `raw_api_path` as `conflicting_raw_api_path`
- same identity with different `projected_path` as `conflicting_projected_path`
- same identity with different `plan_path` as `conflicting_plan_path`

`reason` is required but is not a conflict field in V1. Same identity with only
`reason` differing is still rejected as `duplicate_rule`; it is never accepted as
a merge. No merge rule exists in V1.

Overlapping scope:

- Exact `resource_type` and matching `resource_prefix` overlap with the same
  provider, stripped version string, and canonical path is rejected as
  `overlapping_scope`.
- Version strings must be exactly equal after stripping; no semantic version
  overlap is computed.
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
| `rules_not_list` | `rules` is not a list | `dynamic_schema.rules must be a list` |
| `rule_not_object` | a rule is not an object | `must be an object` |
| `unknown_key` | key outside accepted set | `unknown rule key` |
| `missing_id` | no `id` | `missing id` |
| `missing_provider` | no `provider` | `missing provider` |
| `missing_provider_version_constraint` | no `provider_version_constraint` | `missing provider_version_constraint` |
| `missing_path` | no `path` | `missing path` |
| `missing_kind` | no `kind` | `missing kind` |
| `missing_ownership` | no `ownership` | `missing ownership` |
| `missing_action` | no `action` | `missing action` |
| `missing_evidence` | no `evidence` | `missing evidence` |
| `missing_reason` | no `reason` | `missing reason` |
| `missing_resource_scope` | neither `resource_type` nor `resource_prefix` | `missing resource scope` |
| `both_resource_scopes` | both `resource_type` and `resource_prefix` | `cannot specify both resource_type and resource_prefix` |
| `field_must_be_string` | required string field is not a string or empty after strip | `missing <field>` or `<field> must be a string` |
| `unknown_kind` | `kind` not in enum | `unknown kind` |
| `unknown_ownership` | `ownership` not in enum | `unknown ownership` |
| `unknown_action` | `action` not in allowed/reserved | `unknown action` |
| `rejected_in_v1_action` | reserved action | `action <action> is rejected in V1` |
| `out_of_matrix_ownership` | `kind` + `ownership` not in matrix | `kind <kind> does not allow ownership <ownership>` |
| `invalid_path_syntax` | path cannot be parsed/canonicalized | `unsupported syntax` |
| `bare_wildcard_segment` | path segment is bare `*` | `bare wildcard segment` |
| `evidence_path_cannot_replace_path` | evidence path present without `path` | `<field> cannot replace path` |
| `sensitive_path_target` | rule path is in supplied sensitive set | `targets a known sensitive path` |
| `provider_resource_mismatch` | resource scope maps to a different provider | `resource_type <x> resolves to provider <actual>, not <expected>` / `resource_prefix <x> is declared for provider <actual>, not <expected>` |
| `provider_resource_unknown` | resource scope has no known prefix | `resource_type <x> is not declared in provider_prefixes` / `resource_prefix <x> is not declared in provider_prefixes` |
| `duplicate_rule` | identical identity tuple | `duplicate rule` |
| `conflicting_kind` | same identity, different `kind` | `conflicting kind` |
| `conflicting_ownership` | same identity, different `ownership` | `conflicting ownership` |
| `conflicting_action` | same identity, different `action` | `conflicting action` |
| `conflicting_evidence` | same identity, different `evidence` | `conflicting evidence` |
| `conflicting_raw_api_path` | same identity, different `raw_api_path` | `conflicting raw_api_path` |
| `conflicting_projected_path` | same identity, different `projected_path` | `conflicting projected_path` |
| `conflicting_plan_path` | same identity, different `plan_path` | `conflicting plan_path` |
| `overlapping_scope` | same type and matching prefix for same provider/version/path | `overlaps resource_prefix` |

## Test Matrix For V1 Validator

### Positive tests

- `None` rules -> empty normalized list.
- Empty rules -> empty normalized list.
- Valid `provider_observed_projection_unsafe` + `unknown` + `manual_review_required`.
- Valid `raw_api_only_provider_blind` + `unknown` + `diagnostic_only`.
- Accepted `ownership` values for each `kind` in the matrix.
- `provider_version_constraint` stripped and preserved as stripped string.
- Path canonicalization `[0]` -> `[]`.
- Path canonicalization `[*]` -> `[]`.
- Optional `raw_api_path`, `projected_path`, `plan_path` accepted.
- `resource_prefix` scope accepted.
- Provider/resource match accepted when `provider_prefixes` is supplied.
- Static sensitive-path rejection with canonicalized paths.
- Validator skips sensitive-path check when `sensitive_paths` is `None`.
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
- unknown `kind`.
- unknown `ownership`.
- unknown `action`.
- each reserved action.
- out-of-matrix `kind` + `ownership`.
- unsupported path syntax.
- bare wildcard path segment.
- evidence path without `path`.
- provider/resource mismatch.
- unknown provider/resource prefix.
- duplicate identical rule.
- same identity conflicting `kind`, `ownership`, `action`, `evidence`, or evidence paths.
- same identity differing only `reason` rejected as `duplicate_rule` (not merged).
- overlapping `resource_type`/`resource_prefix` same version/path rejected.
- overlapping `resource_type`/`resource_prefix` different version accepted.
- `sensitive_paths` supplied but rule path present rejected.
- no cross-class duplicate checks run.
- no projection/omission behavior authorized.

## Recommended Next Step

With the V1 validator contract now frozen, the next step is external review of
the contract, followed by any validator-only hardening if needed. Do not
implement projection behavior.

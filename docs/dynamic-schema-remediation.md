# Dynamic Schema Remediation Design

This is a design note, not implemented behavior. The current engine can
diagnose dynamic-schema paths, but it does not authorize projection or omission
of those paths from pack metadata.

The problem is deliberately dangerous. When provider schema cannot statically
describe a path, the wrong move is to either project it blindly or ignore it
silently. A future remediation rule must be pack-owned, path-specific,
provider-evidence-backed, and blocked by default until explicit behavior exists.

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

Dynamic schema is per-provider, per-resource, and per-path. There is no global
switch.

## Required Evidence

A future rule must cite evidence showing all relevant sides of the path:

- Raw API path and value, or raw API absence.
- Provider-state path and value, or provider-state absence.
- Projected tfvars path and value, or projected absence.
- Saved-plan path and drift behavior.
- Terraform schema diagnostic output for the path.
- Whether the field is user-owned, provider-computed, or server-owned.
- Whether map keys or collection elements are stable across plan/apply cycles.
- Before/after plan behavior when the path is included or excluded.
- Cleanup and safety notes from the lab.

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
        "resource_type": "cloudflare_ruleset",
        "path": "rules[].action_parameters",
        "kind": "provider_observed_projection_unsafe",
        "ownership": "manual_review_required",
        "action": "diagnostic_only",
        "evidence": "docs/provider-labs/cloudflare-free-tier-pr32.md",
        "reason": "Provider exposes a dynamic nested map under action_parameters; the schema cannot prove stable projection semantics."
      }
    ]
  }
}
```

Required fields for a future rule should include:

- `id`: stable rule identifier.
- `provider`: provider short name.
- `resource_type` or `resource_prefix`: explicit scope.
- `path`: the projected/provider-state path under review.
- `kind`: one of the failure classes above.
- `ownership`: who owns the value (`user_owned`, `provider_computed`,
  `server_owned`, `unknown`).
- `action`: proposed narrow action.
- `evidence`: committed lab report or sanitized fixture path.
- `reason`: human-readable justification.
- `raw_api_path`: optional raw API path evidence when it differs from `path`.
- `provider_state_path`: optional provider-state path evidence when it differs
  from `path`.
- `plan_path`: optional saved-plan path evidence when it differs from `path`.

`ownership` is not a permission to project. It is a classification that must be
proven before any future action is allowed.

## Allowed Future Actions

Future actions should be deliberately narrow:

- `diagnostic_only`: classify and explain, but do not change projection.
- `manual_review_required`: block automation and require a human decision.
- `preserve_observed_scalar`: reserved for a future design where the path is
  proven to be a stable, user-owned scalar with no schema ambiguity.
- `projection_omit_candidate`: reserved for a future design that routes through
  the existing `DriftPolicy.projection_omit` path with the same fail-loud
  behavior and reporting visibility.

Avoid broad actions such as `project_dynamic`, `accept_unknown`, or
`ignore_schema_gap`. They are too coarse for this failure class.

## Validator-Only First Stage

The first behavior PR, if any, should be validator-only:

- Parse and validate the metadata shape.
- Reject unknown `kind`.
- Reject unknown `ownership`.
- Reject unknown `action`.
- Reject out-of-matrix kind/action/ownership combinations.
- Reject every action that would change projection until a later design proves
  the path is safe and routes through the existing projection paths.
- Reject missing `id`, scope, `path`, `kind`, `ownership`, `action`, `evidence`,
  or `reason`.
- Reject provider/resource mismatch when a provider_prefixes map is available.
- Reject rules that scope globally across providers or resource types.
- Reject rules that infer ownership from provider state alone.
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

## Recommended Next Step

External review of this design, followed by correction PRs if needed. Only
after the design survives review should a validator-only implementation PR be
planned. No dynamic projection behavior should be implemented until the metadata
contract and evidence requirements survive validator-only review and at least
one provider lab proves a narrow, safe class that can be promoted.

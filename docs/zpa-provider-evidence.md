# ZPA Provider v4.4.9 Evidence

This evidence records the provider-source facts for the 16 fetch-backed ZPA
resources. The canonical
machine-readable matrix is
[`packs/zpa/evidence/zpa-provider-v4.4.9.json`](../packs/zpa/evidence/zpa-provider-v4.4.9.json).

It is deliberately narrower than a compatibility claim. Static provider source
can establish import dispatch, Read identity assignments, schema shape, and
sensitivity. It cannot prove Terraform's exact `-generate-config-out` bytes or
whether the generated configuration survives provider validation. Every matrix
row therefore remains `terraform_runtime_evidence_required` for generated
configuration.

## Source Binding

The matrix is bound to:

- `zscaler/terraform-provider-zpa` tag `v4.4.9`;
- commit `1d4f43cc4c59a24d8380f0c655a07b6da7199465`;
- the complete SHA-256 of every consulted provider source file;
- inclusive, SHA-256-bound line ranges for every import, Read-identity, and
  exception claim; and
- the committed ZPA pack manifest, registry, relevant overrides, and provider
  schema dump.

Current tests validate the matrix digest and schema. The optional external
source test also replays every whole-file and inclusive-range binding against
the exact provider tag. A reviewer still reads the cited source ranges to
decide whether each curated claim is correct.

## v4.4.9 refresh boundary

The provider inventory remains 55 resource schemas and 71 data-source schemas;
Infrawright's registry still contains 54 ZPA resources. The schema refresh has
16 effective transitions rather than a resource-count expansion:

- `bypass_on_reauth` on `zpa_application_segment` changes from
  Optional+Computed to Optional;
- `clientless_apps` changes from list to set on the two browser-access
  resources, and `common_apps_dto.apps_config` changes from list to set on
  `zpa_application_segment_pra`;
- access policy and access-policy-v2 gain
  `device_posture_failure_notification_enabled`;
- the capabilities rule gains `control_session` and `join_session`;
- the portal rule gains three sandbox capability booleans and relaxes the
  prior one-item limit on `privileged_portal_capabilities`; and
- four other schemas inherit
  `device_posture_failure_notification_enabled` from the provider's shared
  policy schema.

The final inherited-field group is not promoted to a support claim. Provider
source reads and expands the field only for access policy and access-policy-v2;
forwarding, redirection, timeout, and the LSS nested policy resource merely
inherit its schema declaration. Their generated inputs remain upstream-schema
surface and require provider-side source or runtime evidence before use.

The portal cardinality change is schema-only, not executable behavior. In
provider `v4.4.9`, `expandPrivilegedPortalCapabilitiesRule` still reads only
`privCapsList[0]` and labels the block `MaxItems: 1`; later configured elements
would be silently ignored. The pack therefore declares
`module_single_blocks: ["privileged_portal_capabilities"]`. Module generation
uses an optional one-element tuple while leaving the checked-in provider schema
byte-exact. A one-element value preserves its capability booleans in the mocked
plan; two-element lists and keyed-object collection bypasses are rejected before
the provider runs. This intentionally changes the old singleton-object module
input to a list-shaped value so Terraform cannot lossily coerce a keyed map into
an empty capability object. Remove the constraint only after provider source
consumes every declared element or the published schema restores its one-item
limit.

The three list-to-set changes alter generated variable/config body types, not
resource instance addresses: none of the affected resources derives
`key_field`, `import_id`, or `sort_lists` identity from those blocks. The full
mock-provider semantics checkpoint verifies the generated module types, while
a credentialed no-op plan remains the final runtime proof.

Only 7 of the 54 registered ZPA resources currently have raw demo fixtures.
`DROPS_CHECK=1` is therefore meaningful for that exercised subset, not a
provider-wide completeness claim. ZTC has no raw fixtures and is deliberately
outside this refresh.

## Findings that constrain adoption

### Import grammar is not uniformly “an ID”

Fourteen resources implement a two-way importer by calling Go
`strconv.ParseInt(id, 10, 64)`: input accepted under that exact signed 64-bit,
explicit-base-10 operation is treated as the object ID, while a parse failure
is interpreted as an alternate lookup key. The matrix grammar is a closed enum
of `base10_numeric_id_or_name`, `base10_numeric_id_or_policy_name`, and
`base10_numeric_id_or_email_id`; suffixes or future variants fail the audit.
The alternate key is normally a name, except:

- `zpa_policy_access_rule` uses a policy name scoped to access/global policy
  types; and
- `zpa_pra_approval_controller` uses an email ID.

Only `zpa_ba_certificate` and `zpa_emergency_access_user` use SDK passthrough
import. Consequently, adoption may treat the current `{id}` registry
value as exact identity for the 14 custom importers only after validating that
it is accepted by `strconv.ParseInt(id, 10, 64)`. Otherwise the provider is
performing a lookup, not importing the supplied bytes as identity.

### `values.id` is not a universal state identity seam

Provider source explicitly writes the schema `id` attribute from Read only for
`zpa_application_segment`. Most custom importers seed that attribute during
import, but three resources have no provider-source assignment to the schema
attribute:

- `zpa_ba_certificate`;
- `zpa_emergency_access_user`; and
- `zpa_inspection_profile`.

The inspection-profile importer writes `profile_id`, which its resource schema
does not declare. This does not prove the three state values are absent at
runtime—the plugin SDK remains part of the execution path—but it does prove a
global source claim such as “every ZPA Read returns `values.id`” is invalid.
The state gate needs per-resource evidence and runtime fixtures rather than
copying the ZCC `values.id` invariant wholesale.

Two other Read paths preserve the current Terraform instance ID instead of
rebinding it from a response:

- app connector group reads the complete list and selects the item whose ID
  equals the current ID; and
- application server fetches by the current ID without comparing or rebinding
  the response ID.

### State projection is materially broader than ZCC

Across the 16 fetched resources, the committed v4.4.9 schema exposes 239 input
attributes and 27 nested input blocks:

| Shape | Count |
|---|---:|
| `string` | 164 |
| `bool` | 47 |
| `set(string)` | 21 |
| `list(string)` | 4 |
| `list(object({from:string,to:string}))` | 2 |
| `map(string)` | 1 |
| nested `list` blocks | 19 |
| nested `set` blocks | 8 |

`zpa_pra_credential_controller` is the only fetched resource with
provider-sensitive input paths: `passphrase`, `password`, and `private_key`.
Its Read function does not restore those values. The existing fail-closed rule
against projecting provider-sensitive inputs must remain intact; this matrix
does not authorize secret synthesis, persistence, or transport.

### Resource-specific source exceptions

The matrix pins these exceptions because they affect registry and adoption
design:

- app connector group uses `GetAll`, selects an item by the current ID, and does
  not rebind the Terraform instance ID from the response;
- application segment converts `policy_style` from enum to boolean and uses
  SDKv2 attribute-as-block port ranges;
- BA certificate Read exposes computed `certificate` but does not restore the
  optional write-side `cert_blob`;
- inspection profile writes undeclared `profile_id` during import and preserves
  `associate_all_controls` from prior state rather than an API response;
- PRA console sets required `pra_application` only when its flattened response
  is non-null;
- PRA portal has a cross-field `CustomizeDiff` exclusion between certificate
  and external-domain fields;
- server group flattens several multi-object reference blocks; and
- service edge group converts the API `is_public` string to a boolean.

These are source facts, not automatic workarounds. Each behavior still needs a
focused compatibility fixture and, where Terraform is involved, a runtime
import/generated-config observation.

## Generated-Config Evidence Gate

All 16 rows are intentionally unqualified for generated configuration. The
next oracle slice should retain the gate until a pinned Terraform run proves,
per resource:

1. the import plan is complete, applyable, and import-only;
2. generated HCL is produced and accepted on the follow-up plan;
3. provider state joins exactly to the requested object without relying on an
   unsupported global `values.id` rule;
4. sensitive values do not enter generated artifacts or diagnostics; and
5. the projected tfvars/import bytes match the committed adoption contract.

Entitlement-optional HTTP statuses in the registry are recorded in the matrix,
but they are collection evidence only. They do not turn an oracle failure into
an optional skip.

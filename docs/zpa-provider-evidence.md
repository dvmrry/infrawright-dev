# ZPA Provider v4.4.6 Evidence

This evidence lane freezes the provider-source facts needed before the Node
adoption oracle can support the 16 fetch-backed ZPA resources. The canonical
machine-readable matrix is
[`evidence/zpa-provider-v4.4.6.json`](evidence/zpa-provider-v4.4.6.json).

It is deliberately narrower than a compatibility claim. Static provider source
can establish import dispatch, Read identity assignments, schema shape, and
sensitivity. It cannot prove Terraform's exact `-generate-config-out` bytes or
whether the generated configuration survives provider validation. Every matrix
row therefore remains `terraform_runtime_evidence_required` for generated
configuration.

## Source Binding

The matrix is bound to:

- `zscaler/terraform-provider-zpa` tag `v4.4.6`;
- commit `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`;
- the complete SHA-256 of every consulted provider source file;
- inclusive, SHA-256-bound line ranges for every import, Read-identity, and
  exception claim; and
- the committed ZPA pack manifest, registry, relevant overrides, and provider
  schema dump.

Run the local-pack audit without an upstream checkout:

```sh
iw zpa-provider-evidence
```

For the source-backed audit, point it at a clean checkout of the pinned tag:

```sh
iw zpa-provider-evidence \
  --provider-root /path/to/terraform-provider-zpa-v4.4.6
```

The audit does not parse Go or infer semantics. It verifies the exact git pin,
complete source bytes, cited source-range bytes, local fetch set, local input
digests, and schema-derived shape summaries. A reviewer still reads the cited
source ranges to decide whether each curated claim is correct.

## Findings That Constrain The Node Port

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
import. Consequently, the Node oracle may treat the current `{id}` catalog
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
The Node state gate needs per-resource evidence and runtime fixtures rather than
copying the ZCC `values.id` invariant wholesale.

Two other Read paths preserve the current Terraform instance ID instead of
rebinding it from a response:

- app connector group reads the complete list and selects the item whose ID
  equals the current ID; and
- application server fetches by the current ID without comparing or rebinding
  the response ID.

### State projection is materially broader than ZCC

Across the 16 fetched resources, the committed v4.4.6 schema exposes 238 input
attributes and 27 nested input blocks:

| Shape | Count |
|---|---:|
| `string` | 164 |
| `bool` | 46 |
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

The matrix pins these exceptions because they affect future catalog or oracle
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
focused Node differential fixture and, where Terraform is involved, a runtime
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
5. the projected tfvars/import bytes match the Python adoption lane.

Entitlement-optional HTTP statuses in the registry are recorded in the matrix,
but they are collection evidence only. They do not turn an oracle failure into
an optional skip.

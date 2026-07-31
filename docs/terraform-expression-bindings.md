# Terraform Expression Bindings

> **Reference tokens (JSON tfvars only).** In JSON-format deployments,
> declared reference fields commit as qualified tokens
> (`"<referent_type>.<key>"`) rather than tenant IDs whenever the
> referent's lookup book knows the ID; HCL-format deployments keep
> literal IDs, and a token-shaped value in an HCL config is refused at
> generation. gen-env renders each token as a
> lookup-first resolver — `try(<remote-state lookup>, <book literal>)` —
> whose fallback reads the committed
> `config/<tenant>/lookups/<referent>.lookup.json` at plan time (a book
> committed at the pre-migration `config/<tenant>/<referent>.lookup.json`
> path still resolves during the migration). IDs live in exactly two
> places: tfstate, and that book. The resolver itself is derived, not
> committed: gen-env recomputes it at render time from the token, the
> pack's declared reference edges, and the provider schema, so no
> `config/<tenant>/<resource_type>.generated.expressions.json` is part of
> the committed surface (a copy still committed from before this changed
> wins unchanged for a tree that has not re-transformed). Per-type,
> committed state is now just the config, the book, and — rarely — a
> hand-written operator overlay. See
> `docs/superpowers/specs/2026-07-30-reference-tokens-design.md` for the
> token contract and
> `docs/superpowers/specs/2026-07-31-sidecar-minimization-design.md` for
> the derivation and migration mechanics; the binding grammar below is
> unchanged and applies to operator-authored sidecars, which are never
> token-wrapped.

Infrawright can bind an exact projected resource path to a Terraform
expression. This is useful when the generated config should refer to a value
owned by Terraform, CI/CD, or a Terraform data source instead of storing a
literal value in generated tfvars.

This is not secret-manager integration. Infrawright does not fetch, store,
inspect, compare, or manage secret values.

## Sidecar File

For tenant `<tenant>` and resource type `<resource_type>`, place bindings next
to the generated tfvars file in the deployment-selected config directory:

```text
[<overlay>/]config/<tenant>/<resource_type>.expressions.json
```

Example:

```json
{
  "resources": {
    "zpa_application_segment.example": {
      "clientless_app_config.password": {
        "expression": "var.zpa_client_secret",
        "sensitive": true,
        "reason": "supplied by CI/CD"
      }
    }
  }
}
```

Then regenerate the env root:

```bash
make gen-env TENANT=<tenant> RESOURCE=<resource_type>
```

The generated root passes `local.infrawright_expression_bound_items` into the
module instead of raw `var.items`.

## Expression Rules

V1 accepts only conservative Terraform expression roots:

- `var.<name>`
- `local.<name>`
- `data.<type>.<name>...`
- `module.<name>...`

Lists may contain supported `data.*` or `module.*` selectors mixed with literal
strings. This is how generated cross-state bindings retain provider/system
sentinels while replacing only managed IDs.

Do not wrap expressions in Terraform interpolation syntax in the binding file.
Use this:

```json
{ "expression": "var.zpa_client_secret" }
```

not this:

```json
{ "expression": "${var.zpa_client_secret}" }
```

The renderer handles syntax for the target format. Native HCL renders the value
unquoted:

```hcl
password = var.zpa_client_secret
```

Terraform JSON syntax renders the value as an interpolation-only string:

```json
"password": "${var.zpa_client_secret}"
```

A literal string such as `"var.zpa_client_secret"` remains a literal string
unless it is supplied through an expression binding.

## Matching Rules

Bindings are exact:

- The resource address must match `<resource_type>.<key>`.
- The path must use dotted object attributes and may use exact canonical numeric
  indexes for ordered lists, for example `server_groups[0].id`.
- The target resource must exist in projected config.
- Intermediate parent objects must exist.
- The target leaf must already exist in projected config.

V1 expression bindings replace existing projected leaves only. Missing leaf
construction is intentionally not supported, and sensitive-required adoption
integration is not implemented in this primitive.

An indexed target must use the exact non-negative base-10 form `[0]`, `[1]`,
and so on. Wildcards (`[]` or `[*]`), identity/key selectors, negative indexes,
quoted indexes, leading-zero forms such as `[01]`, and indexes outside
JavaScript's safe-integer range are rejected. Traversing a list without an
index is also rejected; `server_groups.id` never means
`server_groups[0].id`.

Every binding path is checked against the pinned provider schema for both JSON
and native-HCL tfvars. Indexing an unordered multi-element set is rejected.
Unknown schema paths and invalid selector forms fail before root publication.
For JSON tfvars, the engine additionally verifies that the selected list
element and leaf exist in the concrete projected value. Native-HCL values are
not parsed back into the engine, so an index that is structurally valid but
absent from a particular runtime value fails closed during Terraform validation
or planning.

Numeric indexes deliberately express positional identity. Use them only where
provider/schema evidence establishes stable ordered-list semantics, and
regenerate bindings when list membership changes.

## Variable Declarations

When an expression is exactly `var.<name>`, generated roots include a matching
variable block:

```hcl
variable "zpa_client_secret" {
  type      = string
  sensitive = true
}
```

Generated variables have no default value. Terraform will require them to be
supplied through `TF_VAR_*`, `-var`, tfvars, or another Terraform-compatible
variable mechanism.

The variable is marked `sensitive = true` if any binding for that variable marks
it sensitive.

CI/CD systems can pass the value through Terraform's normal environment
variable convention:

```bash
export TF_VAR_zpa_client_secret="<supplied outside Infrawright>"
```

For Azure DevOps, map secret variables into the task environment explicitly, for
example:

```yaml
steps:
- bash: |
    terraform init
    terraform plan -input=false
  env:
    TF_VAR_zpa_client_secret: $(ZPA_CLIENT_SECRET)
```

Vault, Key Vault, or other systems can be used through Terraform data sources if
the user supplies the data source and binds to its expression, for example:

```json
{
  "expression": "data.vault_kv_secret_v2.zpa_app.data[\"password\"]"
}
```

Infrawright still does not read the secret.

## State Boundary

Terraform provider behavior determines what lands in Terraform state. Marking a
binding as sensitive helps generated variable declarations, but it does not
guarantee secret-free state. State storage, encryption, backend access, and
provider-specific sensitivity behavior remain operator responsibilities.

## Non-Goals

Expression bindings do not:

- call Vault, Key Vault, Azure DevOps, SOPS, 1Password, or any secret manager
- store secret values
- compare secret values
- generate provider data resources
- change projection semantics
- change omission or normalization behavior
- change drift policy
- make arbitrary output changes acceptable to `assert-adoptable`; the one
  exception is the engine-owned cross-state ID output, which is accepted only
  when bound topology and planned provider IDs prove its exact value
- run Terraform/OpenTofu

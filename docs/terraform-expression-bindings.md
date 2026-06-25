# Terraform Expression Bindings

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
- The path must be a dotted object path.
- The target resource must exist in projected config.
- Intermediate parent objects must exist.
- The target leaf must already exist in projected config.

V1 expression bindings replace existing projected leaves only. Missing leaf
construction is intentionally not supported, and sensitive-required adoption
integration is not implemented in this primitive.

V1 rejects list selectors such as `connectors[0].token` or
`connectors[].token`. Add support only after list identity is stable enough to
be safe.

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
- change `assert-adoptable` status behavior
- run Terraform/OpenTofu

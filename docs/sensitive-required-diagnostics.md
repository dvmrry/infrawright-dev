# Sensitive Required Diagnostics

Provider labs can expose sensitive provider-observed state that is also needed
for a valid Terraform configuration. Grafana contact points showed this shape:
the provider marked the selected `webhook` notifier block sensitive, Infrawright
correctly refused to write it into generated tfvars, and Terraform validation
still required one notifier block to be present.

Decide cases from provider-state sensitivity masks, provider schema,
validation failures, and explicit pack policy without emitting secrets. There
is no standalone sensitive-required command. Record sanitized lab evidence as
an item-key-to-path map when a review needs a portable input:

```json
{
  "prod_contact": ["webhook"]
}
```

The classification is diagnostic only. It does not write secrets, generate
placeholders, alter drift policy, or authorize generated HCL.

Important statuses:

- `sensitive_required_validation`: the sensitive path is absent from projected
  config and caller-supplied validation evidence says it is structurally
  required.
- `sensitive_required_schema`: the sensitive path is absent from projected
  config and the Terraform schema marks the exact path as required.
- `sensitive_structural_candidate`: the sensitive path is absent from projected
  config, but static evidence does not prove it is required. Review it before
  production adoption.
- `sensitive_present`: the sensitive path is already present in projected
  config. This is unusual and should be reviewed, but the diagnostic does not
  change behavior.

The command intentionally separates validation-required evidence from
schema-required evidence because provider schemas do not always encode
cross-block structural rules such as "one of these notifier blocks must be
present."

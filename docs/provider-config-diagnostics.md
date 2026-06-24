# Provider Config Diagnostics

Provider labs can expose drift caused by provider-level defaults rather than
resource projection. The GCP lab found this when the Google provider added:

```text
terraform_labels.goog-terraform-provisioned = "true"
```

Setting `add_terraform_attribution_label = false` in provider configuration
removed the drift. That is a provider-config requirement, not a resource
tfvars normalization rule.

This diagnostic is static-only. It reads saved plan JSON and explicit pack
metadata, then reports whether changed paths match a declared provider-config
requirement. It does not render provider config, modify drift policy, change
projection, update `assert-adoptable`, or run Terraform/OpenTofu.

For the guidance metadata and validator contract, see
[Provider Config Requirement Guidance](provider-config-remediation.md).

## Pack Metadata

Declare requirements in `pack.json`:

```json
{
  "provider_config": {
    "requirements": [
      {
        "id": "google_disable_attribution_label",
        "provider": "google",
        "setting": "add_terraform_attribution_label",
        "value": false,
        "reason": "Google provider adds terraform attribution labels by default.",
        "resource_types": [
          "google_bigquery_dataset",
          "google_pubsub_subscription",
          "google_pubsub_topic"
        ],
        "plan_paths": [
          "terraform_labels.goog-terraform-provisioned"
        ]
      }
    ]
  }
}
```

`resource_types` is optional. Use it when a provider setting only explains
drift for specific resources. `resource_prefixes` is also supported for broad
families. If neither is set, the requirement applies to every resource owned by
that provider.

## CLI

Run against a saved plan JSON fixture:

```sh
python -m engine.provider_config \
  --provider google \
  --plan /tmp/infrawright-gcp-lab/plans/google_pubsub_topic.tfplan.json
```

Or infer the provider from a single resource type:

```sh
python -m engine.provider_config \
  --resource-type google_pubsub_topic \
  --plan /tmp/infrawright-gcp-lab/plans/google_pubsub_topic.tfplan.json
```

The report classifies each update path as either:

- `provider_config_requirement`: the path matches explicit pack metadata.
- `unmatched_plan_change`: no provider-config metadata explains the path.

The diagnostic never infers behavior from field names alone. A path such as
`terraform_labels.goog-terraform-provisioned` remains unmatched until a pack
declares the provider setting that explains it.

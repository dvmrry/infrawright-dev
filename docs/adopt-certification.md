# Adopt Certification Advisory

The import-oracle adoption path can intentionally run from key-only inventory:

```text
key/import_id -> Terraform/OpenTofu import -> provider state projection
```

That is the normal adoption path. It avoids treating raw API response bodies as
Terraform configuration truth.

Certification mode is separate. The CLI is a static advisory diff. It requires
raw detail JSON and precomputed oracle/projected fixtures, then compares the
raw API shape against provider-imported state and projected tfvars:

```text
raw detail JSON
  -> raw leaf paths
oracle provider state
  -> provider leaf paths
projected tfvars
  -> projected config leaf paths
drift policy
  -> projection_omit paths
```

This restores the early warning signal from the legacy transform path: raw API
surface that Terraform cannot see or does not project.

## Command

```sh
python -m engine.adopt_certify \
  --resource-type sample_resource \
  --raw raw.json \
  --oracle-state oracle_state.json \
  --projected projected.auto.tfvars.json \
  --policy policy.json
```

The command is fixture-driven. It does not run oracle import, projection, or Terraform plan.
Plan cleanliness comes from `assert-adoptable`, not from the advisory report.

## Inputs

`--raw` may be either a list of raw detail items or a map keyed by stable item
key. When it is a list, the command uses the same adoption metadata key
derivation as `make adopt`.

`--oracle-state` is a map keyed by stable item key. Values use the shape emitted
by the import oracle:

```json
{
  "prod_app": {
    "values": {
      "name": "Prod App"
    },
    "sensitive_values": {}
  }
}
```

`--projected` is normal generated tfvars JSON:

```json
{
  "items": {
    "prod_app": {
      "name": "Prod App"
    }
  }
}
```

`--policy` is optional and uses the same drift-policy format as
`assert-adoptable`.

## Output

The report is pretty JSON on stdout:

```json
{
  "resource_type": "sample_resource",
  "metadata": {
    "mode": "static_advisory_diff",
    "oracle_import": "not_run_by_cli",
    "projection": "not_run_by_cli",
    "terraform_plan": "not_run_by_cli",
    "plan_cleanliness": "not_computed_by_cli_use_assert_adoptable",
    "required_missing": "caller_supplied_not_computed_by_cli",
    "sensitive_blocked": "caller_supplied_not_computed_by_cli"
  },
  "summary": {
    "items": 1,
    "raw_only_paths": 2,
    "provider_only_paths": 1,
    "projected_paths": 3,
    "omitted_by_policy": 1,
    "required_missing": 0,
    "sensitive_blocked": 0
  },
  "items": {
    "prod_app": {
      "raw_only_paths": [
        "cbi_profile.id",
        "security_extra.mode"
      ],
      "provider_only_paths": [
        "provider_default.enabled"
      ],
      "projected_paths": [
        "description",
        "enabled",
        "name"
      ],
      "omitted_by_policy": [
        "metadata.generate_name"
      ],
      "required_missing": [],
      "sensitive_blocked": []
    }
  }
}
```

`raw_only_paths` are advisory by default. They may be API-only metadata,
provider-invisible security surface, or fields intentionally outside Terraform
control. They should be reviewed before production provider adoption.

`required_missing` and `sensitive_blocked` are not computed by this CLI. They
are retained in the report contract for future in-process callers that can
supply projection diagnostics. In CLI-generated reports they default to empty
unless a future caller supplies those diagnostics before report construction.

## Scope Boundary

This PR adds the static advisory harness only. Provider proof runs belong in
later PRs. NetBox, Grafana, Cloudflare, and Zscaler certification should use
this harness after the report contract is stable.

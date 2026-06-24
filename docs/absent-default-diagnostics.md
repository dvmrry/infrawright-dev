# Absent/Default Diagnostics

Provider labs have shown a recurring adoption pattern: a provider can represent
an absent optional API value as a concrete placeholder in imported state or
projected config. Examples from the labs include NetBox empty string/zero
placeholders and Cloudflare singleton defaults such as `hold`,
`hold_after`, and `include_subdomains`.

Use the absent/default diagnostic command to classify these shapes before
deciding whether they should become provider-pack omission guidance, explicit
normalization rules, plan-tolerance policy, or hard adoption blockers.

Classify projected tfvars:

```sh
python -m engine.absent_defaults \
  --resource-type netbox_device \
  --projected netbox_device.auto.tfvars.json
```

Classify a saved Terraform/OpenTofu plan JSON:

```sh
python -m engine.absent_defaults \
  --resource-type cloudflare_zone_hold \
  --plan tfplan.json
```

You can pass both fixtures in one run. The report is diagnostic only. It does
not run projection, normalize values, change drift policy, alter
`assert-adoptable`, or run Terraform/OpenTofu.

Important statuses:

- `absent_default_candidate`: an optional projected config path has a
  placeholder-shaped value such as `""`, `0`, `"0"`, `false`, `[]`, `{}`, or
  `null`.
- `required_placeholder_observed`: a required projected config path has a
  placeholder-shaped value. This is reported separately because automatically
  omitting it would be invalid.
- `absent_default_drift_candidate`: a saved plan update changes an optional path
  where at least one side of the diff is placeholder-shaped.
- `placeholder_update`: a plan update has placeholder-shaped values, but the
  schema path is required, computed-only, or unknown.
- `other_update`: a plan update does not look like absent/default placeholder
  drift.

These statuses are evidence, not remediation. `false`, empty collections, and
`null` are especially context-sensitive, so low-confidence candidates still need
provider/resource review before they become pack-owned behavior.

For the conservative design boundary around future behavior, see
[Absent/Default Normalization Design](absent-default-normalization.md).

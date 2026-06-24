# Absent/Default Diagnostics

Provider labs have shown a recurring adoption pattern: a provider can represent
an absent optional API value as a concrete placeholder in imported state or
projected config. Examples from the labs include NetBox empty string/zero
placeholders and Cloudflare singleton defaults such as `hold`,
`hold_after`, and `include_subdomains`.

Use the absent/default diagnostic command to classify these shapes before
deciding whether they should remain diagnostic/manual-review findings, feed the
existing `projection_omit` path with explicit evidence, or stay hard adoption
blockers. These diagnostics must not become a second omission system or drift
tolerance policy.

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

Any future omit behavior must preserve the existing projection/advisory
accounting described in the normalization design. A placeholder-shaped value by
itself is not enough to omit.

For the conservative design boundary around future behavior, see
[Absent/Default Normalization Design](absent-default-normalization.md).

## Assert-Adoptable Guidance

`make assert-adoptable` uses committed `absent_defaults.rules` as additive
guidance for blocked saved plans. When a blocked plan path exactly matches a
manual-review absent/default rule for the same provider and resource type, the
blocked output includes the rule id, kind, observed value, matched plan path,
reason, and evidence.

This is guidance only. The plan remains blocked. Matching absent/default
metadata is not drift tolerance, does not omit or normalize the value, and does
not change projection, generated config, provider configuration, or
Terraform/OpenTofu execution.

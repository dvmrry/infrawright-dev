# Grafana Provider Lab PR24

## Scope

This lab exercised the PR15 import-oracle adoption path, the PR20 static
certification/advisory report, and the PR21 oracle safety hardening against a
disposable Grafana OSS target.

This is a provider lab report, not a committed Grafana pack. The temporary
pack, schema dump, raw API details, oracle state, projected tfvars, Terraform
roots, state, plans, and logs were kept under `/tmp/infrawright-grafana-lab`
and are not part of this repository.

## Environment

| Component | Value |
|---|---|
| Grafana OSS | `13.0.2` |
| Terraform provider | `registry.terraform.io/grafana/grafana` `v4.39.0` |
| Terraform | `v1.15.4` |
| Lab run prefix | `iw-pr23-20260623a` |
| Live cleanup | completed |

The lab used a temporary `INFRAWRIGHT_PACKS` root with ten Grafana resource
registry entries and the provider schema from `terraform providers schema
-json`. Provider credentials were supplied by environment variables. No remote
backend was used.

The `iw-pr23` run prefix was the disposable Grafana object prefix chosen before
the report PR number was assigned; it is recorded here as factual lab metadata.

## Matrix

| Resource | Seeded | Oracle adopt | Initial plan | Policy omissions | Final plan |
|---|---:|---:|---:|---|---:|
| `grafana_folder` | yes | pass | import-only | none | pass |
| `grafana_dashboard` | yes | pass | import-only | none | pass |
| `grafana_data_source` | yes | failed sensitive projection | not run | `http_headers`, `secure_json_data_encoded` | pass |
| `grafana_annotation` | yes | pass | import-only | none | pass |
| `grafana_playlist` | yes | pass | import-only | none | pass |
| `grafana_library_panel` | yes | pass | import-only | none | pass |
| `grafana_team` | yes | pass | import-only | none | pass |
| `grafana_service_account` | yes | pass | import-only | none | pass |
| `grafana_contact_point` | yes | failed sensitive projection | not run | `webhook` | failed validation |
| `grafana_mute_timing` | yes | pass | import-only | none | pass |

Final gate for the nine resources that produced valid saved plans:

```text
all 9 saved plan(s) clean
```

`grafana_contact_point` did import and refresh through the oracle path, but the
projected config could not produce a valid Terraform plan after the sensitive
`webhook` block was omitted:

```text
"webhook": one of `alertmanager,dingding,discord,email,googlechat,jira,kafka,
line,oncall,opsgenie,pagerduty,pushover,sensugo,slack,sns,teams,telegram,
threema,victorops,webex,webhook,wecom` must be specified
```

## Advisory Summary

The static certification report compared raw Grafana detail JSON,
oracle-imported provider state, projected tfvars, and policy omissions.

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `grafana_folder` | 10 | 1 | 4 | 0 | 0 | 0 |
| `grafana_dashboard` | 43 | 7 | 3 | 0 | 0 | 0 |
| `grafana_data_source` | 10 | 1 | 12 | 2 | 0 | 2 |
| `grafana_annotation` | 7 | 1 | 6 | 0 | 0 | 0 |
| `grafana_playlist` | 3 | 2 | 6 | 0 | 0 | 0 |
| `grafana_library_panel` | 19 | 4 | 5 | 0 | 0 | 0 |
| `grafana_team` | 7 | 2 | 4 | 0 | 0 | 0 |
| `grafana_service_account` | 6 | 0 | 4 | 0 | 0 | 0 |
| `grafana_contact_point` | 5 | 12 | 3 | 0 | 0 | 1 |
| `grafana_mute_timing` | 5 | 2 | 6 | 0 | 0 | 0 |
| **Total** | **115** | **32** | **53** | **2** | **0** | **3** |

Raw-only paths were lower than NetBox, but the mismatch was more about API
surface shape than rich relationship metadata. Representative examples:

| Resource | Raw-only shape | Provider/projection shape |
|---|---|---|
| `grafana_dashboard` | `dashboard.panels[].grid_pos.h`, `meta.can_edit` | `config_json`, `folder`, `org_id` |
| `grafana_library_panel` | `model.options.content`, `model.title` | `model_json`, `folder_uid`, `name` |
| `grafana_mute_timing` | `time_intervals[].times[].start_time` | `intervals[].times[].start` |
| `grafana_data_source` | `basic_auth`, `basic_auth_user`, `with_credentials` | `basic_auth_enabled`, `basic_auth_username`, `url` |
| `grafana_contact_point` | `settings.url`, `type`, `uid` | `webhook[].url`, `webhook[].uid` |

## Findings

The key-only import-oracle model worked for the sampled OSS-local Grafana
surface. All ten resources imported from empty scratch resource stubs and
produced oracle provider state. Nine resources then projected into tfvars,
planned as import-only, and passed `assert-adoptable`.

Grafana is a useful counterpoint to NetBox. NetBox mostly exposed absent-value
placeholder issues; Grafana exposed multiple API/projection translation shapes:

- Dashboard API details are wrapped under `dashboard` and `meta`, while the
  provider writes the dashboard body through `config_json`.
- Library panel API details expose a `model` object, while the provider writes
  `model_json`.
- Mute timing provisioning API uses `time_intervals`, while the provider writes
  `intervals`.
- Data source details use Grafana API naming such as `basicAuth` and
  `basicAuthUser`, while provider state uses Terraform-style names such as
  `basic_auth_enabled` and `basic_auth_username`.
- Contact points use the provisioning API's `{type, settings}` shape, while
  provider state expands the selected notifier into a typed nested block such
  as `webhook`.

The main failure class was sensitive-but-structurally-required provider state.
For `grafana_contact_point`, the provider marks the entire `webhook` block
sensitive. PR15 correctly refuses to write sensitive provider state into
generated tfvars unless policy explicitly omits it. After the lab omitted that
block, Terraform validation still required one notifier block to be present, so
the generated configuration was structurally invalid.

That is not the same problem as NetBox's absent placeholders. It suggests the
adoption engine needs a future diagnostic for sensitive required blocks, or a
provider-specific way to classify which sensitive block fields can be safely
represented, redacted, or intentionally left for a human.

With static sensitive marker derivation, the advisory report should flag the
sensitive provider-observed paths that were absent from projected tfvars. That
does not remediate `grafana_contact_point`: issue #25 remains the adoption
engine work for sensitive blocks that are also required for Terraform
validation.

The PR20 advisory report also exposed a report-semantics gap: a block-level
policy omission such as `webhook` did not appear in `omitted_by_policy` because
the path inventory reports leaf paths such as `webhook[].url`. The projection
policy did take effect, but the advisory report did not count that block-level
omission. Future advisory output should either expand block omissions to
observed leaves or report container-level omissions separately. Issue #26
remains the report work for block/container-level `projection_omit` entries.

## Lab Friction

The first broad plan pass hit local host disk pressure because each temporary
Terraform root attempted to materialize provider data. Re-running with a shared
`TF_DATA_DIR` and `TF_PLUGIN_CACHE_DIR` fixed the issue. Future provider-lab
helpers should set those paths by default.

The temporary pack needed separate key-only adoption fixtures and raw-detail
fixtures. Raw Grafana detail objects are not consistently shaped for stable-key
derivation: some are wrapped responses, some use provisioning IDs, and some use
provider import IDs that differ from API numeric IDs.

## Follow-Ups

- Use the sensitive-required diagnostics command to classify sensitive
  provider-observed blocks that are also required for Terraform validation.
- Teach the advisory report to represent block/container-level
  `projection_omit` entries instead of only leaf-path intersections.
- Consider a provider-lab helper that creates realpath-normalized temporary
  overlays, sets shared Terraform data/plugin-cache paths, captures oracle
  state, and emits the standard matrix.
- Continue with Cloudflare as the next broad provider surface after Grafana.

# Zscaler Adoption Follow-Up Runbook

Use this runbook on the approved work machine to resolve the two open questions
from the live ZIA adoption report:

1. Did Transform and Adopt load the same additive pack overrides?
2. Are source-less resources entering roots automatically, through an explicit
   deployment group, or from a stale generated root?

This is a read-only qualification run. It does not authorize Terraform Apply,
remote object changes, credential inspection, or committing tenant data.

Complete every available step. Mark an unavailable step `NOT RUN` with the
reason instead of substituting a different experiment.

## Safety Rules

- Use the existing approved credential environment without printing it.
- Do not run `env`, `set`, shell tracing, or Terraform debug logging.
- Do not print raw pulls, tfvars, generated HCL, plans, state, object names,
  IDs, URLs, or credentials.
- Report only paths with tenant components redacted, SHA-256 values, resource
  type names, counts, exit statuses, and normalized plan classifications.
- Work only in disposable, job-owned workspaces. Do not reuse the same output
  tree for the Transform and Adopt lanes.
- Do not run Terraform Apply. Retained Oracle scratch data may contain secrets;
  delete it after reporting the sanitized counts.

## Inputs

Set these to the exact files used by the existing clean-room harness:

```sh
export IW_CLI=/absolute/path/to/dist/infrawright-cli.mjs
export IW_PACKS=/absolute/path/to/packs
export IW_PROFILE=/absolute/path/to/packsets/full.json
export IW_CATALOG=/absolute/path/to/packsets/full.json
export IW_DEPLOYMENT=/absolute/path/to/deployment.json
export IW_PULL=/absolute/path/to/the/frozen/pull/root
export IW_TENANT=the-existing-test-tenant
```

Do not report `IW_TENANT` or tenant-bearing path components verbatim.

## Phase 1: Bind The Exact Runtime And Pack Authority

Record:

```sh
git rev-parse HEAD
node --version
terraform version
shasum -a 256 "$IW_CLI"
node -p 'require("fs").realpathSync(process.argv[1])' "$IW_PACKS"
```

The additive override files must be under the exact real path printed above.
Record whether each file exists and its SHA-256:

```sh
shasum -a 256 \
  "$IW_PACKS/zia/overrides/zia_firewall_filtering_network_service.json" \
  "$IW_PACKS/zia/overrides/zia_browser_control_policy.json"
```

Print only the non-sensitive policy fragment:

```sh
jq -c '{drop_if_default,defaults,key_field}' \
  "$IW_PACKS/zia/overrides/zia_firewall_filtering_network_service.json"
jq -c '{drop_if_default,defaults,key_field}' \
  "$IW_PACKS/zia/overrides/zia_browser_control_policy.json"
```

Expected additions include the exact default values under investigation. A
missing file or missing key is a metadata-authority failure, not an Oracle
rewriter failure.

Record the effective profile and deployment hashes without printing them:

```sh
shasum -a 256 "$IW_PROFILE" "$IW_CATALOG" "$IW_DEPLOYMENT"
```

## Phase 2: Reproduce With One Explicit Pack Root

Use the existing clean-room harness to create two disposable workspaces from
the same frozen pull snapshot:

- Workspace T: Transform only.
- Workspace A: Adopt only, with retained Oracle work directories.

Both invocations must explicitly pass the same pack, profile, catalog, and
deployment paths. Do not rely on package-relative defaults.

```sh
node "$IW_CLI" transform \
  --root "$IW_PACKS" \
  --profile "$IW_PROFILE" \
  --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" \
  --in "$IW_PULL" \
  --tenant "$IW_TENANT" \
  --resource zia_firewall_filtering_network_service

INFRAWRIGHT_KEEP_ORACLE=1 node "$IW_CLI" adopt \
  --root "$IW_PACKS" \
  --profile "$IW_PROFILE" \
  --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" \
  --in "$IW_PULL" \
  --tenant "$IW_TENANT" \
  --resource zia_firewall_filtering_network_service
```

Repeat the two commands in separate fresh workspaces for
`zia_browser_control_policy`. Record command exit statuses and the redacted
retained Oracle directory for each Adopt run.

### Count Values Without Printing Tenant Data

For each raw pull, transformed tfvars, and adopted tfvars file, report only
these counts:

```sh
jq '[.. | objects | select(has("tag") and .tag == "")] | length' FILE
jq '[.. | objects | select(has("plugin_check_frequency") and .plugin_check_frequency == "")] | length' FILE
jq '[.. | objects | select(has("end") and .end == 0)] | length' FILE
```

Do not paste the files or matching objects.

For every retained Oracle directory, count the generated HCL lines before and
after policy. A missing `generated.tf.before-policy` means the engine recorded
no generated-config edit and must be reported as `ABSENT`.

```sh
rg -c '^[[:space:]]*tag[[:space:]]*=[[:space:]]*""[[:space:]]*$' \
  REDACTED_ORACLE_DIR/generated.tf.before-policy \
  REDACTED_ORACLE_DIR/generated.tf
rg -c '^[[:space:]]*plugin_check_frequency[[:space:]]*=[[:space:]]*""[[:space:]]*$' \
  REDACTED_ORACLE_DIR/generated.tf.before-policy \
  REDACTED_ORACLE_DIR/generated.tf
rg -c '^[[:space:]]*end[[:space:]]*=[[:space:]]*0[[:space:]]*$' \
  REDACTED_ORACLE_DIR/generated.tf.before-policy \
  REDACTED_ORACLE_DIR/generated.tf
```

Interpret the result as follows:

| Observation | Classification |
|---|---|
| Override key absent from the exact pack root | pack-authority failure |
| Default remains in `generated.tf` | generated-config policy dispatch failure |
| Default removed from generated HCL but present in adopted tfvars | provider-state projection failure |
| Default absent from both but old tfvars still contain it | stale or wrong output workspace |
| Default absent from generated HCL and adopted tfvars | behavior correct; retain as live evidence |

The absence of a field from Transform alone is not proof that the override was
loaded: the raw API may omit a value that provider Read later materializes.

## Phase 3: Classify Root Membership

Generate topology with the same explicit authorities. Select one known
materializable member of each root:

```sh
node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
  --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" --resource zia_url_categories \
  > /tmp/iw-zia-url-roots.json

node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
  --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" --resource zpa_app_connector_group \
  > /tmp/iw-zpa-app-roots.json

node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
  --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" --resource zpa_application_segment \
  > /tmp/iw-zpa-application-roots.json

node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
  --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" --resource zpa_policy_access_rule \
  > /tmp/iw-zpa-policy-roots.json

node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
  --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" --resource zpa_pra_portal_controller \
  > /tmp/iw-zpa-pra-roots.json

node "$IW_CLI" roots --tenant "$IW_TENANT" --root "$IW_PACKS" \
  --profile "$IW_PROFILE" --catalog "$IW_CATALOG" \
  --deployment "$IW_DEPLOYMENT" --resource zpa_service_edge_group \
  > /tmp/iw-zpa-service-roots.json
```

Do not return the raw files because they contain tenant-derived directories.
Return only sanitized topology:

```sh
for file in /tmp/iw-*-roots.json; do
  jq '{resource_roots,roots:[.roots[]|{label,members,provider}]}' "$file"
done
```

Report explicit group membership separately:

```sh
jq '{
  zia_url:(.roots.zia.groups.zia_url // null),
  zpa_app:(.roots.zpa.groups.zpa_app // null),
  zpa_application:(.roots.zpa.groups.zpa_application // null),
  zpa_policy:(.roots.zpa.groups.zpa_policy // null),
  zpa_pra:(.roots.zpa.groups.zpa_pra // null),
  zpa_service:(.roots.zpa.groups.zpa_service // null)
}' "$IW_DEPLOYMENT"
```

Interpretation:

- Automatic `zia_url` must not contain `zia_url_categories_predefined`.
- An explicit `zia_url` group may still contain it because explicit groups
  intentionally override automatic `slug_group:false` metadata.
- Current automatic ZPA slug roots are expected to expose the source-less
  membership defect; report the exact resource type names.
- A generated root on disk that disagrees with the current `roots` output is
  stale. Regenerate it in a disposable workspace before evaluating it.

No Terraform plan is required for Phase 3.

## Report Template

Return one sanitized report using this structure:

```text
Zscaler adoption follow-up

Authority
- repository commit:
- Node version:
- Terraform version:
- CLI SHA-256:
- pack root real path: <tenant-independent path or REDACTED>
- network-service override: PRESENT/MISSING, SHA-256
- browser-control override: PRESENT/MISSING, SHA-256
- profile/catalog/deployment SHA-256:

Field pipeline
Resource | Raw default count | Transform count | Generated before | Generated after | Adopt tfvars count | Exit/result
network service tag | | | | | |
network service end=0 | | | | | |
browser plugin frequency | | | | | |

Root topology
Root | Explicit or automatic | Current members | Source-less members | Generated root matched topology?
zia_url | | | | |
zpa_app | | | | |
zpa_application | | | | |
zpa_policy | | | | |
zpa_pra | | | | |
zpa_service | | | | |

Classification
- pack authority:
- generated-config policy:
- provider-state projection:
- stale output:
- ZIA topology:
- ZPA topology:

Unavailable steps
- step:
- reason:

Safety confirmation
- no Terraform Apply
- no remote writes
- no credentials or tenant objects printed
- retained Oracle directories deleted after sanitized counts
```

Do not generalize beyond the observed resource types. Return `INCONCLUSIVE`
when the exact pack root or output workspace cannot be proved.

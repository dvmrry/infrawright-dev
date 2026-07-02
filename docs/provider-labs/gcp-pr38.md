# Google Cloud No-Billing Provider Lab

Historical report file: `gcp-pr38.md`.

Date: 2026-06-24
Provider: `hashicorp/google` `v7.38.0`
Terraform: `v1.15.4`
Target: disposable Google Cloud project `<disposable-gcp-project>`

This lab exercised the import-oracle adoption path and static advisory report
against a disposable Google Cloud project. This is a provider lab report, not a
committed Google Cloud pack. Temporary packs, schemas, raw REST details, oracle
state, projected tfvars, Terraform roots, state, plans, and logs were kept in
an uncommitted temporary lab root and are not part of this repository.

## Summary

| Outcome | Count |
|---|---:|
| Resources attempted | 11 |
| Seeded live resources | 5 |
| Oracle import/read succeeded | 5 |
| Adopted config generated | 5 |
| Initial import-only/adoptable plan | 2 |
| Initial blocked by provider-label drift | 3 |
| Final import-only/adoptable plan | 5 |
| Required missing (caller-supplied diagnostics) | 0 |
| Sensitive blocked | 0 |

Static advisory totals for the five import-only/adoptable resources:

| Advisory bucket | Count |
|---|---:|
| Items | 5 |
| Projected paths | 48 |
| Raw-only paths | 19 |
| Provider-only paths | 23 |
| Omitted by policy | 0 |
| Required missing (caller-supplied diagnostics) | 0 |
| Sensitive blocked | 0 |

Cleanup: Terraform destroy completed for all tracked objects. Separate REST
verification found the BigQuery dataset, Pub/Sub topic/subscription, and service
account missing. The custom IAM role remained addressable only as a soft-deleted
role with `deleted=true`, which is normal Google Cloud behavior.

## Credential Safety

Credentials came from a disposable service account JSON outside the repository.
The key file was not copied into the repo or committed. No non-lab Google
credential was detected in the shell. There was no `gcloud` binary available, so
raw REST capture and cleanup verification used short-lived OAuth access tokens
minted from the disposable service account key.

## Environment

| Component | Value |
|---|---|
| Product | Google Cloud disposable project |
| Terraform provider | `registry.terraform.io/hashicorp/google` `v7.38.0` |
| Terraform | `v1.15.4` |
| Lab run prefix | `iw-gcp-lab-20260624a` |
| Temporary root | uncommitted temporary lab root |
| Live cleanup | completed |

The lab used a temporary `INFRAWRIGHT_PACKS` root with five Google Cloud
resource registry entries and the provider schema from `terraform providers
schema -json`. Provider credentials were supplied by environment variables. No
remote backend was used.

Cloud Resource Manager was initially disabled. The lab enabled it through the
Service Usage REST API so Terraform could refresh `google_project_service`
resources. `bigquery.googleapis.com`, `iam.googleapis.com`,
`pubsub.googleapis.com`, and `storage.googleapis.com` were enabled as
prerequisites and left enabled because the temporary `google_project_service`
resources used `disable_on_destroy=false`.

## Matrix

| Resource | Seeded | Raw detail | Oracle import | Adopt/project | Initial plan | Policy omissions | Final plan | Cleanup |
|---|---|---|---|---|---|---|---|---|
| `google_bigquery_dataset` | yes | REST dataset detail | pass | pass | blocked: provider attribution label | none | pass with provider config override | verified |
| `google_project_iam_custom_role` | yes | REST role detail | pass | pass | import-only | none | pass | soft-deleted |
| `google_pubsub_subscription` | yes | REST subscription detail | pass | pass | blocked: provider attribution label | none | pass with provider config override | verified |
| `google_pubsub_topic` | yes | REST topic detail | pass | pass | blocked: provider attribution label | none | pass with provider config override | verified |
| `google_service_account` | yes | REST service account detail | pass | pass | import-only | none | pass | verified |
| `google_storage_bucket` | failed billing gate | not captured | not run | not run | not run | none | not run | not created |
| `google_secret_manager_secret` | API billing gate | not captured | not run | not run | not run | none | not run | not created |
| `google_secret_manager_secret_version` | API billing gate | not captured | not run | not run | not run | none | not run | not created |
| `google_compute_network` | API billing gate | not captured | not run | not run | not run | none | not run | not created |
| `google_compute_subnetwork` | API billing gate | not captured | not run | not run | not run | none | not run | not created |
| `google_compute_firewall` | API billing gate | not captured | not run | not run | not run | none | not run | not created |

Final gate:

```text
all 5 saved plan(s) clean
```

Each final saved plan contained exactly one import action and no updates,
creates, or deletes.

## Advisory Summary

| Resource | Raw-only | Provider-only | Projected | Omitted by policy | Required missing | Sensitive blocked |
|---|---:|---:|---:|---:|---:|---:|
| `google_bigquery_dataset` | 7 | 4 | 19 | 0 | 0 | 0 |
| `google_project_iam_custom_role` | 2 | 2 | 7 | 0 | 0 | 0 |
| `google_pubsub_subscription` | 4 | 7 | 11 | 0 | 0 | 0 |
| `google_pubsub_topic` | 3 | 6 | 5 | 0 | 0 | 0 |
| `google_service_account` | 3 | 4 | 6 | 0 | 0 | 0 |
| **Total** | **19** | **23** | **48** | **0** | **0** | **0** |

Representative raw-only/provider-only shapes:

| Resource | Raw/API shape | Provider/projection shape |
|---|---|---|
| `google_bigquery_dataset` | `dataset_reference.dataset_id`, `labels.*`, `kind`, `type` | `dataset_id`, `project`, `effective_labels.*`, `terraform_labels.*` |
| `google_project_iam_custom_role` | `included_permissions[]`, `etag` | `permissions[]`, `deleted`, `id` |
| `google_pubsub_subscription` | `labels.*`, `state` | `effective_labels.*`, `expiration_policy[].ttl`, `tags`, `timeouts` |
| `google_pubsub_topic` | `labels.*` | `effective_labels.*`, `tags`, `timeouts` |
| `google_service_account` | `oauth2_client_id`, `etag`, `project_id` | `member`, `id`, `timeouts` |

## Findings

The key-only import-oracle model worked for the Google Cloud surface that could
be seeded without billing. All five seeded resources imported from empty
scratch stubs, projected provider state, staged import blocks, and produced
import-only saved plans after applying a Google provider configuration override.

The main Google-specific finding is provider attribution-label drift. With the
default provider configuration, three resources planned an update from an empty
`terraform_labels` map to:

```text
terraform_labels.goog-terraform-provisioned = "true"
```

Setting `add_terraform_attribution_label = false` in the lab provider config
removed that drift and made all five plans clean. This is not a resource
projection problem. It should be handled as Google pack/provider-config
metadata or documented consumer provider configuration, not as a broad drift
tolerance rule.

Google Cloud also added a no-billing BigQuery wrinkle. The disposable project
had no billing account, and BigQuery populated `default_table_expiration_ms` and
`default_partition_expiration_ms` with `5184000000` milliseconds. Terraform
wanted to clear those values in the seed root until the lab pinned the observed
defaults. The import-oracle adoption path preserved those observed values in
projected tfvars, so the final adopted plan did not hit the BigQuery billing
validation error.

The billing boundary was material. Enabling `compute.googleapis.com` and
`secretmanager.googleapis.com` failed because the project had no billing
account. Creating an empty Cloud Storage bucket also failed because the owning
project had no active billing account. Those are provider/product boundaries,
not import-oracle failures.

The advisory report exposed normal Google API/provider shape differences:
labels split across raw `labels`, provider `effective_labels`, and provider
`terraform_labels`; custom roles use API `includedPermissions` but Terraform
`permissions`; BigQuery REST uses `datasetReference`, while Terraform config
uses `dataset_id` and `project`.

## Lab Friction

`gcloud` was not installed in this environment. The lab used Terraform for
seeding/import planning and direct REST calls for raw detail capture and cleanup
verification.

The first seed pass tried a broader surface and partially created
`google_project_service` state before failing on billing and disabled API
preconditions. The lab then enabled Cloud Resource Manager, reduced the surface
to no-billing resources, and kept all live objects lab-prefixed.

The local shell was `zsh`, whose default scalar splitting differed from the
bash-style loop used in earlier lab snippets. One local adoption loop initially
treated the resource list as a single filename. It had no cloud impact and was
rerun with bash semantics.

## Cleanup Verification

```text
Terraform destroy completed for the five seeded objects and four tracked
project-service prerequisite records. A separate REST verification checked:

- IAM service account GET -> 404 NOT_FOUND
- BigQuery dataset GET -> 404 NOT_FOUND
- Pub/Sub topic GET -> 404 NOT_FOUND
- Pub/Sub subscription GET -> 404 NOT_FOUND
- IAM custom role GET -> present with deleted=true

The seed Terraform state was empty after destroy.
```

## Validation

```text
terraform validate passed for the seed root.
terraform apply created the reduced five-resource no-billing surface.
oracle import/projection passed for 5/5 resources.
initial assert-adoptable blocked 3/5 on terraform_labels.goog-terraform-provisioned.
with add_terraform_attribution_label=false, assert-adoptable reported:
all 5 saved plan(s) clean
REST cleanup verification passed as described above.
```

Repository validation passed:

```text
make check
git diff --check
```

## Provider-Config Guidance Negative Retest

Date: 2026-06-24
Commit: `f11dadc`

A live retest was run after provider-config `assert-adoptable` guidance
annotations were implemented. The retest did not validate the annotation
because the historical attribution-label drift no longer reproduced on current
main/provider flow.

Result:

- The narrow GCP subset was seeded: `google_bigquery_dataset`,
  `google_pubsub_topic`, and `google_pubsub_subscription`.
- The seed used `add_terraform_attribution_label = false`.
- Import-oracle/adoption flow succeeded.
- Initial saved plans were import-only and `assert-adoptable` reported clean.
- After applying import-only plans to local state, unstaging imports, and
  replanning, post-import saved plans were still clean.
- `assert-adoptable` reported all saved plans clean.
- No blocked plan path existed for
  `terraform_labels.goog-terraform-provisioned`.
- Therefore the provider-config guidance annotation did not have a live blocked
  path to attach to.

Additional attempted reproduction:

- `GOOGLE_ADD_TERRAFORM_ATTRIBUTION_LABEL=true`
- explicit temporary tfvars value for
  `terraform_labels.goog-terraform-provisioned`

Both attempts still produced clean plans. `terraform_labels` is computed-only
for the tested resources, and generated modules do not render it as input.

Sanitized output excerpt:

```text
post-import assert-adoptable:
all 3 saved plan(s) clean

forced attribution env assert-adoptable:
all 3 saved plan(s) clean

forced terraform_labels tfvars assert-adoptable:
all 3 saved plan(s) clean
```

Conclusion: the historical GCP attribution-label drift documented in this lab is
not currently reproducible on current main/provider flow. This lab remains
useful historical evidence for the provider-config failure class, but it should
not be treated as current live validation for provider-config
`assert-adoptable` guidance annotations.

Cleanup:

- Terraform destroy completed for the three seeded retest resources.
- The retest worktree was clean after cleanup.
- No logs, state, plans, credentials, project IDs, local temp roots, generated
  tfvars/import blocks, or provider artifacts were committed.

## Follow-Ups

- `pack:provider-config`: use provider-config diagnostics for
  `add_terraform_attribution_label = false` and classify Google attribution
  label drift against explicit pack metadata.
- `provider-boundary:paid-or-disabled`: repeat the lab with billing enabled to
  cover Compute, Secret Manager, Cloud Storage, and other billable APIs.

## Metadata Classification

The `add_terraform_attribution_label = false` provider-config requirement is now
recorded in `packs/google/pack.json` under `provider_config.requirements` with
`required_external` mode. It cites this lab report and targets the three
resources that drifted on `terraform_labels.goog-terraform-provisioned`.

Provider-config `assert-adoptable` guidance annotations are implemented as
additive, annotation-only output and documented in
`docs/provider-config-assert-guidance.md`. The historical GCP drift in this lab
is not current live validation for that guidance path; the guidance still does
not render or mutate provider configuration.
- `pack:identity-alias`: keep explicit identity/import metadata for Google
  resources where REST, import ID, and Terraform state names diverge, starting
  with BigQuery dataset `datasetReference.datasetId` to
  `projects/<project>/datasets/<dataset_id>`.
- `engine:absent-defaults`: use absent/default diagnostics on Google defaults
  such as BigQuery no-billing expiration values before deciding whether any
  normalization belongs in a future Google pack.
- `lab-harness`: consider a small REST helper for providers where a CLI such as
  `gcloud` is unavailable but raw API detail is still needed for advisory
  reports.

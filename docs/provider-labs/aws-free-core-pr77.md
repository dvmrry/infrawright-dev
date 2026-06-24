# AWS Free/Core Provider Lab

Date: 2026-06-24

Commit tested: `296890b` (`Fix provider-config guidance metadata validation`)

Terraform: `v1.15.4`

Provider: `hashicorp/aws v6.52.0`

Region: `us-east-1`

Scope: disposable AWS resources only. No credentials, state, plans, raw logs,
account IDs, ARNs, provider output dumps, or local temp paths are committed.

## Goal

Exercise the import-oracle adoption loop against real AWS resources before
revoking a temporary AWS key:

```text
seed resource
  -> key-only pull identity
  -> import oracle
  -> provider-state projection
  -> saved Terraform plan
  -> assert-adoptable
```

The secondary goal was to look for a current provider-config drift case that
could live-validate provider-config `assert-adoptable` guidance annotations.

## Resources Tested

| Resource | Why | Live result |
|---|---|---|
| `aws_cloudwatch_log_group` | Simple scalar/tag resource and provider-config tag probe | Import/adopt worked. Default-tags and ignore-tags probes did not produce a provider-config blocked path. |
| `aws_s3_bucket` | Global-ish identity, tag surfaces, computed/deprecated AWS surfaces | Import/adopt worked. Default-tags probe did not produce provider-config drift after unrelated placeholder omission. |
| `aws_iam_role` | JSON assume-role policy canonicalization | Import/adopt worked. JSON policy re-planned clean after unrelated `name_prefix` placeholder omission. |
| `aws_iam_policy` | JSON policy canonicalization and ARN import identity | Import/adopt worked. JSON policy re-planned clean after unrelated `name_prefix` placeholder omission. |
| `aws_security_group` | VPC-scoped identity, empty ingress/egress lists, set-ish provider behavior | Import/adopt worked. Empty ingress/egress lists re-planned clean after unrelated `name_prefix` placeholder omission. |

The account had no default VPC in `us-east-1`, so the security group test used
a disposable VPC created and destroyed by the seed root.

## Provider-Config Probes

### `default_tags`

`default_tags` was tested with:

- `aws_cloudwatch_log_group`
- `aws_s3_bucket`

Both resources read the provider default tag back into provider-observed
resource state. The projected config included the tag in `tags` and `tags_all`.
After unrelated AWS placeholder paths were omitted for the lab, both resources
planned as import-only and `assert-adoptable` reported clean.

Result: no provider-config guidance validation case.

### `ignore_tags`

`ignore_tags` was tested with a CloudWatch log group:

```hcl
provider "aws" {
  region = "us-east-1"

  ignore_tags {
    keys = ["iw-ignore-me"]
  }
}
```

The seed apply showed the expected provider behavior: the configured tag was
present in `tags`, while provider `tags_all` omitted the ignored key. Importing
the same remote object through the normal oracle provider without `ignore_tags`
read the ignored key back into both `tags` and `tags_all`.

After lab-only omission of unrelated empty placeholders, the saved plan was
import-only and `assert-adoptable` reported clean.

Result: no provider-config guidance validation case.

## Observed AWS Quirks

See [AWS Absent/Default Placeholder Classification](../aws-absent-default-classification.md)
for the design split between mutually-exclusive prefix conflicts and absent
optional reference placeholders.

### Empty Prefix Placeholders

Several AWS resources import empty string prefix fields even when the explicit
name field is set. Terraform then rejects the generated config because the
fields conflict:

| Resource | Conflicting placeholder |
|---|---|
| `aws_cloudwatch_log_group` | `name_prefix = ""` |
| `aws_s3_bucket` | `bucket_prefix = ""` |
| `aws_iam_role` | `name_prefix = ""` |
| `aws_iam_policy` | `name_prefix = ""` |
| `aws_security_group` | `name_prefix = ""` |

The lab used temporary consumer-owned `projection_omit` entries to remove only
those placeholder paths so deeper provider behavior could be evaluated. This is
an absent/default placeholder class, not provider-config guidance. These prefix
fields are specifically mutually-exclusive field conflict candidates: rendering
both a concrete identity field such as `name` or `bucket` and an empty prefix
field creates invalid config.

`aws_cloudwatch_log_group` also imported `kms_key_id = ""` for an unencrypted
log group; that path was omitted in the lab for the same reason. Unlike the
prefix fields, `kms_key_id` is an absent optional reference candidate, not a
mutually-exclusive identity-prefix conflict.

### Deprecated AWS Surfaces

The generated S3 and IAM role modules referenced deprecated provider surfaces:

- `aws_s3_bucket` legacy inline nested surfaces such as `grant`,
  `server_side_encryption_configuration`, and related outputs.
- `aws_iam_role.inline_policy`.

These produced warnings, not blocked plans, in this lab. They should be treated
as provider-readiness/adoption-surface evidence, not provider-config drift.

### JSON Canonicalization

Both JSON-heavy IAM resources planned clean after the unrelated prefix
placeholder omission:

- `aws_iam_role.assume_role_policy`
- `aws_iam_policy.policy`

This is a useful positive signal that provider-normalized JSON strings can flow
through import oracle projection and back into Terraform without immediate
canonicalization drift for these simple policies.

## Outcome Matrix

| Resource | Import oracle | Projection | Natural plan | Lab-only placeholder omit | Final saved plan | `assert-adoptable` |
|---|---:|---:|---|---|---|---|
| `aws_cloudwatch_log_group` (`default_tags`) | pass | pass | blocked by empty placeholders | `name_prefix`, `kms_key_id` | import-only | clean |
| `aws_s3_bucket` (`default_tags`) | pass | pass | blocked by empty placeholder | `bucket_prefix` | import-only | clean |
| `aws_iam_role` | pass | pass | blocked by empty placeholder | `name_prefix` | import-only | clean |
| `aws_iam_policy` | pass | pass | blocked by empty placeholder | `name_prefix` | import-only | clean |
| `aws_security_group` | pass | pass | blocked by empty placeholder | `name_prefix` | import-only | clean |
| `aws_cloudwatch_log_group` (`ignore_tags`) | pass | pass | blocked by empty placeholders | `name_prefix`, `kms_key_id` | import-only | clean |

## Classification

This lab produced no provider-config blocked plan path suitable for live
validating provider-config `assert-adoptable` guidance annotations.

The main failure class was AWS provider absent/default placeholder drift:

- empty `name_prefix`
- empty `bucket_prefix`
- empty `kms_key_id`

Those paths are candidates for the absent/default design track, not
provider-config metadata. They must not be globally normalized based only on
falsey value shape.

## Follow-Up Metadata

AWS absent/default pack metadata has been added as manual-review-only rules
citing this lab evidence. The rules make the observed placeholder findings
inventory-visible and validator-backed, but they do not change projection,
omission, drift policy, `assert-adoptable`, provider configuration, or
Terraform/OpenTofu execution.

## Cleanup

Cleanup completed successfully:

- CloudWatch log groups destroyed.
- S3 bucket destroyed.
- IAM role destroyed.
- IAM policy destroyed.
- Disposable VPC destroyed.
- Security group destroyed.

The temporary AWS key should be revoked after this lab. The local scratch
directory was removed after extracting this sanitized report.

## Follow-Ups

- Do not add AWS provider-config metadata from this lab.
- Do not mark provider-config guidance annotations as live-lab validated.
- Track AWS empty-prefix placeholder behavior under the absent/default
  normalization design, with per-resource evidence and runtime discriminators.
- Use the AWS absent/default classification design to keep the committed
  manual-review prefix-conflict metadata separate from absent optional reference
  placeholders before proposing any behavior.
- Consider provider-readiness metadata for deprecated inline AWS surfaces before
  attempting broad AWS pack support.

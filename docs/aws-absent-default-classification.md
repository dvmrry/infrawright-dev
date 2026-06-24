# AWS Absent/Default Placeholder Classification

This is a design note, not implemented behavior. It classifies the AWS
absent/default findings from the AWS free/core provider lab before any AWS pack
metadata is committed.

Evidence: [AWS Free/Core Provider Lab](provider-labs/aws-free-core-pr77.md)

## Problem Statement

The AWS provider can import empty-string fields that become invalid or noisy
when projected literally into generated config. The AWS lab observed these
paths:

| Resource | Path | Observed value |
|---|---|---|
| `aws_cloudwatch_log_group` | `name_prefix` | `""` |
| `aws_cloudwatch_log_group` | `kms_key_id` | `""` |
| `aws_s3_bucket` | `bucket_prefix` | `""` |
| `aws_iam_role` | `name_prefix` | `""` |
| `aws_iam_policy` | `name_prefix` | `""` |
| `aws_security_group` | `name_prefix` | `""` |

These findings are absent/default candidates, not provider-config guidance. The
same lab tested AWS `default_tags` and `ignore_tags`, but neither produced a
current provider-config blocked plan path.

## Safety Boundary

Empty string alone is not a discriminator. No behavior may omit, drop, or
rewrite a path only because its value is `""`.

This design does not authorize:

- AWS absent/default pack metadata.
- Validator changes.
- Projection changes.
- Omission behavior.
- Drift-policy changes.
- `assert-adoptable` status changes.
- Terraform/OpenTofu execution.
- Global falsey normalization.

Any future AWS rule must be provider/resource/path-specific, cite committed lab
evidence, and remain manual-review-only until a later behavior design proves a
runtime discriminator and routes omission through the existing
`projection_omit` path.

## Failure Shape A: Mutually-Exclusive Empty Prefix Conflict

Observed paths:

- `aws_cloudwatch_log_group.name_prefix = ""`
- `aws_s3_bucket.bucket_prefix = ""`
- `aws_iam_role.name_prefix = ""`
- `aws_iam_policy.name_prefix = ""`
- `aws_security_group.name_prefix = ""`

Shape:

- Provider state includes an empty prefix field.
- A concrete identity/name sibling is also present, such as `name` or `bucket`.
- Rendering both fields creates invalid Terraform config because the fields are
  mutually exclusive.
- The safety argument is not "empty string is falsey." The safety argument is
  about a provider-returned empty prefix conflicting with a concrete identity
  field that already owns the remote object identity.

Future behavior would need a runtime discriminator proving all of the
following for the current object:

- The prefix field is exactly `""`.
- The concrete sibling field is present and non-empty.
- The concrete sibling field is the identity used for import/adoption.
- The prefix field was not user-owned intent.
- Omitting the prefix does not change the remote identity or create a rename.

### Provisional Kind

This PR does not settle the V1 kind. There are two viable contract paths:

- Reuse `provider_absent_placeholder` and document this as a mutually-exclusive
  prefix-conflict variant.
- Add a new kind such as `provider_absent_conflicting_placeholder`.

If the new kind is chosen, the existing absent/default validator does not accept
it today. A future contract PR must add the kind before AWS metadata can use it.

## Failure Shape B: Absent Optional Reference Placeholder

Observed path:

- `aws_cloudwatch_log_group.kms_key_id = ""`

Shape:

- Provider state includes an empty optional reference field for an unencrypted
  CloudWatch log group.
- This is not a mutually-exclusive prefix/name conflict.
- It likely represents "no KMS key configured."
- The safety argument differs from prefix conflicts because a future encrypted
  log group must remain visible as drift, not be hidden by an empty-value rule.

Future behavior would need evidence proving all of the following:

- AWS/provider semantics treat `kms_key_id = ""` as absent for an unencrypted
  log group.
- No configured KMS key is being erased.
- Absence can be distinguished from user-owned configuration.
- A remote log group encrypted with a real KMS key appears as a non-empty value
  and still produces visible drift if config does not match.

### Provisional Kind

This can likely remain under `provider_absent_placeholder`, with documentation
that it is an absent optional reference variant. If a more precise
absent-reference kind is proposed later, that requires a separate validator
contract PR before metadata can use it.

## V1 Action Stance

For any future AWS metadata based on this evidence, the only acceptable V1
action is:

- `manual_review_required`

Do not use or introduce:

- `omit_when_absent_in_api`
- `omit_when_provider_placeholder`
- `drop_empty_values`
- `drop_falsey`
- `normalize_defaults`

The existing absent/default design reserves omit actions for a future runtime
discriminator design. AWS placeholder findings do not change that boundary.

## Evidence Requirements Before Metadata

Before committing AWS absent/default metadata, a follow-up PR must prove the
relevant shape per resource/path. The metadata PR should cite committed evidence
and keep action as `manual_review_required`.

### Prefix Conflict Paths

Required evidence:

- Exact resource type.
- Exact prefix path.
- Exact conflicting concrete sibling field, such as `name` or `bucket`.
- Observed imported provider-state value.
- Natural generated-config conflict from rendering both fields.
- Clean plan after lab-only omission of the prefix field.
- Proof the concrete field is present and non-empty.
- Proof the concrete field is the import/adoption identity.
- Reason why no user intent or remote identity is lost.

### `kms_key_id`

Required evidence:

- Exact resource type.
- Observed imported provider-state value.
- Generated-config effect.
- Clean plan after lab-only omission.
- Proof the remote log group is unencrypted or has no KMS key configured.
- Proof a real KMS key appears as a non-empty provider-state value.
- Reason why future remote encryption would stay visible rather than being
  masked.

## Illustrative Future Metadata

This is non-binding and is not committed by this PR.

```json
{
  "id": "aws_cloudwatch_log_group_empty_name_prefix",
  "provider": "aws",
  "resource_type": "aws_cloudwatch_log_group",
  "path": "name_prefix",
  "observed_value": "",
  "kind": "provider_absent_conflicting_placeholder",
  "action": "manual_review_required",
  "reason": "AWS provider imports empty name_prefix alongside concrete name; rendering both creates an invalid conflict.",
  "evidence": "docs/provider-labs/aws-free-core-pr77.md"
}
```

If `provider_absent_conflicting_placeholder` is used, a future validator
contract PR must add that kind first. The current validator contract does not
accept it.

The same metadata could instead use `provider_absent_placeholder` if the
contract chooses to represent prefix conflicts as a documented variant of that
existing kind.

## Recommended Next Step

Settle the kind/contract choice before adding AWS metadata. The next safe PR is
metadata-only and manual-review-only after the contract can represent the AWS
prefix-conflict shape without implying omit behavior.

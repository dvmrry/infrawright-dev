# Integration Validation Report

## Summary

| Field | Value |
|---|---|
| Date |  |
| Provider / pack |  |
| Tenant / environment |  |
| Resource scope |  |
| Result |  |

## Versions

| Component | Version |
|---|---|
| Infrawright commit |  |
| Terraform/OpenTofu |  |
| Terraform provider |  |
| Pack |  |

## Command Sequence

```sh
make fetch TENANT=<tenant> RESOURCE=<resource-or-provider>
make adopt IN=pulls/<tenant> TENANT=<tenant> RESOURCE=<resource-or-provider>
make gen-env TENANT=<tenant> RESOURCE=<resource-or-provider>
make stage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
make plan TENANT=<tenant> RESOURCE=<resource-or-provider> SAVE=1
make assert-adoptable TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
make apply TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
```

## Plan Summary

| Resource | Plan result | Blocked paths | Tolerated paths | Notes |
|---|---|---|---|---|
|  |  |  |  |  |

## Performance Evidence (when enabled)

| Run | Phase | Resource family | Instances | HTTP requests | Terraform commands | Duration (ms) |
|---|---|---|---:|---:|---:|---:|
|  |  |  |  |  |  |  |

- Fetch concurrency:
- HTTP 429 count / retry delay:
- Artifact manifest SHA-256:
- Baseline manifest match:
- Fixture-only or live work-machine evidence:

## Generated-Config Projection Timing

- Resource(s) exercised:
- Optional-zero sentinel paths tested, for example `size_quota = 0` or `end = 0`:
- Projection policy entries expected to apply before provider validation:
- Result:
- Dependent tests or binding proofs still blocked:

## Policy Used

- Policy path:
- Relevant entries:
- Approval/ticket references:

## Generated Artifacts

- Pull artifacts:
- Config artifacts:
- Import/move artifacts:
- Env roots:
- Saved plans:
- State/backend location:

## Failure Classification

| Category | Evidence | Owner | Next action | Blocks validation? |
|---|---|---|---|---|
|  |  |  |  |  |

## Evidence Links / Paths

- Sanitized output:
- Saved plan classification summary:
- Provider/API evidence:
- Related docs/issues/PRs:

## Redaction Notes

- Credentials:
- Tenant/account/project identifiers:
- URLs/hostnames:
- ARNs/resource IDs:
- Local paths:
- State/plans/logs:

## Cleanup

- Import blocks unstaged:
- Plans cleaned:
- Live resources destroyed or retained by approval:
- Keys/tokens revoked:
- Worktree status:

## Next Action

- 

# ADR 0001: Pipeline-Owned Publication Roots

- Status: Accepted
- Date: 2026-07-11

## Context

InfraWright is a machine-only pipeline tool. Azure DevOps jobs may run
concurrently, including two runs of the same Git branch. Branch identity does
not provide filesystem isolation; physical workspace and artifact paths do.

Node bootstrap and refresh publication update generated config and import
artifacts beneath one host-selected `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT`.
Terraform plan/apply commands also mutate their env roots. The publication code
can detect many mid-operation filesystem changes, but those checks do not make
competing writers composable: a detected race aborts, and a refresh replacement
still has a final rename or unlink syscall window. The pending-move marker is a
durable refresh-state fence, not a general publisher lock.

Self-hosted agents may retain files between sequential jobs. Hosted jobs are
ephemeral; self-hosted agents must have distinct work folders. Neither fact
permits two jobs to publish into the same physical output root.

## Decision

### Ownership unit

The complete canonical `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` has exactly one
publisher at a time. It must equal the common artifact-layout authority derived
from the resolved config, import, and applicable lookup targets. A containing
ancestor is not an equivalent authority and fails before artifact mutation with
`OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY`. The unit is deliberately not a resource,
tenant, config directory, or individual file. One publication can span config
and import directories, and resource transforms can consume lookup sidecars
produced in reference order.

Persistent Node mutations are:

- bootstrap `materialize_pull_artifacts`;
- refresh `materialize_pull_artifacts`;
- `acknowledge_pull_refresh`.

Each process-host mutation acquires the root-level
`.infrawright.publisher.lock` with exclusive, no-follow creation and fails
immediately with `OUTPUT_ROOT_BUSY` if that pathname already exists. It never
waits, and guard acquisition never removes a pre-existing lock. The guard is a
transient host coordination file at the output-root boundary; it is not a
generated artifact, refresh marker, parity input, or schema field.

The host retains open handles and device/inode bindings for both the authority
directory and its guard. Before cleanup it rechecks both handles against their
current pathnames and refuses to unlink a replaced guard or one reached through
a replaced root. This closes deterministic replacement and root-rollover
failures under the trusted single-writer model. Portable Node still cannot make
the final pathname recheck plus `unlink` indivisible, so this is not a security
boundary against a hostile same-UID process racing that final syscall.

The guard covers one Node process invocation. The pipeline-owned workspace is
the broader guarantee spanning Python transforms, Node publication, Terraform
plan/apply, and refresh acknowledgement. Those persistent writer steps remain
serial within the job.

### Allowed parallelism

Concurrent jobs and workers are supported only when their physical workspaces
and output roots are disjoint. Fetching, transforming, validating, comparing,
or running private adoption-oracle scratch transactions can be parallelized in
disjoint roots. Read-only operations may share a stable root, but a publisher
must not run there concurrently.

If tenant or resource work must run in parallel, allocate a complete physical
workspace/output-root copy per worker and merge or publish results only after
the workers succeed. Do not point parallel workers at different leaf files
beneath one output root.

### Azure DevOps convention

Use a job-owned authority rooted at:

```text
$(Agent.TempDirectory)/infrawright/$(System.JobId)/workspace
```

Populate and run the complete workflow there. A relative deployment overlay
stays under that workspace. An absolute overlay must also be placed beneath the
same job-owned `$(Agent.TempDirectory)/infrawright/$(System.JobId)` authority;
never reuse a machine-global overlay across jobs.

Set `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` to the canonical deployment overlay
itself: the workspace for overlay `.`, the resolved overlay directory for a
relative overlay, or the exact external overlay directory for an absolute
overlay. Do not configure the job root or another containing ancestor.

The following is a reference shape, not a repository-owned pipeline:

```yaml
workspace:
  clean: all

steps:
  - checkout: self
    clean: true

  - bash: |
      set -euo pipefail
      job_root='$(Agent.TempDirectory)/infrawright/$(System.JobId)'
      test ! -e "$job_root"
      install -d -m 700 "$job_root/workspace"
      git -C '$(Build.SourcesDirectory)' archive --format=tar HEAD \
        | tar -xf - -C "$job_root/workspace"
      printf 'Agent.Name=%s\n' '$(Agent.Name)'
      printf 'Agent.MachineName=%s\n' '$(Agent.MachineName)'
      printf 'Pipeline.Workspace=%s\n' '$(Pipeline.Workspace)'
      printf 'Build.BuildId=%s\n' '$(Build.BuildId)'
      printf 'System.JobId=%s\n' '$(System.JobId)'
      printf 'Infrawright.Workspace=%s\n' "$job_root/workspace"
    displayName: Create and identify the job-owned workspace
```

Fetch inputs, credentials, dependencies, or selected untracked pipeline files
must be materialized inside that workspace after the archive step. A pipeline
may instead configure checkout directly into the job-specific path when its ADO
configuration supports that safely.

At minimum, every run logs the agent name, machine name, pipeline workspace,
build ID, job ID, canonical InfraWright workspace, and canonical materialization
output root. These values make accidental physical-root reuse diagnosable.

### Stale files and guards

Sequential self-hosted jobs may encounter stale checkout data, partial artifact
prefixes, refresh markers, or abandoned publisher guards.

- Use explicit ADO workspace/checkout cleaning or a newly created job-specific
  authority.
- Bootstrap and refresh retry-forward logic remains responsible for exact
  prefixes produced by an interrupted publication.
- The pending marker remains authoritative between refresh publication and
  acknowledgement; do not clear it as generic workspace cleanup.
- `OUTPUT_ROOT_BUSY` does not prove a live process. It reports an active **or
  stale** guard. InfraWright never auto-breaks it.
- Remove a stale guard only as an explicit pipeline/operator action after
  establishing that no publisher is active. Job-root deletion is safe only for
  a root that the current job exclusively owns.
- Agent maintenance may remove abandoned directories for completed jobs; it
  must not sweep another active job's root.

## Consequences

- Two runs of the same branch are safe when their physical roots are disjoint.
- A misconfigured shared absolute overlay fails fast instead of allowing two
  Node publishers to race.
- A containing output-root alias cannot create a second lock namespace for the
  same artifacts; it fails with `OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY` before any
  artifact write.
- The root-wide guard intentionally serializes different tenants/resources when
  they share one output root.
- The guard does not replace crash recovery, atomic per-file publication,
  final-byte verification, path containment, or the refresh pending marker.
- Existing process schemas, generated artifact bytes, publication order, and
  acknowledgement contracts are unchanged.
- Future removal of hostile-local-race rechecks is permitted only in separately
  reviewed phases and only while this ownership model remains true.

## Verification gate for later simplification

Before reducing existing TOCTOU checks, retain evidence from two concurrent
same-branch ADO jobs showing distinct canonical workspaces and output roots,
and retain tests proving same-root exclusion, disjoint-root concurrency, stale
guard refusal, and guard cleanup on success and failure.

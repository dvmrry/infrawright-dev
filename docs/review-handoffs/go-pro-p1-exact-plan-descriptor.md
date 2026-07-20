# GPT-5.6 Pro P1: descriptor-bound exact saved-plan Apply

Status: APPROVED after fresh adversarial review and changed-surface recheck.
The candidate remains unpushed.

Review result: the first review found one legacy pathname error-priority
regression. The builder restored `UNRESOLVED_TERRAFORM_SHOW_PATH`, added nil
descriptor preflight and focused budget/spawn/close regressions, and the same
reviewer approved the corrected surface with no remaining findings.

## Intent

- Close the gap where exact Apply assessed a private saved-plan snapshot but
  invoked Terraform Apply with the mutable public `tfplan` pathname.
- Bind one read-only descriptor to the prepared snapshot, verify its identity
  and bytes, and pass that same descriptor to both `terraform show` and
  `terraform apply`.
- Preserve the complete-field fail-closed gate, full freshness recheck,
  classification, branch/allow gates, post-Apply accounting, and artifact
  cleanup ordering.

## Base / Head

- Base: `e5d1a81857de76abca7e5c6dfdf8139a4b4a510e`.
- Head: current uncommitted worktree on
  `feature/go-canonjson-foundation`.
- Review only the files listed below. Concurrent canonjson, reference-backend,
  Node-source, and provenance-fixture changes belong to another parcel.

## Files Changed

- `go/internal/assessment/exact_plan_apply.go`
- `go/internal/assessment/exact_plan_apply_test.go`
- `go/internal/plan/evidence.go`
- `go/internal/plan/evidence_snapshot_posix.go`
- `go/internal/plan/evidence_snapshot_unsupported.go`
- `go/internal/plan/evidence_snapshot_test.go`
- `go/internal/terraformcmd/api.go`
- `go/internal/terraformcmd/runner.go`
- `go/internal/terraformcmd/show.go`
- `go/internal/terraformcmd/inherited_plan_darwin.go`
- `go/internal/terraformcmd/inherited_plan_linux.go`
- `go/internal/terraformcmd/inherited_plan_unsupported.go`
- `go/internal/terraformcmd/inherited_plan_test.go`
- This handoff.

Intentionally untouched: artifact/report renderers, plan classification,
provider/auth code, adoption staging, canonjson, Node sources, CLI command
surface, dependencies, and live-provider qualification.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing design records: the GPT-5.6 Pro migration review; existing
  saved-plan evidence and exact-Apply contracts; `docs/adversarial-review.md`.
- Local feasibility evidence: Terraform 1.15.4 on Darwin successfully rendered
  a provider-free saved plan through `/dev/fd/3` after its pathname was
  replaced. No provider, credential, remote backend, or real Apply was used.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures or snapshots: no committed fixture or snapshot change.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- `plan.OpenSavedPlanSnapshot` opens the private snapshot with
  `O_RDONLY|O_NONBLOCK|O_NOFOLLOW|O_CLOEXEC`, checks regular-file identity and
  digest through that descriptor, and charges a caller-supplied fresh budget.
- `terraformcmd` accepts exactly one optional saved-plan descriptor. Supported
  platforms enforce regular, seekable, and `F_GETFL/O_RDONLY` before spawn,
  rewind it for every command, and expose it only as child descriptor 3.
- Darwin uses `/dev/fd/3`; Linux uses `/proc/self/fd/3`; other platforms fail
  closed.
- `TerraformShowPlan` accepts exactly one of the existing absolute pathname or
  the new descriptor. General assessment keeps pathname mode; exact Apply uses
  descriptor mode.
- Exact Apply opens the descriptor after its first full evidence recheck,
  uses the same pointer for Show and Apply, performs the existing final full
  evidence recheck plus a descriptor digest/identity recheck, retains the file
  until Apply is reaped, then closes it before evidence cleanup.
- A successful Terraform Apply is recorded as applied even if a later close or
  artifact-cleanup operation fails.

## Invariants Claimed

- The exact bytes classified by Show are the bytes presented to Apply even if
  the public saved-plan pathname or private snapshot pathname is rebound.
- The `complete == true` typed gate still executes before classification or
  Apply.
- No descriptor is opened until initialization and the initial full evidence
  freshness recheck succeed.
- The final full evidence recheck and descriptor recheck both happen before
  Apply.
- Closed, pipe, read-write, non-regular, and unsupported descriptor cases fail
  before child spawn.
- Descriptor closure precedes descriptor-bound evidence cleanup. Post-Apply
  failures never under-report an already committed Apply.

## Tests Run

- Focused plan, terraformcmd, and assessment descriptor/exact-Apply tests.
- Focused race runs and full `go test -race ./internal/terraformcmd`.
- `go vet` on changed packages and `cmd/iw`.
- `gofmt` and `git diff --check`.
- RootCatalog and Topology standing byte gates passed.
- The builder could not obtain an isolated full-suite/Transform result because
  the concurrent surrogate parcel intentionally changed frozen provenance
  fixtures and another differential test removed the shared ignored binary.
  The coordinator must rerun the full Go suite, race targets, vet, and all four
  standing byte gates after parcel isolation and before commit.

## Known Deferrals

- `exec.Cmd.ExtraFiles` must make descriptor 3 inheritable by the Terraform
  process. A provider child can inherit it unless Terraform closes unrelated
  descriptors before spawning that child; the stdlib runner cannot force that
  closure without breaking Terraform's fd-path input. The descriptor is
  read-only and contains the same plan data Terraform is executing, but real
  Terraform/provider inheritance behavior remains an explicit credential-free
  qualification item.
- A same-UID actor that can modify the already-open inode in place remains
  outside the pathname-rebind closure. Eliminating that stronger threat needs
  an immutable/sealed object or separate OS identity, not another pathname
  check.
- No live provider, credential, API, remote backend, or real Terraform Apply
  was run or is authorized by this parcel.

## Review Focus

- Attack whether one descriptor really spans Show through Apply and whether
  any code path falls back to public or private path lookup.
- Attack budget charging, seek/offset handling, dev/inode and digest checks,
  read-only enforcement, unsupported-platform behavior, close ownership, and
  post-Apply result accounting.
- Verify error-priority and existing path-based `TerraformShowPlan` behavior
  remain unchanged for non-exact assessment callers.
- Verify the fake executable reads original fd bytes after pathname rebind,
  rather than merely asserting argv text.
- Treat descriptor inheritance into provider children and in-place same-inode
  mutation as explicit residuals; reject any broader claim that they are
  solved.

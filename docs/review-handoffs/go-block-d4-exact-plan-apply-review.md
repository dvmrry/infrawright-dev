# Block D4 Exact Saved-Plan Apply — Builder Review Handoff

## Intent

- Port `node-src/domain/exact-plan-apply.ts` onto the existing qualified Go
  plan, assessment, evidence, control-file, and `terraformcmd` boundaries.
- Preserve the complete-field fail-closed gate before every destroy or broad
  override decision.
- Reuse the randomized, descriptor-bound eab4fff assessment scratch lifecycle;
  do not introduce fixed slots, recursive pathname deletion, or a janitor.
- Keep every test fixture/fake-only. This parcel does not qualify or perform a
  real provider Apply.

This is a builder handoff, not approval. D4 is uncommitted and ready for a
fresh no-edit adversarial review.

## Base / Head

- Base: `4f59c5e` (`Port fail-closed adopt orchestration`).
- Head: uncommitted working tree on `feature/go-canonjson-foundation`.
- Review only:
  - `go/internal/assessment/exact_plan_apply.go`
  - `go/internal/assessment/exact_plan_apply_test.go`
  - this handoff

## Files / LOC

- Production: `exact_plan_apply.go` — 686 lines.
- Tests: `exact_plan_apply_test.go` — 808 lines.
- Go delta: 1,494 lines.
- No existing Go source, renderer, `go.mod`, or `go.sum` changed.

## Implementation Shape

- `ExactPlanApplyTerraform` exposes only init, typed-plus-lossless show, and
  exact saved-plan Apply.
- `CreateExactPlanApplyTerraform` composes existing `plan.CreatePlanTerraform`,
  `terraformcmd.TerraformShowPlan`, and bounded `RunTerraformCommand`; the exact
  Apply argv is `apply -input=false tfplan`.
- The adapter rejects a nil environment, requires an explicit executable and
  snapshotted environment, and never resolves `terraform` through PATH. Show
  uses the existing operational environment filter, then layers
  `terraform-json`'s typed `Plan.Complete` pointer on top of the lossless
  `canonjson` value consumed by `ClassifyPlan`.
- `CurrentApplyBranch` rejects a nil environment snapshot and preserves source priority:
  `BUILD_SOURCEBRANCH`, `GITHUB_REF`, `BITBUCKET_BRANCH`, then the bounded
  shell-free Git fallback. Both Git stdout and stderr are independently capped
  at Node's 64-KiB `maxBuffer` ceiling; overflow cancels and reaps the child
  instead of continuing to drain an unbounded stream, and `Cmd.WaitDelay`
  bounds a descendant that retains an inherited output pipe.
- `ApplyExactSavedPlans` preserves branch/policy/input/control/context/root
  ordering and processes roots serially in materialized Python order.
- Each root prepares bound saved-plan evidence, performs both complete
  freshness windows, classifies through `ClassifyPlan`, applies destroy and
  BLOCKED gates only after the complete gate, invokes the exact saved plan,
  then removes only `tfplan` and `tfplan.sources`.

## Scratch / Cleanup Contract

- Creation calls the same-package `makeAssessmentTemporaryDirectory`, which is
  randomized and private (`os.MkdirTemp`, requested 0700).
- The directory identity is captured with `directorySafeIdentity`.
- The prepared snapshot is bound with `assessmentSnapshotCleanupBinding`.
- Deferred cleanup first calls `plan.CleanupSavedPlanEvidence`, which scrubs the
  exact bound inode to zero.
- Only after successful scrubbing does
  `cleanupAssessmentTemporaryDirectory` verify the directory and exact
  zero-length snapshot identity through `os.Root`, remove the verified entry
  descriptor-relatively, recheck the public identity, and remove the empty
  directory through a bound parent root.
- No `os.RemoveAll`, recursive pathname deletion, fixed slot, or janitor exists.
- Descriptor-removal failure leaves a scrubbed randomized remnant and does not
  impose a future run ceiling. Unsafe identity/content change fails closed. A
  primary classification/apply failure retains precedence over a cleanup
  failure, matching Node. If Terraform already returned Apply success, the
  result's `Applied` count records that committed operation even when saved-pair
  removal or scratch cleanup subsequently fails; callers cannot mistake a
  cleanup error for a pre-Apply refusal.

## Safety Tests

- Branch priority, ref stripping, nil-environment refusal, Git fallback, both
  stream ceilings, prompt termination of a non-exiting overflowing child, and
  bounded handling of descendant-retained pipes before branch refusal, input,
  or Terraform work.
- Clean init/show/apply flow, exact init request, exact saved-pair removal, and
  empty scratch cleanup.
- Missing, false, null, string, and numeric `complete` variants all reject with
  both broad overrides enabled and prove zero Apply calls. A deliberately
  inconsistent injected typed plan also proves `tfjson.Plan.Complete` is an
  independent fail-closed gate.
- Destroy/BLOCKED override matrix proves the flags layer on top of
  classification rather than bypassing it.
- Four freshness classes—control, context, saved-plan evidence, and policy—are
  mutated in deterministic windows and never reach Apply.
- Three-root failure proves serial order, successful prior-pair removal, and
  preservation of both the failed and later saved pairs.
- Forty forced descriptor-removal failures followed by run 41 all succeed;
  retained remnants are private and contain only a zero-length regular
  snapshot.
- Hostile public-path swap is refused without touching the outside victim; the
  returned result still records the already-successful Apply and the narrow
  saved pair is gone.
- Apply failure plus simultaneous cleanup refusal preserves the primary Apply
  error. A post-Apply saved-pair removal failure likewise records the committed
  Apply count.
- Concurrent scratch creation returns unique randomized private directories.
- The real adapter test executes only an explicit temporary fake executable and
  proves exact argv plus full-vs-show environment asymmetry.
- A source guard rejects direct host-`terraform` resolution and Zscaler SDK use.

## Gates Run

- `gofmt` clean.
- `go vet ./internal/assessment` pass.
- Focused D4 tests pass.
- Focused D4 race tests pass.
- `go test -count=1 ./...` pass.
- `go test -race -count=1 ./internal/assessment` pass.
- D4 tests invoked no real Terraform binary, provider API, credential, tenant,
  remote backend, provider configuration, or live/local real Apply. The only
  command named `apply` was handled by a temporary fake executable.

## Review Focus

- Verify the complete gate is structurally before `AllowDestroy` and
  `AllowPlanChanges`.
- Attack cleanup failure precedence, the post-Apply cleanup-refusal ambiguity,
  and whether any pathname-rebind route can reach an outside target.
- Verify `expectedRoots` uses the current suffix for each recheck, matching
  Node after prior roots remove their saved pairs.
- Verify control → context → evidence → policy → control ordering in both
  windows with fresh serial budgets.
- Verify adapter argv/output/environment and CI branch priority byte-for-byte.
- Check that test-only fakes cannot reach a host Terraform or provider.

## Known Deferrals

- D5 owns CLI wiring, legacy exit-code behavior, and the combined frozen-Node
  stdout/stderr/artifact differential corpus.
- Controlled Apply qualification against any real provider state remains a
  separate human-gated event and is not authorized here.

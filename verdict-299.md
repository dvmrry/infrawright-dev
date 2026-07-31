# Adversarial review — PR #299 (branch claude/iw-prefix, commit 861c4b3)

Reviewer: codex (external, adversarial, read-only sandbox)
Target: `git -C <worktree> diff claude/sidecar-minimization...claude/iw-prefix`
Base: claude/sidecar-minimization (PR #298 stack)

## Decision

**FAIL**

## codex's findings (verbatim)

### Must fix

1. **Pre-rename AzureRM roots never receive the promised legacy `TF_VAR`.**
   The detector searches only for `variable "iw_remote_state_backend_config"`
   (`go/internal/plan/lifecycle.go:309`); its result gates environment
   projection (`go/internal/plan/lifecycle.go:631`). A root declaring only
   the legacy variable name returns false, so the function that emits both
   TF_VAR aliases is never called, and Terraform fails with a missing
   required variable. The new test
   (`TestReferenceBackendEnvironmentEmitsLegacyVariableAlias` in
   `go/internal/plan/reference_backend_test.go:211`) calls the projection
   function directly, bypassing the broken gate — so it passes without
   proving the end-to-end migration path works. codex recommends adding an
   end-to-end `PlanEnvironmentRoots` regression using a legacy-only root
   declaration to expose this.

2. **Raw-ID legacy caches remain unwrapped against new-only outputs.**
   Existing (unwrapped) legacy-selector caches win outright regardless of
   tokenization (`go/internal/envgen/environment_generator.go:1569`), but
   the try()-wrap + lookup-local emission only happens on the
   token-generation path (`environment_generator.go:2336`). The v2
   full-surface fixture deliberately pins unwrapped legacy selectors
   (`go/cmd/iw/v2_full_surface_qualification_test.go:460`), while
   regenerated referents and smoke mocks publish only `iw_reference_ids`.
   Once such a referent has current-only state, referrers holding an
   unwrapped raw legacy-spelled cache selector fail to resolve — the new
   wrapping test (`TestWrapResolverFallbacksWrapsBothSelectorSpellings`)
   invokes the helper directly and doesn't exercise this production
   condition.

`git diff --check` and `gofmt -d` were reported clean by codex; it did not
modify the worktree (read-only review).

### Verdict line (exact)

> VERDICT: FAIL — The advertised migration bridges are unreachable in real
> paths: legacy roots receive no backend TF_VAR, and raw legacy caches
> target an output regenerated referents no longer publish.

## Report summary

- Reviewer: codex
- Target: PR #299, branch `claude/iw-prefix` @ 861c4b3, diffed against
  `claude/sidecar-minimization`
- Decision: **fail**
- Notes: codex identifies two concrete migration-bridge holes rather than
  cosmetic nits: (1) the `iw plan` dual-TF_VAR projection is gated by a
  detector that only recognizes the new variable name, so a pre-rename root
  declaring only `infrawright_remote_state_backend_config` never gets the
  legacy alias emitted and Terraform apply fails on a missing required
  variable; (2) unwrapped raw-ID legacy selector caches (the "cache wins
  outright" bridge from #298) are never retrofitted with the try()-fallback
  wrap, so once a referent's state only publishes the new `iw_reference_ids`
  output, referrers still holding such a cache lose resolution. Both claims
  point at specific line numbers and note that the corresponding new tests
  call the relevant helper/projection functions directly rather than through
  the real gating path, so the tests pass without proving the migration
  bridge actually functions end-to-end.

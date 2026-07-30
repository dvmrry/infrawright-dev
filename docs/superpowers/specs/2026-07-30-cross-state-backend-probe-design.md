# Cross-state probe: ask the backend, not the workspace — design

Amends `docs/superpowers/specs/2026-07-25-cross-state-fallback-design.md`. The
fallback design stands: probe before binding, drop to the tfvars literal when
the referent has no usable state, report through the notes channel, fail
closed on probe errors. What this spec replaces is *how the probe acquires
state*.

## Problem

The shipped probe answers "does this referent root have usable state?" by
inspecting the local workspace:

- `go/internal/stateprobe/terraform_state_probe.go:86-92` requires
  `<root>/.terraform` to exist and reports absent otherwise.
- When it does run Terraform, it runs `state pull` *inside the generated
  referent root*, so it inherits that root's init.

Both facts assume a persistent, initialized workspace. Every consuming
pipeline is the opposite: `workspace: clean: all`, gen-env runs before any
init, env roots are regenerated fresh. Structurally, at gen-env time no root
has `.terraform`, so the probe answers absent for every referent on every CI
run, and `STATE_AWARE=1` has never done anything there except silently rewrite
every cross-state reference to a literal. The failure is invisible because
falling back is valid output.

Verified downstream (tf-zscaler pipelines, 2026-07-30): gen-env ordering
proves the probe fires before any init; run logs show the fallback note on
every reference; the only observable symptom was literal IDs in emitted roots.

The goal (restated by the requester): **no direct ID references where the
items are in tfstate**. The lookup must actually engage when the referent is
applied; the literal fallback stays for referents that are not.

## What is already correct and stays

Verified end-to-end in this repository:

- Emitted lookup shape:
  `data.terraform_remote_state.<root>.outputs.infrawright_reference_ids.<type>["<key>"]`,
  azurerm state key `<tenant>/<label>.tfstate`
  (`environment_generator.go:348`) — identical to the key `iw plan` inits
  each root with (`lifecycle.go:628`, `import_staging.go:142`).
- Plan-time variable wiring: roots declaring
  `variable "infrawright_remote_state_backend_config"` get
  `TF_VAR_infrawright_remote_state_backend_config` projected from the
  `BACKEND_CONFIG` JSON file (`lifecycle.go:379`, `reference_backend.go:89`).
- Referent roots publish the name→ID map from live state
  (`environment_generator.go:388`).
- `ReferenceIDsPresent` (`envgen/state_probe.go:82`) fail-closed state
  classification; `filterStatelessBindings` ordering after all validation
  gates; operator-binding exemption; `NOTE bindings:` reporting; memoized
  probe. All unchanged.

The one broken link is state acquisition. Fix that link only.

## Design

### Acquisition

Probe the backend the same way plan reaches it, from a scratch directory that
does not depend on any generated root:

1. Create a temp directory; write a minimal config:

   ```hcl
   terraform {
     backend "azurerm" {}
   }
   ```

2. `terraform init -input=false -reconfigure -backend-config=<file>
   -backend-config=key=<tenant>/<label>.tfstate` — the exact argv pattern of
   `import_staging.go:137-143` and `lifecycle.go:163-174`, with the same
   `BACKEND_CONFIG` file plan already consumes. Credentials come from the
   process environment, as they do for plan.
3. `terraform state pull`; classify the bytes with the existing
   `envgen.ReferenceIDsPresent`.

Outcome mapping (unchanged semantics, now reachable):

- Pull succeeds, empty or no per-type entry → absent → fall back, note.
- Pull succeeds with the entry → usable → binding survives, data block
  emitted.
- Init or pull fails (auth, network, missing container) → error → gen-env
  aborts. A probe that cannot answer must never degrade references.

One `state pull` per referent root per run (cache raw state per root inside
the prober; the existing memoization already dedupes per root+type).

### Backend dispatch

- Resolved backend nil/local → envgen's existing `localStateProbe`
  (reads `tenantDirectory/<label>/terraform.tfstate`, the same path the local
  data blocks embed). The CLI stops injecting a Terraform-backed prober here.
- Resolved backend `azurerm` → the scratch prober above. Requires
  `--backend-config`; `--state-aware` without it on an azurerm backend is a
  loud usage error, never a silent no-op.
- Any other backend → error, matching `renderRemoteStateBlocks`'s
  local-or-azurerm contract (`environment_generator.go:331`).

Backend resolution (flag vs `.backend` marker) lives in envgen; the CLI
cannot see the marker. The seam therefore becomes a factory:
`GenerateEnvironmentRootsOptions.StateProbeFor func(backend *string)
(StateProbe, error)`, consulted after marker resolution when the direct
`StateProbe` field is nil. The direct field stays for tests and library
callers; precedence is `StateProbe` > `StateProbeFor(backend)` >
`localStateProbe`.

### Surface changes

- `iw gen-env` gains `--backend-config` (value flag, same meaning as plan's).
  A relative path is resolved against the invocation directory before it
  reaches the probe, as `iw plan` and `stage-imports` already do for the same
  flag: the probe runs Terraform in a scratch directory, so an unresolved
  relative path would fail `init` for every realistic invocation.
- `make gen-env` gains `BACKEND_CONFIG=<file>` passthrough and passes
  `--terraform "$(TF)"` alongside `--state-aware`, mirroring stage-imports.
- `go/internal/stateprobe` drops `ResolveRootDirectory` and the `.terraform`
  existence check; gains `Tenant` and `BackendConfig` options.

## Invariants

- Without `--state-aware`, gen-env output remains a pure function of
  repository inputs — byte-identical to today (existing tests keep pinning
  this).
- With `--state-aware`, output is a deterministic function of (inputs, state
  snapshot). Repeat runs against unchanged state are byte-identical.
- Regression that must exist: the probe answers *usable* with **no generated
  root directory present anywhere** — the exact shape of a fresh CI
  workspace. This is the test the original implementation could never pass.
- The two tests pinning workspace-dependence
  (`TestProbeReportsAbsentForRootThatWasNeverGenerated`,
  `TestProbeReportsAbsentForUninitializedRoot`) are deleted with the behavior
  they pinned; their scenario ("referent never applied") is re-pinned at the
  correct layer: empty `state pull` → absent.

## Convergence story (why this meets the goal)

Run N: referent unapplied → probe absent → literal, note. Apply referent.
Run N+1: probe usable → lookup emitted → plan resolves the same ID from state
→ no-op plan. From then on the config never carries the literal again. No
ordering gate, no declaration list, no new files.

## Out of scope (follow-up plans)

- Folding `.generated.expressions.json` derivation into gen-env and moving
  `lookup.json` under a subdirectory (committed-surface minimization).
- Closure-ordered plan/apply dispatch.
- Downstream pipeline recommendation: generate once per pipeline and pass the
  artifact between plan and apply stages, since state-aware bytes depend on
  the state snapshot.

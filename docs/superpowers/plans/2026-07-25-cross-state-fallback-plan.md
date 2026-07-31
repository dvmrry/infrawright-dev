# Cross-state reference fallback — implementation plan

> **Amended by `2026-07-30-cross-state-backend-probe-plan.md`:** the probe's
> state acquisition was reworked to ask the backend from a scratch directory;
> the `.terraform`-beside-the-root acquisition this plan produced never fired
> in clean-workspace pipelines.

Companion to `docs/superpowers/specs/2026-07-25-cross-state-fallback-design.md`.
Read that first; this plan assumes its established facts and constraints.

## 0. Reproduction (premise now verified)

The brief's unverified premise — that a real generated env root fails at plan
time when a referenced root has no state — **reproduces** against actually
generated output. Procedure (all in scratch, worktree untouched):

1. `make dist/iw`.
2. Scratch overlay: copy `demo/config/demo` into `<scratch>/overlay/config/demo`,
   write `deployment.json` `{"overlay": "<scratch>/overlay", "module_dir":
   "<scratch>/overlay/modules/default"}`.
3. `iw modules generate --profile packs/full.packset.json`, then
   `iw gen-env --tenant demo --profile packs/full.packset.json`
   (both with `INFRAWRIGHT_DEPLOYMENT=<scratch>/deployment.json`).
4. The generated `envs/demo/zpa_server_group/main.tf` contains:

   ```hcl
   data "terraform_remote_state" "zpa_app_connector_group" {
     backend = "local"
     config = {
       path = "../zpa_app_connector_group/terraform.tfstate"
     }
   }
   ```

   and `expression_bindings.tf` rewrites
   `items["example100"].app_connector_groups[0].id` through that data source.
5. `terraform init` (succeeds), copy the tfvars in, `terraform plan`:

   - **Referenced state absent** → exit 1:

     ```
     Error: Unable to find remote state
       with data.terraform_remote_state.zpa_app_connector_group,
     No stored state was found for the given workspace in the given backend.
     ```

   - **Control** (hand-written `terraform.tfstate` for
     `zpa_app_connector_group` with the `infrawright_reference_ids` output):
     the remote-state error disappears entirely; only the unrelated
     provider-credential error remains. The failure is caused by state
     absence, nothing else.

   - **State present but outputs empty** (destroyed root): exit 1 with
     `Error: Unsupported attribute ... outputs is object with no attributes`.
     A bare file-existence probe is therefore insufficient; see §1.3.

## 1. Answers to the open design questions

### 1.1 Report format and channel

Reuse gen-env's existing stderr diagnostics channel with the established
`NOTE bindings:` prefix. Precedent:

- `filterGeneratedBindings` emits `NOTE bindings: stale generated binding
  ignored (...)` (`go/internal/envgen/environment_generator.go:633`,
  constants at `:73-74`).
- Transform emits a per-resource summary `"%s: %d bound, %d skipped
  (reasons)"` (`go/internal/tfrender/transform_artifacts.go:927-929`).

Emit one line per dropped binding plus a per-root summary, e.g.:

```
NOTE bindings: zpa_server_group.example100.app_connector_groups[0].id fell back to the tfvars literal; root zpa_app_connector_group has no usable state (envs/demo/zpa_app_connector_group/terraform.tfstate) — apply that root and rerun gen-env to bind
NOTE bindings: zpa_server_group: 0 cross-state binding(s) kept, 1 fell back (state absent: zpa_app_connector_group)
```

No new report file, no structured output, no acknowledgement gate —
"visibility, not ceremony". A machine-readable `Fallbacks` field on
`EnvironmentGenerationResult` is deferred until a consumer exists.

### 1.2 Probe-failure handling

Distinguish three outcomes:

- **Absent** (no state object; or state parses but carries no
  `infrawright_reference_ids` entry for the referenced resource type) →
  fall back, report.
- **Probe error** (unreadable file, corrupt state JSON; for azurerm later:
  init/pull failure, missing backend config) → **fail closed** with an error
  naming the root and the remedy. Never fall back on an error.
- **Probe disabled** (default) → generation byte-identical to today.

Precedent nuance that must NOT be copied: `stage-imports --state-aware` maps a
failed `terraform state list` to "no state" (`ListState`,
`go/internal/adopt/import_staging.go:174-177`). That is safe there because its
fallback direction is conservative (keeps all imports staged). Here the same
mapping would silently swap tenant-bound references for literals during a
transient backend outage. Copy stage-imports' hard failure on `Initialize`
error (`import_staging.go:576-578`) and `TERRAFORM_REQUIRED` (`:561-566`), not
its lenient `ListState` mapping.

### 1.3 Probe depth

**Per-root, per-referenced-resource-type "usable state" check — not bare
existence, not key-level.** Concretely for local state: the file at
`<tenantDirectory>/<label>/terraform.tfstate` (the exact path
`renderRemoteStateBlocks` embeds, `environment_generator.go:1282`) exists,
parses as JSON, and has `outputs.infrawright_reference_ids.value.<referent
type>` present as an object.

- Existence-only is empirically insufficient: a destroyed root leaves a state
  file whose empty outputs still halt the plan (§0, last bullet).
- Key-level probing (checking the individual stable key) is deliberately
  rejected for now: a present output map with one missing key means the item
  was renamed/removed since transform bound it — a staleness signal. Falling
  back there trades a loud failure for silent use of a stale literal, against
  the repository's fail-loud posture. That plan-time failure (`Invalid
  index`) stays loud. Revisit only with a real incident to motivate it; the
  brief already flags this as a separate call.

### 1.4 Determinism versus the byte-golden contract

Verified facts:

- `check-demo` and `demo-contract` diff **only** `demo/config/demo` and
  `demo/imports/demo` (Makefile:36-45, 91-104); `make demo` runs transform +
  modules generate, never gen-env. No env root is committed
  (`git ls-files | grep envs/` is empty). The demo byte-golden contract does
  not cover gen-env output at all.
- The gen-env byte gates that do exist — `topology_authority_test.go`
  (`gen-env.tree` golden), `v2_full_surface_qualification_test.go`
  (omitted vs explicit-true tree equality), and the envgen unit goldens — all
  invoke gen-env **without** the probe.

Therefore: keep the probe strictly opt-in (`--state-aware`, defaulting off,
mirroring stage-imports). The invariant to encode and test:

> Without `--state-aware`, gen-env output is a pure function of repository
> inputs; the presence or absence of any `terraform.tfstate` under the envs
> directory must not change a single emitted byte.

With `--state-aware`, bytes become a deterministic function of (inputs, state
snapshot); repeat runs against an unchanged snapshot must be byte-identical
(asserted in tests). No make target that feeds a golden or drift gate may ever
pass `--state-aware`. This keeps every existing gate meaningful with zero
golden churn (the only expected golden diff in this work is the CLI help text,
Step 2).

## 2. Implementation steps

Scope anchor: the change is small because the literal ID already survives in
tfvars, `ApplyExpressionBindings` only overlays it
(`go/internal/envgen/expression_bindings.go`), and dropping a binding already
removes its `data` block as a consequence (`remoteStateReferencesForBindings`
derives blocks from surviving bindings, `environment_generator.go:651`;
`removeIfPresent` already prunes a now-empty `expression_bindings.tf`,
`:1326-1332`). The fix is a filter plus a probe plus a flag.

Per AGENTS.md "Validation Promotion": every step below writes its focused test
first and demonstrates it failing against the unfixed behaviour (or a stated
faithful mutation) before the production edit. Full-corpus suites run only in
Step 4.

### Step 1 — envgen: probe-gated filtering of generated bindings (local state)

**Files**

- `go/internal/envgen/environment_generator.go`
- `go/internal/envgen/environment_generator_test.go`

**Behaviour change**

- Add to `GenerateEnvironmentRootsOptions`:
  - `StateAware bool` (default false — everything below is inert when false).
  - `StateProbe func(rootLabel, referentType string) (StateProbeResult, error)`
    — optional injection seam for tests and, later, the azurerm prober. When
    `StateAware` is true and `StateProbe` is nil, install the default local
    prober described in §1.3. `StateProbeResult` distinguishes
    usable / absent; errors are returned, not folded into "absent".
- In `GenerateEnvironmentRoots`, after backend resolution (the `.backend`
  marker read, `:1193-1204`): if `StateAware` and the resolved backend is
  azurerm, fail with a clear error (`state-aware generation currently probes
  local state only; azurerm probing arrives with --backend-config`) until
  Step 3 lands. Build the probe closure over `tenantDirectory` (which already
  respects `OutputRoot`) and memoize results per (label, referentType) for the
  invocation.
- In `loadBindingLayers` (`:794`), thread the probe through and apply a new
  `filterStatelessGeneratedBindings` to the **generated layer only**,
  immediately after the existing `filterGeneratedBindings`: for each binding,
  `ExpressionRemoteStateReferences(binding.Expression)`
  (`expression_bindings.go:1627`); if any referenced root's probe says
  absent, drop the binding and emit the per-binding NOTE (§1.1). Emit the
  per-root summary NOTE from the caller once per root when at least one
  binding fell back. The operator layer (`<type>.expressions.json`) is never
  filtered — explicit operator intent to reference a stateless root keeps its
  `data` block and keeps failing loudly at plan time.
- Everything downstream is untouched by construction: surviving bindings drive
  `remoteStateReferencesForBindings`, `validateRemoteStateReferences`, the
  rendered `data` blocks, and `expression_bindings.tf` emission/removal.

**Tests (write first, show red)**

1. *Fallback on absent state* — temp-overlay fixture in the style of
   `TestSingletonCrossStateDisableRemovesStaleGeneratedBindings`
   (`environment_generator_test.go:1005`): config + `.generated.expressions.json`
   binding onto a second root, no state file. `StateAware: true` →
   `main.tf` contains no `data "terraform_remote_state"`,
   `expression_bindings.tf` absent, both NOTE lines present in collected
   diagnostics. **Red proof**: run against current code (option not yet
   consumed) — data block present, test fails.
2. *Usable state keeps the binding* — same fixture plus a state file whose
   `outputs.infrawright_reference_ids.value.<referent>` contains the key:
   `StateAware: true` output byte-identical (`snapshotTree`) to
   `StateAware: false` output; a second `StateAware: true` run is
   byte-identical to the first (repeat determinism). Guards against the
   faithful mutation "drop every generated binding whenever StateAware".
3. *Destroyed-root state falls back* — state file present, outputs empty →
   same assertions as test 1. Guards the mutation "probe checks file
   existence only" (mutation makes this test fail; §0 shows why it matters).
4. *Probe error fails closed* — corrupt (non-JSON) state file →
   `GenerateEnvironmentRoots` returns an error naming the root. Guards the
   mutation "treat probe error as absent".

   Corrected during implementation: this originally also promised "no output
   tree mutation for that root". Generation is not transactional — the
   referrer's directory can already exist when the probe fails — so that
   guarantee was never true and is not claimed. Making generation atomic is a
   separate change, out of this step's scope.
5. *Operator bindings never filtered* — operator `.expressions.json` with a
   declared-edge remote reference to a stateless root, `StateAware: true` →
   `data` block still emitted. Guards the mutation "filter the merged layers
   instead of the generated layer".
6. *Default path is state-blind* — `StateAware: false`, generate twice: once
   with a state file present under `envs/`, once without → byte-identical
   trees. This is the §1.4 invariant. Guards the mutation "probe
   unconditionally".
7. *azurerm + StateAware rejected* — deployment/backend resolving to azurerm,
   `StateAware: true` → the explicit not-yet-supported error.

Independently runnable: `cd go && go test ./internal/envgen -run StateAware`
(or the chosen test-name stem). Reverting this step is deleting the option,
filter, and tests — no other step depends on its internals beyond the option
name.

### Step 2 — CLI: `--state-aware` on gen-env

**Files**

- `go/cmd/iw/commands_topology.go` (`newGenEnvCobraCommand` gains
  `boolFlags: []string{"--state-aware"}` following
  `commands_adopt_apply.go:322`; `genEnvInput` maps it to
  `GenerateEnvironmentRootsOptions.StateAware`)
- `go/cmd/iw/cobra.go` — the shared flag-description map (`:231`) currently
  reads "inspect local ephemeral state while staging imports"; generalize to
  something command-neutral, e.g. "inspect ephemeral state before staging or
  generating". This map is shared with stage-imports, so its help output
  changes too.
- `go/cmd/iw/cobra_test.go` / `cobra_docs_test.go` goldens — the help-text
  golden updates from the description change are the only expected golden
  diff in this whole plan; review them as such.
- `Makefile` `gen-env` target: pass `$(if $(STATE_AWARE),--state-aware)`
  mirroring how stage-imports exposes it (check its existing recipe and copy
  the idiom). Do **not** touch the `demo`, `check-demo`, or `demo-contract`
  recipes (§1.4).

No terraform-preflight change: `iw gen-env` is already in the
always-requires-terraform list (`cobra.go:328`), and the local probe needs no
terraform at all.

**Tests (write first, show red)**

1. Flag-surface test in the existing `cobra_test.go` style: gen-env accepts
   `--state-aware`. **Red proof**: fails with "unknown flag" before wiring.
2. Binary-level focused test (patterned on
   `runV2FullSurfaceGenEnv`, `v2_full_surface_qualification_test.go:187`):
   temp workspace with a cross-state binding and no state, run
   `gen-env --state-aware` → exit 0, stderr contains the fallback NOTE,
   emitted `main.tf` has no `data` block; run without the flag → `data`
   block present. The second half doubles as the CLI-level default-path
   regression.

### Step 3 — azurerm probe (explicitly deferrable)

Land only if the azurerm adoption workflow needs it now; Steps 1-2 deliver
the reproduced local-state case completely, and the Step 1 error message
keeps azurerm honest rather than silently unprobed.

**Files**: `go/cmd/iw/commands_topology.go` (add `--backend-config` value
flag — description already exists in the shared map), a small prober in
`go/cmd/iw` (or `go/internal/envgen` behind the existing `StateProbe` seam —
keep `envgen` free of terraform execution; build the prober at the CLI layer
with `go/internal/terraformcmd`, mirroring `CreateImportStagingTerraform`,
`import_staging.go:129`).

**Behaviour**: probe in a scratch directory (TMPDIR) containing only
`terraform { backend "azurerm" {} }`: `terraform init -input=false
-reconfigure -backend-config=<file>
-backend-config=key=<tenant>/<label>.tfstate` then `terraform state pull`.
Empty pull → absent (then apply the same §1.3 outputs check to the pulled
JSON); any init/pull failure → fail closed (§1.2). The scratch-dir approach is
order-independent — it does not require the referenced root's directory to
have been generated yet (generation order is alphabetical, not topological:
`CrossStateDependencyClosure`, `reference_topology.go:84-105`).
`--state-aware` without `--backend-config` while the resolved backend is
azurerm keeps failing with the Step 1 error, now suggesting
`--backend-config`.

**Tests**: unit tests against a fake prober seam (absent / present /
outputs-missing / error), red-first by asserting the Step 1 azurerm rejection
is replaced by probe-driven behaviour; a terraformcmd-level test for the
scratch-dir invocation argv (no live Azure anywhere).

### Step 4 — promotion gates

Only after Steps 1-2 (and 3 if taken) are green under their focused commands:

- `cd go && go test -count=1 ./...`
- `make check`
- `make v2-authority` — the gen-env goldens must be byte-identical (nothing
  in them enables the probe); the only accepted diffs are the Step 2 help
  goldens.

Per AGENTS.md, if any corpus gate fails, reproduce with a focused command
before touching anything, and do not rerun passing supersets.

## 3. Scope boundaries (stop-and-ask lines)

- **No transform, lineage, or assessment changes.** `DeriveGeneratedBindings`
  and its notes are untouched; the fix consumes its artifact.
- **No `overrideKeys` entry** anywhere. If one seems needed, stop and ask.
- **No `try(...)` emission** for the state-present/key-missing case — that
  residual failure stays loud (§1.3); changing it is a separate decision.
- **Operator bindings are never filtered** by the probe.
- **`check-demo` / `demo-contract` / `make demo` never enable the probe**;
  the demo byte-golden contract stays a pure function of repo inputs.
- `go/internal/envgen` gains no terraform-execution dependency; any
  terraform-based probing enters through the injected `StateProbe` seam from
  the CLI layer.
- Generic production code beyond gen-env and these direct helpers is out of
  scope; touching it means stop and ask, not implementer discretion.

## 4. Deliberately deferred

- **azurerm probing** (Step 3) if not immediately required — local covers the
  reproduced case and incremental adoption on local/demo workflows; the
  explicit rejection error prevents silent no-probe azurerm runs meanwhile.
- **Key-level probe decisions** and any `try(..., literal)` fallback for
  stale keys — fail-loud posture wins until a concrete need shows up.
- **Machine-readable fallback report** (`Fallbacks` on
  `EnvironmentGenerationResult`) — no consumer today; stderr NOTEs satisfy
  the "pipeline reports fallbacks" constraint.
- **Re-binding automation** (detecting that a previously-fallen-back root now
  has state) — rerunning `gen-env --state-aware` already restores bindings;
  the NOTE text says exactly that.

## 5. Size estimate

Production code: ~2 files for the core (Steps 1-2:
`environment_generator.go`, `commands_topology.go` + a one-line description
edit in `cobra.go` and a Makefile idiom); +1-2 files if Step 3 is taken.
Tests: ~3 files (`environment_generator_test.go`, `cobra_test.go`, one
binary-level test). Two landable PR-sized steps (three with azurerm), each
independently revertible. This matches the brief's "small change" argument;
the only place the work is bigger than the brief implied is the probe depth —
bare existence is provably not enough (§0), so the probe reads the state's
output map at type level.

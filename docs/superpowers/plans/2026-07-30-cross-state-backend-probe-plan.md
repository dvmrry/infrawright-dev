# Cross-State Backend Probe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Companion to `docs/superpowers/specs/2026-07-30-cross-state-backend-probe-design.md`.
Read that first; this plan assumes its verified facts.

**Goal:** Make `gen-env --state-aware` probe the referent's *backend* (scratch
`init` + `state pull`) instead of the local workspace, so cross-state lookups
actually engage in clean-workspace pipelines.

**Architecture:** Rework `go/internal/stateprobe` acquisition; add a
backend-aware factory seam to envgen consulted after `.backend` marker
resolution; wire `--backend-config` through `iw gen-env` and `make gen-env`.
Classification (`ReferenceIDsPresent`), filtering, notes, and memoization are
untouched.

**Tech Stack:** Go, `terraformcmd.RunTerraformCommand`, sh fake-terraform test
harness (existing pattern in `terraform_state_probe_test.go`).

## Global Constraints

- Per AGENTS.md Validation Promotion: each task's focused regression must fail
  against pre-fix behavior (or a stated faithful mutation) before the
  production edit; full suites run only in Task 5.
- Without `--state-aware`, gen-env bytes are unchanged — no golden churn.
- Probe errors always abort generation; only a successful pull that lacks the
  entry may fall back.
- No new committed artifacts, no new sidecars, no new report files.

---

### Task 1: stateprobe — scratch-directory backend acquisition

**Files:**
- Rewrite: `go/internal/stateprobe/terraform_state_probe.go`
- Rewrite: `go/internal/stateprobe/terraform_state_probe_test.go`

**Interfaces:**
- Produces: `stateprobe.New(Options{BackendConfig, Environment, Tenant,
  TerraformExecutable}) envgen.StateProbe`. `ResolveRootDirectory` is gone.

- [ ] **Step 1: Rewrite the test file.** Upgrade `fakeTerraform` to dispatch on
  the subcommand and record argv:

```go
// fakeTerraform writes an executable stand-in that logs each argv line to
// argvLog, exits initCode for `init`, and emits pullStdout/pullCode for
// `state pull`. Driving the probe through a real child process keeps the
// terraformcmd runner in the path.
func fakeTerraform(t *testing.T, argvLog string, initCode int, pullStdout string, pullCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terraform")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(argvLog) + "\n" +
		"if [ \"$1\" = init ]; then exit " + itoa(initCode) + "; fi\n" +
		"printf '%s' " + shellQuote(pullStdout) + "\nexit " + itoa(pullCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o777); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}
	return path
}

func probeFor(t *testing.T, initCode int, pullStdout string, pullCode int) (bool, error, string) {
	t.Helper()
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	probe := New(Options{
		BackendConfig:       "/config/backend.azurerm.json",
		Environment:         map[string]string{},
		Tenant:              "demo",
		TerraformExecutable: fakeTerraform(t, argvLog, initCode, pullStdout, pullCode),
	})
	result, err := probe("referent_root", "example_type")
	raw, _ := os.ReadFile(argvLog)
	return result.Usable, err, string(raw)
}
```

  Keep `appliedState`, `shellQuote`, `itoa`. Replace the workspace-dependent
  tests with:

```go
// TestProbeAnswersWithoutAnyWorkspace pins the defect this rewrite exists
// for: a fresh CI workspace has no generated roots and no .terraform
// anywhere, and the probe must still reach the backend and answer usable.
func TestProbeAnswersWithoutAnyWorkspace(t *testing.T) {
	usable, err, _ := probeFor(t, 0, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if !usable {
		t.Errorf("usable = false, want true with no workspace present")
	}
}

// TestProbeInitArgvMatchesPlanConvention pins that the scratch init consumes
// the same BACKEND_CONFIG file and derives the same per-root key iw plan
// uses, so probe and plan can never look at different state addresses.
func TestProbeInitArgvMatchesPlanConvention(t *testing.T) {
	_, err, argv := probeFor(t, 0, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	for _, want := range []string{
		"init -input=false -reconfigure -backend-config=/config/backend.azurerm.json -backend-config=key=demo/referent_root.tfstate",
		"state pull",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv log %q does not contain %q", argv, want)
		}
	}
}

// TestProbeReportsAbsentForEmptyStatePull re-pins the never-applied referent
// at the correct layer: the backend answers, and the answer is empty.
// (table: "", "\n", synthesized empty state — same three cases as before)

// TestProbeFailsClosedWhenInitFails pins that a backend the probe cannot
// even configure (missing container, bad auth surfaces here too) is an
// error, never absence.
func TestProbeFailsClosedWhenInitFails(t *testing.T) {
	_, err, _ := probeFor(t, 1, appliedState, 0)
	if err == nil {
		t.Fatalf("probe error = nil, want a refusal when terraform init fails")
	}
	if !strings.Contains(err.Error(), "referent_root") {
		t.Errorf("error = %q, want it to name the root", err)
	}
}

// TestProbePullsOncePerRoot pins the per-root cache: two referent types on
// one root must not re-init or re-pull.
func TestProbePullsOncePerRoot(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	probe := New(Options{
		BackendConfig: "/config/b.json", Environment: map[string]string{},
		Tenant: "demo", TerraformExecutable: fakeTerraform(t, argvLog, 0, appliedState, 0),
	})
	if _, err := probe("referent_root", "example_type"); err != nil {
		t.Fatalf("first probe: %v", err)
	}
	if _, err := probe("referent_root", "other_type"); err != nil {
		t.Fatalf("second probe: %v", err)
	}
	raw, _ := os.ReadFile(argvLog)
	if got := strings.Count(string(raw), "state pull"); got != 1 {
		t.Errorf("state pull ran %d times, want 1", got)
	}
}
```

  Retain (adapted to the new `probeFor` signature):
  `TestProbeReportsUsableForAppliedState`,
  `TestProbeFailsClosedWhenTerraformExits` (pull exit 1),
  `TestProbeFailsClosedForMalformedState`.
  Delete: `TestProbeReportsAbsentForRootThatWasNeverGenerated`,
  `TestProbeReportsAbsentForUninitializedRoot`, `initializedRoot`.

- [ ] **Step 2: Run to verify failure.**
  `go test ./go/internal/stateprobe/` — expected: compile failure
  (`Options` has no `BackendConfig`/`Tenant`), which is the focused
  pre-fix failure for an interface change.

- [ ] **Step 3: Rewrite the probe.** Replace `Options` and `New`; delete
  `directoryExists`; rewrite the package doc comment (workspace rationale is
  gone — the probe now asks the backend the same way plan does):

```go
type Options struct {
	// BackendConfig is the same JSON backend file iw plan consumes; the
	// probe derives each root's key, so the file never names one.
	BackendConfig string
	// Environment is the complete child environment, never merged with the
	// host's. Backend credentials reach Terraform through it.
	Environment map[string]string
	// Tenant scopes the per-root state key, <tenant>/<label>.tfstate.
	Tenant string
	// TerraformExecutable is the resolved terraform binary.
	TerraformExecutable string
}

type pullOutcome struct {
	raw []byte
	err error
}

// New returns a StateProbe that pulls each referenced root's state from the
// configured azurerm backend in a scratch directory, so the answer never
// depends on workspace contents. One pull per root per run.
func New(options Options) envgen.StateProbe {
	pulls := map[string]pullOutcome{}
	return func(rootLabel, referentType string) (envgen.StateProbeResult, error) {
		outcome, cached := pulls[rootLabel]
		if !cached {
			outcome.raw, outcome.err = pullState(options, rootLabel)
			pulls[rootLabel] = outcome
		}
		if outcome.err != nil {
			return envgen.StateProbeResult{}, outcome.err
		}
		if len(bytes.TrimSpace(outcome.raw)) == 0 {
			return envgen.StateProbeResult{Usable: false}, nil
		}
		return envgen.ReferenceIDsPresent(outcome.raw, rootLabel, referentType)
	}
}

func pullState(options Options, rootLabel string) ([]byte, error) {
	scratch, err := os.MkdirTemp("", "iw-stateprobe-")
	if err != nil {
		return nil, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
	}
	defer os.RemoveAll(scratch)
	backend := "terraform {\n  backend \"azurerm\" {}\n}\n"
	if err := os.WriteFile(path.Join(scratch, "backend.tf"), []byte(backend), 0o666); err != nil {
		return nil, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
	}
	if _, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: options.TerraformExecutable,
		Argv: []string{
			"init", "-input=false", "-reconfigure",
			"-backend-config=" + options.BackendConfig,
			"-backend-config=key=" + options.Tenant + "/" + rootLabel + ".tfstate",
		},
		CWD:         scratch,
		Environment: options.Environment,
		Output:      terraformcmd.TerraformCommandOutputDiscard,
	}); err != nil {
		return nil, fmt.Errorf(
			"probe state for root %s: terraform init against the cross-state backend failed, so this run cannot tell an unapplied root from an unreachable backend: %w",
			rootLabel, err,
		)
	}
	result, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: options.TerraformExecutable,
		Argv:                []string{"state", "pull"},
		CWD:                 scratch,
		Environment:         options.Environment,
		Output:              terraformcmd.TerraformCommandOutputCapture,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"probe state for root %s: terraform state pull failed, so this run cannot tell an unapplied root from an unreachable backend: %w",
			rootLabel, err,
		)
	}
	return result.Stdout, nil
}
```

- [ ] **Step 4: Run to verify pass.** `go test ./go/internal/stateprobe/`
  — expected: PASS.

- [ ] **Step 5: Commit.** `git add go/internal/stateprobe && git commit -m
  "stateprobe: pull referent state from the backend, not the workspace"`

---

### Task 2: envgen — StateProbeFor factory seam

**Files:**
- Modify: `go/internal/envgen/environment_generator.go:1242-1247` (options),
  `:1333-1347` (probe resolution)
- Test: `go/internal/envgen/state_aware_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `GenerateEnvironmentRootsOptions.StateProbeFor func(backend
  *string) (StateProbe, error)`; precedence `StateProbe` >
  `StateProbeFor(backend)` > `localStateProbe`.

- [ ] **Step 1: Write the failing tests** (in `state_aware_test.go`, using the
  file's existing generation harness — mirror the fixture setup of the
  nearest `StateProbe`-injecting test in that file):

```go
// TestStateProbeForReceivesResolvedBackend pins that the factory sees the
// backend the run actually resolved (marker or flag), not the flag alone —
// otherwise a marker-configured azurerm tenant would probe local state.
func TestStateProbeForReceivesResolvedBackend(t *testing.T) {
	// Arrange a tenant directory whose .backend marker says "azurerm"
	// (write the marker as the harness's backend-marker tests do), no
	// --backend flag. Run generation with:
	var seen []string
	options.StateAware = true
	options.StateProbeFor = func(backend *string) (StateProbe, error) {
		if backend == nil {
			seen = append(seen, "<nil>")
		} else {
			seen = append(seen, *backend)
		}
		return func(string, string) (StateProbeResult, error) {
			return StateProbeResult{Usable: true}, nil
		}, nil
	}
	// Assert generation succeeds and seen == []string{"azurerm"}.
}

// TestStateProbeForErrorAbortsGeneration pins fail-closed at the seam: a
// factory that cannot build a probe (e.g. azurerm without backend config)
// must abort the run, not degrade it.
func TestStateProbeForErrorAbortsGeneration(t *testing.T) {
	options.StateAware = true
	options.StateProbeFor = func(*string) (StateProbe, error) {
		return nil, errors.New("backend config required")
	}
	// Assert GenerateEnvironmentRoots returns an error containing
	// "backend config required" and writes no roots.
}

// TestDirectStateProbeBeatsFactory pins precedence so existing library
// callers and tests keep meaning what they said.
func TestDirectStateProbeBeatsFactory(t *testing.T) {
	// options.StateProbe = <usable-true stub>; options.StateProbeFor =
	// factory that t.Fatal()s if called. Assert generation succeeds.
}

// TestNilFactoryResultFallsBackToLocalProbe pins the local path: factory
// returns (nil, nil) and generation uses tenantDirectory/<label>/
// terraform.tfstate exactly as when no factory is installed.
```

- [ ] **Step 2: Run to verify failure.**
  `go test ./go/internal/envgen/ -run 'TestStateProbeFor|TestDirectStateProbe|TestNilFactoryResult'`
  — expected: compile failure (`StateProbeFor` undefined).

- [ ] **Step 3: Implement.** In the options struct:

```go
	// StateProbe overrides how StateAware resolves a referenced root's
	// state. Nil consults StateProbeFor, then the local prober.
	StateProbe StateProbe
	// StateProbeFor builds the probe after the backend is resolved (flag or
	// .backend marker) — the CLI's seam, since only generation can see the
	// marker. Returning (nil, nil) selects the local prober; returning an
	// error aborts the run: a probe that cannot be built must not degrade
	// references. Consulted only when StateProbe is nil.
	StateProbeFor func(backend *string) (StateProbe, error)
```

  At the probe-resolution site (after the marker read):

```go
	var probe StateProbe
	if options.StateAware {
		probe = options.StateProbe
		if probe == nil && options.StateProbeFor != nil {
			built, err := options.StateProbeFor(backend)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			probe = built
		}
		if probe == nil {
			probe = localStateProbe(tenantDirectory)
		}
		probe = memoizedStateProbe(probe)
	}
```

- [ ] **Step 4: Run to verify pass.** Same `-run` filter — expected: PASS.
- [ ] **Step 5: Run the package.** `go test ./go/internal/envgen/` —
  expected: PASS (821-line suite untouched).
- [ ] **Step 6: Commit.** `git commit -m "envgen: resolve the state probe
  through a backend-aware factory seam"`

---

### Task 3: iw gen-env — --backend-config and the azurerm prober

**Files:**
- Modify: `go/cmd/iw/commands_topology.go` (gen-env flag list + probe
  injection block at `:402-440`), `go/cmd/iw/cobra.go:231` area (help text)
- Test: `go/cmd/iw/gen_env_state_aware_test.go`

**Interfaces:**
- Consumes: `stateprobe.New` (Task 1), `StateProbeFor` (Task 2).
- Produces: `iw gen-env --state-aware [--backend-config <file>]` behavior.

- [ ] **Step 1: Write the failing tests** (harness style of the existing
  tests in `gen_env_state_aware_test.go`, which run the command with a fake
  terraform and scratch deployment):

```go
// TestGenEnvStateAwareAzurermRequiresBackendConfig: run gen-env
// --state-aware against a tenant whose .backend marker says azurerm, no
// --backend-config. Expect a non-zero exit and stderr containing
// "requires --backend-config" — a loud usage error where the old probe
// silently degraded every reference.

// TestGenEnvStateAwareLocalNeedsNoTerraform: run gen-env --state-aware on a
// local-backend tenant with no terraform binary on PATH; expect success
// (local probing is a file read; resolving terraform would be a regression).

// TestGenEnvStateAwareRejectsUnsupportedBackend: marker "s3" + --state-aware
// → error naming local and azurerm.
```

- [ ] **Step 2: Run to verify failure.**
  `go test ./go/cmd/iw/ -run TestGenEnvStateAware` — expected: FAIL
  (today the azurerm case silently proceeds with the root-dir prober, and
  the local case tries to resolve terraform).

- [ ] **Step 3: Implement.** In the gen-env command: add `--backend-config`
  to the value-flag list; replace the unconditional `stateprobe.New`
  injection with:

```go
	if generateOptions.StateAware {
		environment := environMap()
		selectedTerraform, _ := lastCommandOption(parsed, "--terraform")
		backendConfig, hasBackendConfig := lastCommandOption(parsed, "--backend-config")
		generateOptions.StateProbeFor = func(backend *string) (envgen.StateProbe, error) {
			if backend == nil || *backend == "" || *backend == "local" {
				// The local prober is a file read beside the generated
				// roots; envgen installs it when this returns nil.
				return nil, nil
			}
			if *backend != "azurerm" {
				return nil, fmt.Errorf("state-aware generation supports local or azurerm state, not %s", *backend)
			}
			if !hasBackendConfig {
				return nil, fmt.Errorf("state-aware generation against an azurerm backend requires --backend-config (the same file iw plan consumes) so the probe can reach the backend; without it every reference would silently fall back to its literal")
			}
			executable, err := terraformcmd.ResolveTerraformExecutable(selectedTerraform, environment)
			if err != nil {
				return nil, err
			}
			return stateprobe.New(stateprobe.Options{
				BackendConfig:       backendConfig,
				Environment:         environment,
				Tenant:              tenant,
				TerraformExecutable: executable,
			}), nil
		}
	}
```

  Update the cobra help line for `--backend-config` reuse and the
  `--state-aware` description if it names the old mechanism.

- [ ] **Step 4: Run to verify pass.** Same filter — PASS.
- [ ] **Step 5: Commit.** `git commit -m "gen-env: reach azurerm state via
  --backend-config; fail loudly when the probe cannot"`

---

### Task 4: Makefile, make-level test, docs

**Files:**
- Modify: `Makefile:148-150` (gen-env target + usage)
- Test: `go/cmd/iw/make_gen_env_state_aware_test.go`
- Modify: `docs/superpowers/specs/2026-07-25-cross-state-fallback-design.md`,
  `docs/superpowers/plans/2026-07-25-cross-state-fallback-plan.md` (one-line
  amendment pointer at top), `CHANGELOG.md`

- [ ] **Step 1: Extend the make-level test** to expect the new recipe line
  (assert it forwards `--terraform "$(TF)"` with `--state-aware` and
  `--backend-config "$(BACKEND_CONFIG)"` when set). Run
  `go test ./go/cmd/iw/ -run TestMakeGenEnv` — expected: FAIL.
- [ ] **Step 2: Edit the target:**

```make
gen-env: dist/iw ## Generate env roots for a tenant (TENANT=<label> [BACKEND=azurerm] [STATE_AWARE=1] [BACKEND_CONFIG=<file>] [RESOURCE="<type|provider> ..."])
	@test -n "$(TENANT)" || { echo "usage: make gen-env TENANT=<label> [BACKEND=azurerm] [STATE_AWARE=1] [BACKEND_CONFIG=<file>] [RESOURCE=\"<type|provider> ...\"]"; exit 2; }
	$(IW) gen-env --tenant "$(TENANT)" --profile "$(PACK_PROFILE)" $(if $(BACKEND),--backend "$(BACKEND)") $(if $(STATE_AWARE),--state-aware --terraform "$(TF)") $(if $(BACKEND_CONFIG),--backend-config "$(BACKEND_CONFIG)") $(foreach rt,$(RESOURCE),--resource "$(rt)")
```

- [ ] **Step 3: Run to verify pass**, then add the amendment pointers to the
  two 2026-07-25 docs ("Amended by
  `2026-07-30-cross-state-backend-probe-design.md`: state acquisition now
  asks the backend; the workspace-dependent probe never fired in clean
  pipelines.") and a CHANGELOG entry noting the new flag and the new loud
  error.
- [ ] **Step 4: Commit.** `git commit -m "make gen-env: forward
  BACKEND_CONFIG and the terraform executable to state-aware runs"`

---

### Task 5: Promotion gates

- [ ] `gofmt -l go/` → empty; `go vet ./go/...` → clean.
- [ ] `go test ./go/...` — full suite, expected PASS.
- [ ] `make check-core` (or the repo's standard gate if narrower) — PASS.
- [ ] Self-review against the spec's Invariants section; confirm the
  no-state-aware byte-purity tests were not touched.
- [ ] Stop at "ready for adversarial review" per AGENTS.md — the change
  alters code that can silently drop bindings; produce the handoff note and
  dispatch the adversarial reviewer before merge.

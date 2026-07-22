package main

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCobraTreeCarriesCompleteCommandSurface(t *testing.T) {
	root := newCobraRoot()
	var got []string
	for _, command := range root.Commands() {
		if command.Name() == "completion" || command.Name() == "help" {
			continue
		}
		got = append(got, command.Name())
	}
	sort.Strings(got)
	want := []string{
		"adopt", "apply", "assert-adoptable", "assert-clean", "check-pack",
		"check-pack-set", "clean-plans", "deployment", "fetch", "fetch-diag",
		"gen-env", "modules", "openapi-map", "plan", "plan-roots",
		"provider-probe", "reconcile", "resources", "roots",
		"scope-paths", "source-evidence-eval", "source-operation-map",
		"stage-imports", "transform", "transform-adopt-parity", "unstage-imports",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Cobra command surface = %q, want %q", got, want)
	}

	modules, _, err := root.Find([]string{"modules"})
	if err != nil {
		t.Fatalf("find modules: %v", err)
	}
	var verbs []string
	for _, command := range modules.Commands() {
		verbs = append(verbs, command.Name())
	}
	sort.Strings(verbs)
	if strings.Join(verbs, ",") != "generate,validate" {
		t.Fatalf("modules verbs = %q, want generate and validate", verbs)
	}
}

func TestCobraTreeExposesSafetyAndAuthoringFlags(t *testing.T) {
	root := newCobraRoot()
	checks := map[string][]string{
		"apply":                {"allow-destroy", "allow-non-main", "allow-plan-changes", "policy", "terraform"},
		"source-operation-map": {"allow-unverified-source", "artifact-dir", "source-manifest", "provider-file", "sdk-root"},
		"provider-probe":       {"debug-traceback", "work-dir"},
	}
	for path, flags := range checks {
		command, _, err := root.Find([]string{path})
		if err != nil {
			t.Fatalf("find %s: %v", path, err)
		}
		for _, name := range flags {
			if command.Flags().Lookup(name) == nil {
				t.Errorf("%s lacks --%s", path, name)
			}
		}
	}
}

func TestCobraHelpIsDeterministicAndCommandSpecific(t *testing.T) {
	render := func(command *cobra.Command) string {
		t.Helper()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs([]string{"--help"})
		if status, err := cobraExecutionResult(command.Execute()); err != nil || status != 0 {
			t.Fatalf("help execution = (%d, %v)", status, err)
		}
		return output.String()
	}
	first := render(newCobraRoot())
	second := render(newCobraRoot())
	if first != second {
		t.Fatal("Cobra root help is not deterministic")
	}
	root := newCobraRoot()
	plan, _, findErr := root.Find([]string{"plan"})
	if findErr != nil {
		t.Fatalf("find plan: %v", findErr)
	}
	usage := plan.UsageString()
	for _, fragment := range []string{"iw plan", "--imports-only", "--save", "--backend-config"} {
		if !strings.Contains(usage, fragment) {
			t.Errorf("plan usage lacks %q: %q", fragment, usage)
		}
	}
}

func TestCobraParserSupportsInlineValuesEndOfFlagsAndNativeErrors(t *testing.T) {
	var got commandInput
	command := newTypedCobraCommand(typedCobraCommandSpec{
		use: "sample", valueFlags: []string{"--out", "--resource"}, boolFlags: []string{"--save"},
		run: func(parsed commandInput) (int, error) {
			got = parsed
			return 0, nil
		},
	})
	status, err := executeStandaloneCobra(command, []string{
		"--out=result.json", "--resource", "first", "--resource=second", "--save", "--", "--literal",
	})
	if err != nil || status != 0 {
		t.Fatalf("Cobra parse = (%d, %v)", status, err)
	}
	if value, _ := lastCommandOption(got, "--out"); value != "result.json" {
		t.Errorf("--out = %q", value)
	}
	if strings.Join(got.Options["--resource"], ",") != "first,second" {
		t.Errorf("--resource = %q", got.Options["--resource"])
	}
	if !got.Flags.Has("--save") || strings.Join(got.Positionals, ",") != "--literal" {
		t.Errorf("flags/positionals = %#v/%q", got.Flags, got.Positionals)
	}
	got = commandInput{}
	status, err = executeStandaloneCobra(newTypedCobraCommand(typedCobraCommandSpec{
		use: "sample", boolFlags: []string{"--save"},
		run: func(parsed commandInput) (int, error) {
			got = parsed
			return 0, nil
		},
	}), []string{"--save=false"})
	if err != nil || status != 0 || got.Flags.Has("--save") {
		t.Fatalf("--save=false = (%d, %v, %#v), want false without error", status, err, got.Flags)
	}

	_, err = executeStandaloneCobra(newTypedCobraCommand(typedCobraCommandSpec{
		use: "sample", run: func(commandInput) (int, error) { return 0, nil },
	}), []string{"--unknown"})
	var exit *cliExit
	if !errors.As(err, &exit) || exit.status != 2 || exit.message != "unknown flag: --unknown" {
		t.Fatalf("unknown flag error = %T(%v), want Cobra usage exit", err, err)
	}
}

func TestTypedCobraEmptyValuePolicy(t *testing.T) {
	for _, arguments := range [][]string{{"--out", ""}, {"--out="}} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			called := false
			status, err := executeStandaloneCobra(newTypedCobraCommand(typedCobraCommandSpec{
				use: "sample", valueFlags: []string{"--out"},
				run: func(commandInput) (int, error) {
					called = true
					return 0, nil
				},
			}), arguments)
			var exit *cliExit
			if status != 0 || !errors.As(err, &exit) || exit.status != 2 || exit.message != "--out requires a value" {
				t.Fatalf("empty --out = (%d, %T(%v)), want usage exit 2", status, err, err)
			}
			if called {
				t.Fatal("empty --out reached the command operation")
			}
		})
	}

	var got commandInput
	status, err := executeStandaloneCobra(newTypedCobraCommand(typedCobraCommandSpec{
		use: "sample", valueFlags: []string{"--tenant"}, allowEmpty: []string{"--tenant"},
		run: func(parsed commandInput) (int, error) {
			got = parsed
			return 0, nil
		},
	}), []string{"--tenant="})
	if err != nil || status != 0 {
		t.Fatalf("allowed empty --tenant = (%d, %v)", status, err)
	}
	values, present := got.Options["--tenant"]
	if !present || len(values) != 1 || values[0] != "" {
		t.Fatalf("allowed empty --tenant = %#v, want one explicit empty value", got.Options)
	}
}

func TestEveryCobraStringFlagRejectsEmptyUnlessExplicitlyAllowed(t *testing.T) {
	allowed := map[string]bool{
		"iw roots --tenant":            true,
		"iw plan-roots --tenant":       true,
		"iw clean-plans --tenant":      true,
		"iw scope-paths --path":        true,
		"iw plan --tenant":             true,
		"iw assert-clean --tenant":     true,
		"iw assert-adoptable --tenant": true,
		"iw apply --tenant":            true,
	}
	seenAllowed := make(map[string]bool, len(allowed))
	for _, prototype := range documentedCobraCommands(newCobraRoot()) {
		path := prototype.CommandPath()
		for rawName := range cobraFlagDescriptions {
			flag := prototype.LocalNonPersistentFlags().Lookup(strings.TrimPrefix(rawName, "--"))
			if flag == nil {
				continue
			}
			if flag.Value.Type() == "bool" {
				continue
			}
			key := path + " --" + flag.Name
			if allowed[key] {
				seenAllowed[key] = true
				continue
			}
			t.Run(strings.ReplaceAll(key, " ", "_"), func(t *testing.T) {
				root := newCobraRootWithTerraformPreflight(func() error { return nil })
				root.SetOut(&bytes.Buffer{})
				root.SetErr(&bytes.Buffer{})
				arguments := append(strings.Fields(path)[1:], "--"+flag.Name+"=")
				root.SetArgs(arguments)
				status, err := cobraExecutionResult(root.Execute())
				var exit *cliExit
				if status != 0 || !errors.As(err, &exit) || exit.status != 2 || exit.message != "--"+flag.Name+" requires a value" {
					t.Fatalf("%s empty value = (%d, %T(%v)), want usage exit 2", key, status, err, err)
				}
			})
		}
	}
	for key := range allowed {
		if !seenAllowed[key] {
			t.Errorf("allowed-empty declaration %q is not present in the Cobra tree", key)
		}
	}
}

func TestTerraformPreflightUsesResolvedCobraCommandAndEffectiveFlags(t *testing.T) {
	preflightFailure := errors.New("injected unsupported Terraform platform")
	tests := []struct {
		name          string
		arguments     []string
		wantPreflight bool
	}{
		{name: "ordinary plan", arguments: []string{"plan"}, wantPreflight: true},
		{name: "gen env", arguments: []string{"gen-env"}, wantPreflight: true},
		{name: "modules generate", arguments: []string{"modules", "generate"}, wantPreflight: true},
		{name: "help", arguments: []string{"plan", "--help"}},
		{name: "help consumed as value", arguments: []string{"plan", "--tenant", "--help"}, wantPreflight: true},
		{name: "unknown before help", arguments: []string{"plan", "--unknown", "--help"}},
		{name: "help after terminator", arguments: []string{"plan", "--", "--help"}, wantPreflight: true},
		{name: "effective help false", arguments: []string{"plan", "--help", "--help=false"}, wantPreflight: true},
		{name: "state aware false then true", arguments: []string{"stage-imports", "--state-aware=false", "--state-aware"}, wantPreflight: true},
		{name: "state aware true then false", arguments: []string{"stage-imports", "--state-aware", "--state-aware=false", "unexpected"}},
		{name: "non terraform help", arguments: []string{"check-pack", "--help"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			root := newCobraRootWithTerraformPreflight(func() error {
				calls++
				return preflightFailure
			})
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(test.arguments)
			_, err := cobraExecutionResult(root.Execute())
			if test.wantPreflight {
				if calls != 1 || !errors.Is(err, preflightFailure) {
					t.Fatalf("preflight = %d calls, error %T(%v); want one injected rejection", calls, err, err)
				}
				return
			}
			if calls != 0 {
				t.Fatalf("preflight = %d calls, want zero", calls)
			}
		})
	}
}

func TestEmptyTerraformOptionCannotOverrideTFOrReachPlanDependencies(t *testing.T) {
	t.Setenv("TF", "/fake/configured/terraform")
	status, err := executeStandaloneCobra(newPlanCobraCommand(planCommandDependencies{}), []string{"--terraform="})
	var exit *cliExit
	if status != 0 || !errors.As(err, &exit) || exit.status != 2 || exit.message != "--terraform requires a value" {
		t.Fatalf("plan --terraform= = (%d, %T(%v)), want usage exit 2", status, err, err)
	}
}

func TestCobraRootClassifiesMissingAndUnknownCommandsAsUsage(t *testing.T) {
	for _, arguments := range [][]string{nil, {"bogus-command"}} {
		status, err := runCobra(arguments)
		var exit *cliExit
		if status != 0 || !errors.As(err, &exit) || exit.status != 2 {
			t.Errorf("runCobra(%q) = (%d, %T(%v)), want usage exit 2", arguments, status, err, err)
		}
	}
}

func TestCobraHelpUsesStdoutWithoutStderr(t *testing.T) {
	root := newCobraRoot()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"apply", "--help"})
	status, err := cobraExecutionResult(root.Execute())
	if err != nil || status != 0 {
		t.Fatalf("apply --help = (%d, %v)", status, err)
	}
	if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("apply --help stdout/stderr lengths = %d/%d, want nonzero/zero", stdout.Len(), stderr.Len())
	}
}

func TestCobraGeneratesShellCompletion(t *testing.T) {
	root := newCobraRoot()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"completion", "bash"})
	status, err := cobraExecutionResult(root.Execute())
	if err != nil || status != 0 {
		t.Fatalf("completion bash = (%d, %v)", status, err)
	}
	if !strings.Contains(stdout.String(), "__start_iw") || stderr.Len() != 0 {
		t.Fatalf("completion bash stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
}

func TestEveryCobraCommandProvidesHelpWithoutRunningDomainLogic(t *testing.T) {
	for _, prototype := range documentedCobraCommands(newCobraRoot()) {
		if prototype.CommandPath() == "iw" {
			continue
		}
		path := strings.Fields(prototype.CommandPath())[1:]
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			root := newCobraRoot()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(append(path, "--help"))
			status, err := cobraExecutionResult(root.Execute())
			if err != nil || status != 0 {
				t.Fatalf("%s --help = (%d, %v)", strings.Join(path, " "), status, err)
			}
			if !strings.Contains(output.String(), "Usage:") {
				t.Fatalf("%s --help lacks Usage: %q", strings.Join(path, " "), output.String())
			}
		})
	}
}

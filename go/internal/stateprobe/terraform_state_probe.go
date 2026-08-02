// Package stateprobe resolves whether a generated root's Terraform state
// carries cross-state reference identifiers, by asking the configured backend
// rather than by inspecting the workspace.
//
// It lives outside envgen deliberately. envgen owns the decision -- which
// bindings survive, in what order relative to validation, and what is reported
// -- and must gain no Terraform-execution dependency to make it. This package
// supplies the one predicate that decision needs, injected through envgen's
// StateProbeFor seam.
//
// The probe runs `terraform init` + `terraform state pull` in a scratch
// directory holding nothing but an azurerm backend stanza, configured from the
// same BACKEND_CONFIG file `iw plan` consumes and the same per-root key
// convention (`<tenant>/<label>.tfstate`) plan inits each root with. The
// answer therefore never depends on workspace contents: a fresh pipeline
// checkout and a long-lived operator clone probe identical state. The previous
// implementation answered from the workspace (it required the generated
// referent root to exist and carry `.terraform`), which meant every
// clean-workspace pipeline read every referent as absent and silently rewrote
// every cross-state reference to a literal.
//
// Local-backend tenants never reach this package: envgen's own local prober
// reads the state file beside each generated root, the same path the emitted
// local data blocks embed.
package stateprobe

import (
	"bytes"
	"fmt"
	"os"
	"path"

	"github.com/dvmrry/infrawright-dev/go/internal/envgen"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

// runTerraformCommand keeps the probe tests at the command-options seam
// without introducing a runner abstraction shared by production callers.
var runTerraformCommand = terraformcmd.RunTerraformCommand

// Options configures a backend-backed probe.
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

// pullOutcome caches one root's `state pull` so a root referenced through
// several resource types is read once, and cannot answer two identical
// probes differently within a run. Errors are cached too: a pull that failed
// once must not appear to succeed later in the same run.
type pullOutcome struct {
	raw []byte
	err error
}

// New returns a StateProbe that pulls each referenced root's state from the
// configured azurerm backend in a scratch directory.
//
// The classification rests on a split Terraform makes for every backend
// uniformly:
//
//   - exit 0 with empty output, or a synthesized empty state -- the backend
//     answered and holds no state (or none whose outputs can satisfy the
//     reference). Absent: the ordinary not-yet-applied case, which falls back
//     to the tfvars literal.
//   - exit 0 with a state document -- decoded by envgen's own parser, so a
//     remote root and a local one are judged by identical rules.
//   - non-zero exit from init or pull -- the backend was unreachable or
//     refused. The probe could not answer, so it fails closed rather than
//     reporting absence: folding an unreachable backend into "absent" would
//     silently rewrite every reference in the run to a literal.
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

// pullState fetches rootLabel's raw state document from the backend. The
// scratch directory holds only a backend stanza -- no providers, no modules
// -- so init configures the backend and nothing else.
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
	if _, err := runTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: options.TerraformExecutable,
		Argv: []string{
			"init", "-input=false", "-reconfigure",
			"-backend-config=" + options.BackendConfig,
			"-backend-config=key=" + options.Tenant + "/" + rootLabel + ".tfstate",
		},
		CWD:         scratch,
		Environment: options.Environment,
		Output:      terraformcmd.TerraformCommandOutputInheritStderr,
	}); err != nil {
		return nil, fmt.Errorf(
			"probe state for root %s: terraform init against the cross-state backend failed, so this run cannot tell an unapplied root from an unreachable backend: %w",
			rootLabel, err,
		)
	}
	result, err := runTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: options.TerraformExecutable,
		Argv:                []string{"state", "pull"},
		CWD:                 scratch,
		Environment:         options.Environment,
		Output:              terraformcmd.TerraformCommandOutputCaptureStdoutInheritStderr,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"probe state for root %s: terraform state pull failed, so this run cannot tell an unapplied root from an unreachable backend: %w",
			rootLabel, err,
		)
	}
	return result.Stdout, nil
}

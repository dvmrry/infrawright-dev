// Package stateprobe resolves whether a generated root's Terraform state
// carries cross-state reference identifiers, by asking Terraform rather than
// by reading state files directly.
//
// It lives outside envgen deliberately. envgen owns the decision -- which
// bindings survive, in what order relative to validation, and what is reported
// -- and must gain no Terraform-execution dependency to make it. This package
// supplies the one predicate that decision needs, injected through envgen's
// StateProbe seam.
//
// Asking Terraform is what makes the probe backend-agnostic. `terraform state
// pull` resolves whatever backend the root was initialized with, so local,
// azurerm, s3, gcs, and consul are all served by the same code path with no
// backend name appearing anywhere in it.
package stateprobe

import (
	"bytes"
	"fmt"
	"os"
	"path"

	"github.com/dvmrry/infrawright-dev/go/internal/envgen"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

// RootDirectoryResolver maps a root label to the working directory Terraform
// should run in. envgen owns that layout, so callers pass its resolver rather
// than reimplementing it here.
type RootDirectoryResolver func(rootLabel string) (string, error)

// Options configures a Terraform-backed probe.
type Options struct {
	// Environment is the complete child environment, never merged with the
	// host's. Backend credentials reach Terraform through it.
	Environment map[string]string
	// ResolveRootDirectory locates the referenced root on disk.
	ResolveRootDirectory RootDirectoryResolver
	// TerraformExecutable is the resolved terraform binary.
	TerraformExecutable string
}

// New returns a StateProbe backed by `terraform state pull`.
//
// The classification rests on a split Terraform makes for every backend
// uniformly:
//
//   - exit 0 with empty output -- a root initialized but never applied, and
//     the implicit-local case where no state exists yet. Absent.
//   - exit 0 with a state document -- decoded by envgen's own parser, so a
//     remote root and a local one are judged by identical rules. A backend
//     holding no state yet answers here too, with a synthesized empty state
//     whose outputs cannot satisfy the reference; that also reads as absent.
//   - non-zero exit -- the backend was unreachable, refused, or the root was
//     never initialized. The probe could not answer, so it fails closed
//     rather than reporting absence: folding an unreachable backend into
//     "absent" would silently rewrite every reference in the run to a literal.
//
// A root that has not been initialized fails closed with Terraform's own
// "Backend initialization required" message, which is the correct default:
// silently treating an uninitialized root as unapplied would hide a setup
// error behind a fallback.
func New(options Options) envgen.StateProbe {
	return func(rootLabel, referentType string) (envgen.StateProbeResult, error) {
		directory, err := options.ResolveRootDirectory(rootLabel)
		if err != nil {
			return envgen.StateProbeResult{}, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
		}
		// A root that has never been generated has no directory, and Terraform
		// cannot start in one that does not exist. That is the ordinary case
		// when adoption reaches a root before its referents: absent, not a
		// probe failure. Anything else about the path is left to Terraform.
		// An uninitialized root is absent, not a probe failure. Terraform
		// refuses `state pull` in a generated root whose modules are not
		// installed ("Module not installed. Run terraform init"), and that is
		// precisely the incremental-adoption case this feature exists for: the
		// referent has been generated but never applied. A root that was never
		// generated at all has no directory, so it has no .terraform either and
		// is answered here too -- no separate existence check is needed, and one
		// was removed after a mutation proved it carried no behaviour.
		//
		// Reading it as absent is sound rather than merely convenient. Without
		// init Terraform cannot read that root's state under any backend, so a
		// data block pointing at it would fail at plan time regardless -- the
		// reference has to fall back either way.
		initialized, err := directoryExists(path.Join(directory, ".terraform"))
		if err != nil {
			return envgen.StateProbeResult{}, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
		}
		if !initialized {
			return envgen.StateProbeResult{Usable: false}, nil
		}
		result, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
			TerraformExecutable: options.TerraformExecutable,
			Argv:                []string{"state", "pull"},
			CWD:                 directory,
			Environment:         options.Environment,
			Output:              terraformcmd.TerraformCommandOutputCapture,
		})
		if err != nil {
			return envgen.StateProbeResult{}, fmt.Errorf(
				"probe state for root %s: terraform state pull failed, so this run cannot tell an unapplied root from an unreachable backend: %w",
				rootLabel, err,
			)
		}
		if len(bytes.TrimSpace(result.Stdout)) == 0 {
			return envgen.StateProbeResult{Usable: false}, nil
		}
		return envgen.ReferenceIDsPresent(result.Stdout, rootLabel, referentType)
	}
}

// directoryExists reports whether a path is an existing directory. A path that
// exists but is not a directory is an error rather than absence: the layout is
// not what this probe assumes, and guessing would mean guessing about state.
func directoryExists(candidate string) (bool, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a directory", candidate)
	}
	return true, nil
}

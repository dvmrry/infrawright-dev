package envgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// StateProbeResult reports whether a referenced root's Terraform state can
// satisfy a cross-state reference. Absent and usable are the only outcomes;
// a probe that cannot answer returns an error instead, because folding a
// backend failure into "absent" would silently rewrite every reference in
// the run to a literal. That distinction is the whole point of the type --
// see the doc comment on the local prober below.
type StateProbeResult struct {
	Usable bool
}

// StateProbe answers whether rootLabel's state carries reference identifiers
// for referentType. It is an injection seam: tests supply their own, and the
// azurerm backend will supply one that pulls remote state.
type StateProbe func(rootLabel, referentType string) (StateProbeResult, error)

const (
	statelessBindingNote = "cross-state binding for %s.%s fell back to the literal value; root %s has no usable state"
	statelessSummaryNote = "%s: %d cross-state binding(s) fell back to literal values (roots without usable state: %s)"
)

// localStateProbe returns a StateProbe that inspects the local tfstate each
// generated root writes beside itself, at the same path renderRemoteStateBlocks
// points its data blocks at (tenantDirectory/<label>/terraform.tfstate).
//
// A missing state file is "absent" -- the ordinary not-yet-applied case.
// Anything else (unreadable file, malformed JSON) is an error, never absent:
// stage-imports' ListState deliberately maps a Terraform failure to "no
// state" because keeping imports is the safe direction there, but here the
// safe direction is the opposite, so that leniency is not copied.
func localStateProbe(tenantDirectory string) StateProbe {
	return func(rootLabel, referentType string) (StateProbeResult, error) {
		statePath := path.Join(tenantDirectory, rootLabel, "terraform.tfstate")
		raw, err := os.ReadFile(statePath)
		if err != nil {
			if os.IsNotExist(err) {
				return StateProbeResult{Usable: false}, nil
			}
			return StateProbeResult{}, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
		}
		return referenceIDsPresent(raw, rootLabel, referentType)
	}
}

// referenceIDsPresent reports whether a decoded Terraform state publishes
// reference identifiers for referentType.
//
// Existence of the state file is not sufficient: a root that has been
// destroyed, or applied before it published the output, leaves a state whose
// outputs cannot satisfy the reference, and Terraform halts on that with
// "Unsupported attribute" exactly as it does on a missing state. Both are
// degenerate roots rather than drift, so both fall back.
//
// A state that cannot be decoded is an error rather than "absent", so a
// corrupt or truncated file never silently rewrites references to literals.
func referenceIDsPresent(raw []byte, rootLabel, referentType string) (StateProbeResult, error) {
	var state struct {
		Outputs map[string]struct {
			Value map[string]any `json:"value"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return StateProbeResult{}, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
	}
	output, present := state.Outputs[InfrawrightReferenceOutput]
	if !present {
		return StateProbeResult{Usable: false}, nil
	}
	value, present := output.Value[referentType]
	if !present {
		return StateProbeResult{Usable: false}, nil
	}
	// Presence is not enough. A JSON null decodes to a present key holding a
	// nil value, and a string or list decodes to a present key holding the
	// wrong shape; both would be reported usable by a bare key check and then
	// halt Terraform at plan time on the index. An absent key is a degenerate
	// root and falls back, but a key holding a non-object is a malformed
	// state we cannot reason about, so it fails closed.
	if _, isObject := value.(map[string]any); !isObject {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: %s.%s is %T, want an object of reference identifiers",
			rootLabel, InfrawrightReferenceOutput, referentType, value,
		)
	}
	return StateProbeResult{Usable: true}, nil
}

// memoizedStateProbe caches probe outcomes per (root, referent type) for one
// generation run, so a state that changes mid-run cannot classify two
// identical references differently and a root referenced by many bindings is
// read once. Errors are cached too: a probe that failed once must not appear
// to succeed later in the same run.
func memoizedStateProbe(probe StateProbe) StateProbe {
	type outcome struct {
		result StateProbeResult
		err    error
	}
	cache := map[string]outcome{}
	return func(rootLabel, referentType string) (StateProbeResult, error) {
		identity := rootLabel + "\x00" + referentType
		if cached, hit := cache[identity]; hit {
			return cached.result, cached.err
		}
		result, err := probe(rootLabel, referentType)
		cache[identity] = outcome{result: result, err: err}
		return result, err
	}
}

// filterStatelessGeneratedBindings drops generated bindings whose referenced
// root has no usable state, so the tfvars literal underneath survives and no
// terraform_remote_state data block is emitted for that root. Operator
// bindings are never passed here: an operator who writes a remote-state
// reference by hand is asserting intent, and that assertion keeps failing
// loudly.
func filterStatelessGeneratedBindings(
	bindings []ExpressionBinding,
	resourceType string,
	probe StateProbe,
	onDiagnostic func(string),
) ([]ExpressionBinding, error) {
	kept := make([]ExpressionBinding, 0, len(bindings))
	statelessRoots := map[string]bool{}
	dropped := 0
	for _, binding := range bindings {
		references, err := ExpressionRemoteStateReferences(binding.Expression)
		if err != nil {
			return nil, err
		}
		unusable := ""
		for _, reference := range references {
			result, err := probe(reference.Root, reference.ResourceType)
			if err != nil {
				return nil, err
			}
			if !result.Usable {
				unusable = reference.Root
				break
			}
		}
		if unusable == "" {
			kept = append(kept, binding)
			continue
		}
		dropped++
		statelessRoots[unusable] = true
		onDiagnostic("NOTE bindings: " + fmt.Sprintf(statelessBindingNote, binding.Key, binding.Path, unusable))
	}
	if dropped > 0 {
		roots := canonjson.SortedStrings(mapKeysBoolSetGeneric(statelessRoots))
		onDiagnostic("NOTE bindings: " + fmt.Sprintf(statelessSummaryNote, resourceType, dropped, strings.Join(roots, ", ")))
	}
	return kept, nil
}

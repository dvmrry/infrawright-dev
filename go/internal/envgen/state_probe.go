package envgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	ctyjson "github.com/zclconf/go-cty/cty/json"

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
// for referentType. It is an injection seam: tests supply their own, and a
// root configured with a remote backend needs one that pulls its state rather
// than reading a file beside the root.
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
		return ReferenceIDsPresent(raw, rootLabel, referentType)
	}
}

// supportedStateVersion is the Terraform state format this probe understands.
// Anything else is refused rather than guessed at: reading an unknown envelope
// and reporting "absent" would silently rewrite references to literals.
const supportedStateVersion = 4

// ReferenceIDsPresent reports whether a decoded Terraform state publishes
// reference identifiers for referentType.
//
// The envelope is checked before the outputs are read. Terraform accepts a file
// as state only when it carries a recognised version and an outputs object, and
// only when each output carries both its value and its type metadata; a bare
// JSON object such as {} is not an applied root with no outputs, it is not
// state at all. Treating it as absence would fall back on every reference in
// the run on the strength of a file Terraform itself refuses.
//
// Existence of the output is not sufficient either: a root that has been
// destroyed, or applied before it published the output, leaves a state whose
// outputs cannot satisfy the reference, and Terraform halts on that with
// "Unsupported attribute" exactly as it does on a missing state. Both are
// degenerate roots rather than drift, so both fall back.
//
// A state that cannot be decoded is an error rather than "absent", so a
// corrupt or truncated file never silently rewrites references to literals.
func ReferenceIDsPresent(raw []byte, rootLabel, referentType string) (StateProbeResult, error) {
	var state struct {
		Version *int                        `json:"version"`
		Outputs *map[string]json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return StateProbeResult{}, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
	}
	if state.Version == nil {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: document carries no state version, so Terraform would not accept it as state",
			rootLabel,
		)
	}
	if *state.Version != supportedStateVersion {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: state version %d is unsupported, want %d",
			rootLabel, *state.Version, supportedStateVersion,
		)
	}
	// A nil pointer covers both an absent "outputs" key and an explicit null:
	// Terraform always writes an object here, even when it is empty.
	if state.Outputs == nil {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: state carries no outputs object",
			rootLabel,
		)
	}
	rawOutput, present := (*state.Outputs)[InfrawrightReferenceOutput]
	if !present {
		return StateProbeResult{Usable: false}, nil
	}
	var output struct {
		Type  json.RawMessage `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(rawOutput, &output); err != nil {
		return StateProbeResult{}, fmt.Errorf("probe state for root %s: %w", rootLabel, err)
	}
	if isAbsentJSON(output.Type) {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: output %s carries no type metadata, so Terraform would not accept it as state",
			rootLabel, InfrawrightReferenceOutput,
		)
	}
	if isAbsentJSON(output.Value) {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: output %s carries no value",
			rootLabel, InfrawrightReferenceOutput,
		)
	}
	// The type is parsed and the value checked against it with the same
	// decoder Terraform uses, so a state this probe calls usable is one
	// Terraform would actually load. Checking only that a type field exists
	// accepts "type": "garbage" and any value disagreeing with its declared
	// type -- both of which Terraform refuses with "Invalid output value type
	// in state", after this code has already rewritten references on the
	// strength of them.
	outputType, err := ctyjson.UnmarshalType(output.Type)
	if err != nil {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: output %s has an invalid type in state: %w",
			rootLabel, InfrawrightReferenceOutput, err,
		)
	}
	if _, err := ctyjson.Unmarshal(output.Value, outputType); err != nil {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: output %s does not match its declared type in state: %w",
			rootLabel, InfrawrightReferenceOutput, err,
		)
	}
	var value map[string]any
	if err := json.Unmarshal(output.Value, &value); err != nil {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: %s is not an object of per-resource-type reference identifiers: %w",
			rootLabel, InfrawrightReferenceOutput, err,
		)
	}
	referent, present := value[referentType]
	if !present {
		return StateProbeResult{Usable: false}, nil
	}
	// Presence is not enough. A JSON null decodes to a present key holding a
	// nil value, and a string or list decodes to a present key holding the
	// wrong shape; both would be reported usable by a bare key check and then
	// halt Terraform at plan time on the index. An absent key is a degenerate
	// root and falls back, but a key holding a non-object is a malformed
	// state we cannot reason about, so it fails closed.
	if _, isObject := referent.(map[string]any); !isObject {
		return StateProbeResult{}, fmt.Errorf(
			"probe state for root %s: %s.%s is %T, want an object of reference identifiers",
			rootLabel, InfrawrightReferenceOutput, referentType, referent,
		)
	}
	return StateProbeResult{Usable: true}, nil
}

// isAbsentJSON reports whether a raw field was omitted or written as null,
// which the state envelope treats identically.
func isAbsentJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
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
		// Every reference is probed even once one is known absent: stopping
		// early would hide a later probe failure behind the earlier absence,
		// downgrading "state could not be read" to a silent fallback. Any
		// error therefore beats absence regardless of reference order, and the
		// first absent root is the one reported.
		unusable := ""
		for _, reference := range references {
			result, err := probe(reference.Root, reference.ResourceType)
			if err != nil {
				return nil, err
			}
			if !result.Usable && unusable == "" {
				unusable = reference.Root
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

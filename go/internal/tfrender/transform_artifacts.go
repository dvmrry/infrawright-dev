package tfrender

// transform_artifacts.go ports node-src/domain/transform-artifacts.ts:
// artifact assembly (tfvars in json/hcl format, imports files, lookup
// sidecars, generated-bindings sidecars) and the transactional filesystem
// write path (legacy single-artifact publish plus the batch
// preflight/publish/rollback machinery). Vectors: the pure-library subset of
// node-tests/transform-runtime-artifacts.test.ts, ported in
// transform_artifacts_test.go -- see that file's doc comment for exactly
// which of that source's tests are ported here versus skipped as
// runner/CLI-level (they exercise node-src/domain/transform-runner.ts,
// node-src/domain/import-staging.ts, node-src/domain/plan-lifecycle.ts, or
// node-src/metadata/loader.ts's pack loading, none of which are part of
// this package's scope or dependency set).
//
// # expression-bindings.ts is NOT consumed here
//
// This task's brief anticipated transform-artifacts.ts might consume
// node-src/domain/expression-bindings.ts for its "binding context"
// handling. It does not: grepping node-src/domain/transform-artifacts.ts's
// imports (reproduced below) shows no reference to expression-bindings.js,
// and grepping node-src/ for "expression-bindings" shows its only importer
// is node-src/domain/environment-generator.ts, an unrelated consumer
// outside this port's scope. BindingContext/TransformReferenceSpec and the
// same-root/cross-state reference-binding derivation logic
// (deriveGeneratedBindings and its helpers, below) are wholly local to
// transform-artifacts.ts itself. No subset of expression-bindings.ts is
// ported by this file; that source remains entirely unaddressed by this
// port and should be flagged to environment-generator.ts's own future
// finisher.
//
// # Value model
//
// PullTransformResult.Items/Originals use map[string]map[string]any (an
// item key to its field record) rather than this package's usual
// map[string]any-rooted canonjson.Value tree, matching
// node-src/domain/pull-transform.ts's own
// `Readonly<Record<string, Readonly<Record<string, unknown>>>>` shape one
// level more specifically typed than a bare `unknown`. Each field record
// (map[string]any) is itself exactly this package's canonjson.Value model,
// and converts freely to/from a bare map[string]any wherever a
// canonjson-rooted function (RenderTfvarsHcl, canonjson.RenderLosslessArtifactJSON)
// needs one -- see recordFromItems.
//
// # Local dependency: PullTransformResult
//
// PullTransformResult below is a LOCAL, minimal port of the interface of
// the same name in node-src/domain/pull-transform.ts, whose full port
// belongs to the sibling finisher's go/internal/transform package for this
// wave (per this task's brief: "a sibling finisher owns go/internal/transform").
// Only the three fields transform-artifacts.ts's write path actually reads
// (Items, Originals, Drops) are ported; Drops is carried for structural
// parity even though no function in this file's slice of
// transform-artifacts.ts reads it (grep confirms: transform-artifacts.ts
// never accesses `.drops` on a PullTransformResult).
import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// PullTransformResult is a LOCAL minimal port of the PullTransformResult
// interface in node-src/domain/pull-transform.ts. See this file's
// package-level doc comment.
type PullTransformResult struct {
	Items     map[string]map[string]any
	Originals map[string]map[string]any
	Drops     []string
}

// TransformReferenceSpec is the Go analogue of the TransformReferenceSpec
// interface in node-src/domain/transform-artifacts.ts. NameField (the TS
// interface's `name_field`) is never read by any function this file ports
// -- grepping transform-artifacts.ts confirms `.name_field`/`name_field`
// only ever appears in this interface's own declaration -- but is kept for
// structural fidelity with callers (outside this slice) that build
// References maps.
type TransformReferenceSpec struct {
	NameField string
	Referent  string
}

// BindingContext is the Go analogue of the BindingContext interface in
// node-src/domain/transform-artifacts.ts. Derived and Generated are the Go
// analogues of its two `ReadonlySet<string>` fields, represented as
// presence-only string sets (map[string]bool) the same way this port
// represents every other TS Set/Readonly<Set> it encounters.
type BindingContext struct {
	Mode          deployment.ReferenceBindingMode
	Derived       map[string]bool
	Generated     map[string]bool
	ResourceRoots map[string]string
	References    map[string]TransformReferenceSpec
}

// GeneratedBindingsResult is the Go analogue of the GeneratedBindingsResult
// interface in node-src/domain/transform-artifacts.ts. Resources is the Go
// analogue of `data.resources` (this file inlines the TS interface's extra
// `{data: {resources: ...}}` nesting level away: RenderGeneratedBindings
// re-adds the "resources" JSON key itself); every entry is a
// canonjson.Value-shaped map[string]any record of field-path to
// {"expression": string, "reason": string}, so it feeds
// canonjson.RenderLosslessArtifactJSON directly.
type GeneratedBindingsResult struct {
	Resources map[string]any
	Notes     []string
}

// TransformArtifactPaths is the Go analogue of the TransformArtifactPaths
// interface in node-src/domain/transform-artifacts.ts.
type TransformArtifactPaths struct {
	Config            string
	GeneratedBindings string
	Imports           string
	Lookup            string
	Moves             string
	StaleConfig       string
}

// TransformArtifactWriteResult is the Go analogue of the
// TransformArtifactWriteResult interface in
// node-src/domain/transform-artifacts.ts.
type TransformArtifactWriteResult struct {
	Paths   TransformArtifactPaths
	Written []string
	Removed []string
}

// TransformLookupData is the Go analogue of the TransformLookupData
// interface in node-src/domain/transform-artifacts.ts.
type TransformLookupData struct {
	ByID    map[string]string
	KeyByID map[string]string
}

// TransformArtifactCompileOptions is the Go analogue of the
// TransformArtifactCompileOptions interface in
// node-src/domain/transform-artifacts.ts.
//
// LookupNameField's *string nil-ness carries the TS `string | null` union
// (a required field that can be explicitly null). LookupOverrides carries
// two independent states per referent key exactly the way the TS
// `Readonly<Record<string, TransformLookupData | null>> | undefined`
// option does: a Go nil map (like a TS `undefined` option) or an absent
// map key both mean "no override, fall through to disk"; a present key
// with a nil *TransformLookupData value means an explicit TS `null`
// override, suppressing a stale on-disk lookup sidecar. Every Go map
// lookup in this file that queries LookupOverrides relies on this: a nil
// Go map and a "no key present" Go map behave identically on read, so no
// separate presence flag is threaded alongside it (unlike the TS source's
// `options.lookupOverrides !== undefined && own(...)` two-part guard,
// whose two conditions collapse to one Go map access).
type TransformArtifactCompileOptions struct {
	BindingContext         BindingContext
	Deployment             deployment.Deployment
	LookupNameField        *string
	RemoveLookupWhenAbsent bool
	LookupOverrides        map[string]*TransformLookupData
	OnDiagnostic           func(string)
	Override               map[string]any
	References             map[string]TransformReferenceSpec
	ResourceType           string
	Result                 PullTransformResult
	Tenant                 string
	VariableName           string
}

// CompiledTransformArtifacts is the Go analogue of the opaque
// CompiledTransformArtifacts interface in
// node-src/domain/transform-artifacts.ts: "fully preflighted transform
// output; pass this to the publish functions."
type CompiledTransformArtifacts struct {
	Binding                GeneratedBindingsResult
	ConfigText             string
	ExistingMoves          *string
	LookupText             *string
	RemoveLookupWhenAbsent bool
	Moves                  ImportMoveDerivation
	NewImports             string
	OnDiagnostic           func(string)
	Paths                  TransformArtifactPaths
	RenderedMoves          *string
	ResourceType           string
}

// batchMutationKind is the Go analogue of the "remove" | "write" literal
// union node-src/domain/transform-artifacts.ts's BatchArtifactMutation.kind
// field carries.
type batchMutationKind string

const (
	mutationRemove batchMutationKind = "remove"
	mutationWrite  batchMutationKind = "write"
)

// batchArtifactMutation is the Go analogue of the BatchArtifactMutation
// type in node-src/domain/transform-artifacts.ts.
type batchArtifactMutation struct {
	contents     *string
	kind         batchMutationKind
	resourceType string
	target       string
}

// preparedBatchArtifactMutation is the Go analogue of
// PreparedBatchArtifactMutation.
type preparedBatchArtifactMutation struct {
	batchArtifactMutation
	backupPath string
	stagePath  *string
}

// appliedBatchArtifactMutation is the Go analogue of
// AppliedBatchArtifactMutation.
type appliedBatchArtifactMutation struct {
	preparedBatchArtifactMutation
	hadOriginal bool
}

// BatchArtifactMutationRef is the Go analogue of the read-only mutation
// view node-src/domain/transform-artifacts.ts's BatchArtifactCommitHook
// type receives: `Readonly<Pick<BatchArtifactMutation, "kind" |
// "resourceType" | "target">>`.
type BatchArtifactMutationRef struct {
	Kind         batchMutationKind
	ResourceType string
	Target       string
}

// BatchArtifactCommitHook is the Go analogue of the BatchArtifactCommitHook
// type in node-src/domain/transform-artifacts.ts: `@internal Test-only
// fault injection for batch publication rollback coverage`. The TS type's
// `(mutation, phase) => void | Promise<void>` (which can throw/reject) is
// this func's `error` return.
type BatchArtifactCommitHook func(mutation BatchArtifactMutationRef, phase string) error

var (
	batchArtifactCommitHookMu  sync.Mutex
	batchArtifactCommitHook    BatchArtifactCommitHook
	batchArtifactCommitHookGen int
)

// InstallTransformArtifactBatchCommitHookForTests ports
// installTransformArtifactBatchCommitHookForTests from
// node-src/domain/transform-artifacts.ts. Only one hook may be installed at
// a time (mirroring the TS source's own single-slot `let
// batchArtifactCommitHook` guard); the returned cleanup func uninstalls it,
// but -- like the TS source's `if (batchArtifactCommitHook === hook)`
// identity check -- only if a later Install call has not since replaced
// it. Go func values are not comparable, so this uses a generation counter
// as this file's Go analogue of that identity comparison.
//
// Deliberately a Go idiom departure from the TS source: TS's
// synchronous-throw-on-already-installed becomes a returned error here
// rather than a panic, since this is a test-only helper with no bytes at
// stake and idiomatic Go callers expect an error return, not a panic, from
// a fallible setup function.
func InstallTransformArtifactBatchCommitHookForTests(hook BatchArtifactCommitHook) (func(), error) {
	batchArtifactCommitHookMu.Lock()
	defer batchArtifactCommitHookMu.Unlock()
	if batchArtifactCommitHook != nil {
		return nil, errors.New("a transform artifact batch commit test hook is already installed")
	}
	batchArtifactCommitHook = hook
	batchArtifactCommitHookGen++
	generation := batchArtifactCommitHookGen
	return func() {
		batchArtifactCommitHookMu.Lock()
		defer batchArtifactCommitHookMu.Unlock()
		if batchArtifactCommitHookGen == generation {
			batchArtifactCommitHook = nil
		}
	}, nil
}

func runBatchArtifactCommitHook(mutation batchArtifactMutation, phase string) error {
	batchArtifactCommitHookMu.Lock()
	hook := batchArtifactCommitHook
	batchArtifactCommitHookMu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(BatchArtifactMutationRef{
		Kind:         mutation.kind,
		ResourceType: mutation.resourceType,
		Target:       mutation.target,
	}, phase)
}

// multiError is this file's Go analogue of the two (non-
// BatchArtifactRollbackError) `throw new AggregateError(...)` call sites in
// node-src/domain/transform-artifacts.ts: one fixed message plus every
// wrapped failure, retrievable via Unwrap() []error (the standard Go 1.20+
// multi-error convention) for any caller that wants to inspect the
// individual failures the way JS code inspects AggregateError.errors.
type multiError struct {
	message string
	errs    []error
}

func (e *multiError) Error() string   { return e.message }
func (e *multiError) Unwrap() []error { return e.errs }

// BatchArtifactRollbackError is the Go analogue of the
// BatchArtifactRollbackError class in
// node-src/domain/transform-artifacts.ts: publication failed AND the
// rollback that followed also failed, leaving transaction backups on disk
// for operator recovery.
type BatchArtifactRollbackError struct {
	Errors                 []error
	TransactionDirectories []string
}

func (e *BatchArtifactRollbackError) Error() string {
	return fmt.Sprintf(
		"transform artifact batch publication and rollback both failed; recovery data preserved in %s",
		strings.Join(e.TransactionDirectories, ", "),
	)
}

func (e *BatchArtifactRollbackError) Unwrap() []error { return e.Errors }

// jsonQuote approximates JSON.stringify's double-quoted string escaping,
// used only for human-readable diagnostic/error text in this file (never a
// byte-gated artifact contract -- artifact bytes always go through
// canonjson.RenderLosslessArtifactJSON or RenderTfvarsHcl instead).
// strconv.Quote's Go escaping (backslash/quote/control-character escapes,
// printable Unicode left literal) matches JSON.stringify closely enough
// for these messages; the one documented divergence is that Go additionally
// has \x/\U escape forms JSON.stringify would spell as \u, which cannot
// arise for any value this file quotes (resource types, template text,
// Terraform addresses -- all either validated identifiers or text that
// round-trips through this same escaping already).
func jsonQuote(s string) string { return strconv.Quote(s) }

// pythonTransformString ports pythonTransformString from
// node-src/domain/transform-artifacts.ts: "match the scalar spelling used
// by Python str() in transform identities." Every failure here is a plain
// error (the TS source throws a plain TypeError, not a ProcessFailure).
func pythonTransformString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case nil:
		return "None", nil
	case json.Number:
		if token, err := canonjson.CanonicalNumberToken(string(v)); err == nil {
			return token, nil
		}
	case float64:
		if isSafeInteger(v) {
			if v == 0 && math.Signbit(v) {
				return "0", nil
			}
			return strconv.FormatInt(int64(v), 10), nil
		}
	}
	return "", errTransformIdentityScalar
}

var errTransformIdentityScalar = errors.New("transform identity must be a scalar JSON value")

var importFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// formatImportTemplate ports formatImportTemplate from
// node-src/domain/transform-artifacts.ts: "match Python str.format's field
// and doubled-brace behavior for import IDs." Indexed by Go string byte
// offset rather than UTF-16 code unit; safe here for the same reason
// import_moves.go's package-level indexing note gives -- every structural
// character this grammar's scanner tests for ("{" and "}") is pure ASCII,
// so byte-indexed and UTF-16-unit-indexed scanning visit the same
// boundaries and copy the same non-ASCII content through unchanged.
func formatImportTemplate(template string, original map[string]any) (string, error) {
	var output strings.Builder
	index := 0
	for index < len(template) {
		character := template[index]
		if character == '{' && index+1 < len(template) && template[index+1] == '{' {
			output.WriteByte('{')
			index += 2
			continue
		}
		if character == '}' && index+1 < len(template) && template[index+1] == '}' {
			output.WriteByte('}')
			index += 2
			continue
		}
		if character != '{' {
			if character == '}' {
				return "", fmt.Errorf("invalid import_id template %s", jsonQuote(template))
			}
			output.WriteByte(character)
			index++
			continue
		}
		rest := template[index+1:]
		relativeEnd := strings.IndexByte(rest, '}')
		if relativeEnd < 0 {
			return "", fmt.Errorf("invalid import_id template %s", jsonQuote(template))
		}
		end := index + 1 + relativeEnd
		field := template[index+1 : end]
		value, ok := original[field]
		if !importFieldPattern.MatchString(field) || !ok {
			return "", fmt.Errorf(
				"import_id template %s references missing field %s",
				jsonQuote(template), jsonQuote(field),
			)
		}
		rendered, err := pythonTransformString(value)
		if err != nil {
			return "", err
		}
		output.WriteString(rendered)
		index = end + 1
	}
	return output.String(), nil
}

// renderTransformImports ports renderTransformImports from
// node-src/domain/transform-artifacts.ts. template nil matches the TS
// source's `options.template ?? "{id}"` default.
func renderTransformImports(resourceType string, originals map[string]map[string]any, template *string) (string, error) {
	tmpl := "{id}"
	if template != nil {
		tmpl = *template
	}
	keys := canonjson.SortedStrings(mapKeys(originals))
	pairs := make([]GeneratedImportPair, 0, len(keys))
	for _, key := range keys {
		original, ok := originals[key]
		if !ok {
			return "", fmt.Errorf("missing original transform item %s", jsonQuote(key))
		}
		importID, err := formatImportTemplate(tmpl, original)
		if err != nil {
			return "", err
		}
		pairs = append(pairs, GeneratedImportPair{Key: key, ImportID: importID})
	}
	return RenderGeneratedImports(resourceType, pairs)
}

// lookupIdentity ports lookupIdentity from
// node-src/domain/transform-artifacts.ts.
func lookupIdentity(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if s, ok := value.(string); ok && s == "" {
		return nil, nil
	}
	rendered, err := pythonTransformString(value)
	if err != nil {
		return nil, err
	}
	return &rendered, nil
}

// RenderTransformLookup ports renderTransformLookup from
// node-src/domain/transform-artifacts.ts: "render Python's transform
// lookup sidecar, including last-key-wins IDs."
func RenderTransformLookup(items, originals map[string]map[string]any, nameField string) (string, error) {
	byID := map[string]any{}
	keyByID := map[string]any{}
	for _, key := range canonjson.SortedStrings(mapKeys(items)) {
		projected, ok := items[key]
		if !ok {
			continue
		}
		merged := map[string]any{}
		for field, value := range originals[key] {
			merged[field] = value
		}
		for field, value := range projected {
			merged[field] = value
		}
		ident, err := lookupIdentity(merged["id"])
		if err != nil {
			return "", err
		}
		if ident == nil {
			continue
		}
		display, isString := merged[nameField].(string)
		text := "<unknown>"
		if isString && strings.TrimSpace(display) != "" {
			text = display
		}
		byID[*ident] = text
		keyByID[*ident] = key
	}
	var payload map[string]any
	if len(keyByID) == 0 {
		payload = byID
	} else {
		payload = map[string]any{"by_id": byID, "key_by_id": keyByID}
	}
	return canonjson.RenderLosslessArtifactJSON(payload)
}

func asObject(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

// ParseLookupSidecar ports parseLookupSidecar from
// node-src/domain/transform-artifacts.ts.
func ParseLookupSidecar(value any) (TransformLookupData, error) {
	root, ok := asObject(value)
	if !ok {
		return TransformLookupData{}, errors.New("lookup sidecar must contain a JSON object")
	}
	nestedByID, hasNestedByID := asObject(root["by_id"])
	nestedKeys, hasNestedKeys := asObject(root["key_by_id"])
	rawByID := root
	if hasNestedByID {
		rawByID = nestedByID
	}
	byID := map[string]string{}
	keyByID := map[string]string{}
	for key, display := range rawByID {
		if s, ok := display.(string); ok {
			byID[key] = s
		} else {
			byID[key] = "<unknown>"
		}
	}
	if hasNestedKeys {
		for key, itemKey := range nestedKeys {
			if s, ok := itemKey.(string); ok && s != "" {
				keyByID[key] = s
			}
		}
	}
	return TransformLookupData{ByID: byID, KeyByID: keyByID}, nil
}

var integerTokenPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

// integerToken ports integerToken from
// node-src/domain/transform-artifacts.ts, returning nil where the TS source
// returns null.
func integerToken(value any) *string {
	if n, ok := value.(json.Number); ok {
		s := string(n)
		if !integerTokenPattern.MatchString(s) {
			return nil
		}
		bi, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil
		}
		token := bi.String()
		return &token
	}
	if f, ok := value.(float64); ok && isSafeInteger(f) {
		token := strconv.FormatInt(int64(f), 10)
		return &token
	}
	return nil
}

// zeroSentinel ports zeroSentinel from
// node-src/domain/transform-artifacts.ts.
func zeroSentinel(value any) bool {
	token := integerToken(value)
	return token != nil && *token == "0"
}

// bindableListElement ports bindableListElement from
// node-src/domain/transform-artifacts.ts.
func bindableListElement(value any) bool {
	if s, ok := value.(string); ok && s != "" {
		return true
	}
	return integerToken(value) != nil
}

// sameRoot ports sameRoot from node-src/domain/transform-artifacts.ts.
func sameRoot(resourceType, referent string, context BindingContext) bool {
	if resourceType == referent {
		return false
	}
	if !context.Generated[resourceType] || !context.Generated[referent] {
		return false
	}
	if context.Derived[resourceType] || context.Derived[referent] {
		return false
	}
	referrerRoot, ok := context.ResourceRoots[resourceType]
	if !ok {
		return false
	}
	referentRoot, ok := context.ResourceRoots[referent]
	return ok && referrerRoot == referentRoot
}

// bindableReference ports bindableReference from
// node-src/domain/transform-artifacts.ts.
func bindableReference(resourceType, referent string, context BindingContext) bool {
	if resourceType == referent {
		return false
	}
	if !context.Generated[resourceType] || !context.Generated[referent] {
		return false
	}
	if context.Derived[resourceType] || context.Derived[referent] {
		return false
	}
	referrerRoot, okReferrer := context.ResourceRoots[resourceType]
	referentRoot, okReferent := context.ResourceRoots[referent]
	if !okReferrer || !okReferent {
		return false
	}
	return context.Mode == deployment.ReferenceBindingCrossState || referrerRoot == referentRoot
}

// fieldCandidate is the Go analogue of fieldCandidates's anonymous element
// type in node-src/domain/transform-artifacts.ts.
type fieldCandidate struct {
	key   string
	path  string
	value any
}

var identifierSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fieldCandidates ports fieldCandidates from
// node-src/domain/transform-artifacts.ts.
func fieldCandidates(items map[string]map[string]any, field string) []fieldCandidate {
	segments := strings.Split(field, ".")
	dotted := len(segments) > 1
	if dotted {
		for _, segment := range segments {
			if !identifierSegmentPattern.MatchString(segment) {
				dotted = false
				break
			}
		}
	}
	var candidates []fieldCandidate
	if dotted {
		var visit func(key string, value any, segmentIndex int, concretePath string)
		visit = func(key string, value any, segmentIndex int, concretePath string) {
			if segmentIndex == len(segments) {
				if value != nil {
					candidates = append(candidates, fieldCandidate{key: key, path: concretePath, value: value})
				}
				return
			}
			if arr, ok := value.([]any); ok {
				for index, child := range arr {
					if child != nil {
						visit(key, child, segmentIndex, fmt.Sprintf("%s[%d]", concretePath, index))
					}
				}
				return
			}
			item, ok := value.(map[string]any)
			if !ok {
				return
			}
			segment := segments[segmentIndex]
			childValue, present := item[segment]
			if !present {
				return
			}
			nextPath := segment
			if concretePath != "" {
				nextPath = concretePath + "." + segment
			}
			visit(key, childValue, segmentIndex+1, nextPath)
		}
		for _, key := range canonjson.SortedStrings(mapKeys(items)) {
			visit(key, items[key], 0, "")
		}
		return candidates
	}
	for _, key := range canonjson.SortedStrings(mapKeys(items)) {
		item, ok := items[key]
		if !ok {
			continue
		}
		value, present := item[field]
		if !present || value == nil {
			continue
		}
		if arr, ok := value.([]any); ok {
			for index, child := range arr {
				if child != nil {
					candidates = append(candidates, fieldCandidate{key: key, path: fmt.Sprintf("%s[%d]", field, index), value: child})
				}
			}
		} else {
			candidates = append(candidates, fieldCandidate{key: key, path: field, value: value})
		}
	}
	return candidates
}

// generatedBindingsBuilder holds the mutable state
// deriveGeneratedBindings's TS closures (resolve, bindValue, count) share
// via lexical capture; Go has no closures-over-enclosing-locals convenient
// enough for this deeply nested shape, so this struct plus its methods is
// this file's direct analogue.
type generatedBindingsBuilder struct {
	resourceType string
	context      BindingContext
	resources    map[string]any
	notes        []string
	bound        int
	skipped      int
	reasons      map[string]int
}

func (b *generatedBindingsBuilder) count(reason string, amount int) {
	if b.reasons == nil {
		b.reasons = map[string]int{}
	}
	b.reasons[reason] += amount
}

func (b *generatedBindingsBuilder) note(format string, args ...any) {
	b.notes = append(b.notes, fmt.Sprintf(format, args...))
}

// resolve ports deriveGeneratedBindings's local `resolve` closure.
func (b *generatedBindingsBuilder) resolve(spec TransformReferenceSpec, keyMap map[string]string, key, fieldPath string, value any) (*string, error) {
	ident, err := pythonTransformString(value)
	if err != nil {
		return nil, err
	}
	referentKey, ok := keyMap[ident]
	if !ok {
		b.count("id_absent", 1)
		b.note("%s.%s.%s value %s skipped; id is absent from %s lookup", b.resourceType, key, fieldPath, jsonQuote(ident), spec.Referent)
		return nil, nil
	}
	if strings.Contains(referentKey, "${") || strings.Contains(referentKey, "%{") {
		b.count("unsafe_key", 1)
		b.note("%s.%s.%s value %s skipped; referent key contains a template interpolation", b.resourceType, key, fieldPath, jsonQuote(ident))
		return nil, nil
	}
	if sameRoot(b.resourceType, spec.Referent, b.context) {
		quoted, err := RenderHclQuotedString(referentKey)
		if err != nil {
			return nil, err
		}
		expr := "module." + spec.Referent + ".items[" + quoted + "].id"
		return &expr, nil
	}
	referentRoot, ok := b.context.ResourceRoots[spec.Referent]
	if !ok {
		return nil, fmt.Errorf("cross-state reference %s has no deployment root", spec.Referent)
	}
	quoted, err := RenderHclQuotedString(referentKey)
	if err != nil {
		return nil, err
	}
	expr := "data.terraform_remote_state." + referentRoot + ".outputs.infrawright_reference_ids." + spec.Referent + "[" + quoted + "]"
	return &expr, nil
}

// bindValue ports deriveGeneratedBindings's local `bindValue` closure.
func (b *generatedBindingsBuilder) bindValue(spec TransformReferenceSpec, keyMap map[string]string, key, fieldPath string, value any) (*string, error) {
	if arr, ok := value.([]any); ok {
		bindable := make([]any, 0, len(arr))
		for _, child := range arr {
			if !zeroSentinel(child) {
				bindable = append(bindable, child)
			}
		}
		hadZero := len(bindable) != len(arr)
		for _, child := range bindable {
			if !bindableListElement(child) {
				b.count("unbindable_list", 1)
				b.skipped++
				b.note("%s.%s.%s skipped; list has null or unbindable elements", b.resourceType, key, fieldPath)
				return nil, nil
			}
		}
		var fragments []string
		boundAny := false
		for index, child := range arr {
			if zeroSentinel(child) {
				continue
			}
			resolved, err := b.resolve(spec, keyMap, key, fmt.Sprintf("%s[%d]", fieldPath, index), child)
			if err != nil {
				return nil, err
			}
			if resolved == nil {
				b.skipped++
				str, err := pythonTransformString(child)
				if err != nil {
					return nil, err
				}
				quoted, err := RenderHclQuotedString(str)
				if err != nil {
					return nil, err
				}
				fragments = append(fragments, quoted)
			} else {
				b.bound++
				boundAny = true
				fragments = append(fragments, *resolved)
			}
		}
		if boundAny {
			expr := "[" + strings.Join(fragments, ", ") + "]"
			return &expr, nil
		}
		if hadZero && len(bindable) == 0 {
			expr := "[]"
			return &expr, nil
		}
		return nil, nil
	}
	if value == nil {
		return nil, nil
	}
	expression, err := b.resolve(spec, keyMap, key, fieldPath, value)
	if err != nil {
		return nil, err
	}
	if expression == nil {
		b.skipped++
	} else {
		b.bound++
	}
	return expression, nil
}

func (b *generatedBindingsBuilder) reasonFor(spec TransformReferenceSpec) string {
	if sameRoot(b.resourceType, spec.Referent, b.context) {
		return "group-local reference binding via " + spec.Referent + ".items"
	}
	return "cross-state reference binding via " + spec.Referent + " root output"
}

func (b *generatedBindingsBuilder) assign(key, fieldPath, expression, reason string) {
	address := b.resourceType + "." + key
	fields, ok := b.resources[address].(map[string]any)
	if !ok {
		fields = map[string]any{}
		b.resources[address] = fields
	}
	fields[fieldPath] = map[string]any{"expression": expression, "reason": reason}
}

// DeriveGeneratedBindings ports deriveGeneratedBindings from
// node-src/domain/transform-artifacts.ts: "pure same-root binding
// derivation; lookup reads stay in the caller."
func DeriveGeneratedBindings(context BindingContext, items map[string]map[string]any, lookupKeys map[string]map[string]string, resourceType string) (GeneratedBindingsResult, error) {
	b := &generatedBindingsBuilder{
		resourceType: resourceType,
		context:      context,
		resources:    map[string]any{},
	}
	if context.Mode == deployment.ReferenceBindingDisabled {
		return GeneratedBindingsResult{Resources: b.resources, Notes: b.notes}, nil
	}
	for _, field := range canonjson.SortedStrings(mapKeys(context.References)) {
		spec, ok := context.References[field]
		if !ok {
			continue
		}
		candidates := fieldCandidates(items, field)
		if resourceType == spec.Referent {
			if len(candidates) > 0 {
				b.count("self_reference", len(candidates))
				b.skipped += len(candidates)
				b.note("%s.%s skipped; self-referential bindings would create a Terraform cycle", resourceType, field)
			}
			continue
		}
		if !bindableReference(resourceType, spec.Referent, context) {
			continue
		}
		keyMap := lookupKeys[spec.Referent]
		if keyMap == nil {
			if len(candidates) > 0 {
				b.count("missing_lookup", len(candidates))
				b.skipped += len(candidates)
				b.note("%s.%s skipped; lookup for %s is missing", resourceType, field, spec.Referent)
			}
			continue
		}
		if len(keyMap) == 0 {
			if len(candidates) > 0 {
				b.count("key_map_unavailable", len(candidates))
				b.skipped += len(candidates)
				b.note("%s.%s skipped; lookup for %s has no key_by_id map", resourceType, field, spec.Referent)
			}
			continue
		}
		reason := b.reasonFor(spec)
		if strings.Contains(field, ".") {
			for _, candidate := range candidates {
				expression, err := b.bindValue(spec, keyMap, candidate.key, candidate.path, candidate.value)
				if err != nil {
					return GeneratedBindingsResult{}, err
				}
				if expression == nil {
					continue
				}
				b.assign(candidate.key, candidate.path, *expression, reason)
			}
			continue
		}
		for _, key := range canonjson.SortedStrings(mapKeys(items)) {
			item, ok := items[key]
			if !ok {
				continue
			}
			value, present := item[field]
			if !present {
				continue
			}
			expression, err := b.bindValue(spec, keyMap, key, field, value)
			if err != nil {
				return GeneratedBindingsResult{}, err
			}
			if expression == nil {
				continue
			}
			b.assign(key, field, *expression, reason)
		}
	}
	if b.bound > 0 || b.skipped > 0 {
		reasonKeys := canonjson.SortedStrings(mapKeys(b.reasons))
		parts := make([]string, len(reasonKeys))
		for i, reason := range reasonKeys {
			parts[i] = fmt.Sprintf("%s=%d", reason, b.reasons[reason])
		}
		reasonText := strings.Join(parts, ", ")
		if reasonText == "" {
			b.note("%s: %d bound, %d skipped", resourceType, b.bound, b.skipped)
		} else {
			b.note("%s: %d bound, %d skipped (%s)", resourceType, b.bound, b.skipped, reasonText)
		}
	}
	return GeneratedBindingsResult{Resources: b.resources, Notes: b.notes}, nil
}

// RenderGeneratedBindings ports renderGeneratedBindings from
// node-src/domain/transform-artifacts.ts.
func RenderGeneratedBindings(resources map[string]any) (string, error) {
	return canonjson.RenderLosslessArtifactJSON(map[string]any{"resources": resources})
}

// ComputeTransformArtifactPaths ports transformArtifactPaths from
// node-src/domain/transform-artifacts.ts. Named ComputeTransformArtifactPaths
// rather than TransformArtifactPaths (the TS function and its return-type
// interface share one lowercase/uppercase-only name in TS, which Go's
// single, case-sensitive-but-not-namespace-separated identifier space for
// types and funcs cannot reproduce; the TransformArtifactPaths identifier
// above is reserved for the struct).
func ComputeTransformArtifactPaths(dep deployment.Deployment, resourceType, tenant string) (TransformArtifactPaths, error) {
	format, err := deployment.DeploymentTfvarsFormat(dep)
	if err != nil {
		return TransformArtifactPaths{}, err
	}
	configDirectory, err := deployment.DeploymentConfigDir(dep, tenant)
	if err != nil {
		return TransformArtifactPaths{}, err
	}
	importsDirectory, err := deployment.DeploymentImportsDir(dep, tenant)
	if err != nil {
		return TransformArtifactPaths{}, err
	}
	ext := ".auto.tfvars.json"
	if format == "hcl" {
		ext = ".auto.tfvars"
	}
	config := path.Join(configDirectory, resourceType+ext)
	staleConfig := strings.TrimSuffix(config, ".json")
	if format == "hcl" {
		staleConfig = config + ".json"
	}
	return TransformArtifactPaths{
		Config:            config,
		StaleConfig:       staleConfig,
		GeneratedBindings: path.Join(configDirectory, resourceType+".generated.expressions.json"),
		Imports:           path.Join(importsDirectory, resourceType+"_imports.tf"),
		Lookup:            path.Join(configDirectory, resourceType+".lookup.json"),
		Moves:             path.Join(importsDirectory, resourceType+"_moves.tf"),
	}, nil
}

// removeIfPresent ports removeIfPresent from
// node-src/domain/transform-artifacts.ts.
func removeIfPresent(file string) (bool, error) {
	err := os.Remove(file)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// readOptionalUtf8 ports readOptionalUtf8 from node-src/io/files.ts, kept
// package-private per this port's per-package convention for this small
// helper -- see go/internal/deployment/deployment.go's own copy, which
// this one mirrors exactly (including its procerr.ProcessFailure codes:
// unlike node-src/domain/transform-artifacts.ts's own throws, which are all
// plain Error/TypeError, io/files.ts's readOptionalUtf8 does raise
// ProcessFailure, and this file's task brief requires every
// ProcessFailure code/message be ported via procerr verbatim).
func readOptionalUtf8(filePath, label string) (*string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "READ_FAILED",
			Category: procerr.CategoryIO,
			Message:  fmt.Sprintf("unable to read %s", label),
		})
	}
	if !utf8.Valid(content) {
		return nil, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "INVALID_UTF8",
			Category: procerr.CategoryDomain,
			Message:  fmt.Sprintf("%s is not valid UTF-8", label),
		})
	}
	text := string(content)
	return &text, nil
}

// loadLookup ports loadLookup from node-src/domain/transform-artifacts.ts.
func loadLookup(file string) (*TransformLookupData, error) {
	text, err := readOptionalUtf8(file, fmt.Sprintf("lookup for %s", path.Base(file)))
	if err != nil {
		return nil, err
	}
	if text == nil {
		return nil, nil
	}
	value, err := canonjson.ParseDataJSONLosslessly(*text)
	if err != nil {
		return nil, err
	}
	data, err := ParseLookupSidecar(value)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// resolveLookup ports resolveLookup from
// node-src/domain/transform-artifacts.ts. See TransformArtifactCompileOptions's
// LookupOverrides doc comment for why no separate "overrides provided"
// flag is threaded here.
func resolveLookup(configDirectory, referent string, overrides map[string]*TransformLookupData) (*TransformLookupData, error) {
	if data, ok := overrides[referent]; ok {
		return data, nil
	}
	return loadLookup(path.Join(configDirectory, referent+".lookup.json"))
}

var systemConstantPattern = regexp.MustCompile(`^[A-Z0-9_]+$`)

// systemConstant ports systemConstant from
// node-src/domain/transform-artifacts.ts.
func systemConstant(value string) bool {
	return !strings.HasPrefix(value, "CUSTOM_") && value == strings.ToUpper(value) && systemConstantPattern.MatchString(value)
}

// displayFor ports displayFor from node-src/domain/transform-artifacts.ts.
func displayFor(value any, mapping map[string]string) (string, error) {
	ident, err := pythonTransformString(value)
	if err != nil {
		return "", err
	}
	if display, ok := mapping[ident]; ok {
		return display, nil
	}
	if systemConstant(ident) {
		return ident, nil
	}
	return "<unknown>", nil
}

// deriveHclComments ports deriveHclComments from
// node-src/domain/transform-artifacts.ts.
func deriveHclComments(
	configDirectory string,
	items map[string]map[string]any,
	references map[string]TransformReferenceSpec,
	lookupOverrides map[string]*TransformLookupData,
) (HclTfvarsComments, error) {
	comments := HclTfvarsComments{}
	// lookups caches resolveLookup's result per referent, including an
	// explicit nil (a referent with no resolvable lookup): the map's own
	// two-value read (`lookup, resolved := lookups[...]`) already
	// distinguishes "resolved to nil" from "never resolved" (a present key
	// with a nil value still reports resolved=true), so no separate
	// presence-tracking map is needed alongside it -- unlike the TS
	// source's `lookup === undefined && !lookups.has(referent)` guard,
	// which needs both halves only because a bare `Map.get` can't
	// distinguish "key absent" from "key present with an undefined value"
	// on its own.
	lookups := map[string]*TransformLookupData{}
	fieldKeys := canonjson.SortedStrings(mapKeys(references))
	for _, itemKey := range canonjson.SortedStrings(mapKeys(items)) {
		item, ok := items[itemKey]
		if !ok {
			continue
		}
		for _, field := range fieldKeys {
			value, present := item[field]
			if !present || value == nil {
				continue
			}
			spec, ok := references[field]
			if !ok {
				continue
			}
			lookup, resolved := lookups[spec.Referent]
			if !resolved {
				var err error
				lookup, err = resolveLookup(configDirectory, spec.Referent, lookupOverrides)
				if err != nil {
					return nil, err
				}
				lookups[spec.Referent] = lookup
			}
			if lookup == nil {
				continue
			}
			commentFor := func(child any) (string, error) {
				display, err := displayFor(child, lookup.ByID)
				if err != nil {
					return "", err
				}
				display = strings.ReplaceAll(display, "\n", " ")
				display = strings.ReplaceAll(display, "\r", " ")
				return display, nil
			}
			if arr, isArr := value.([]any); isArr {
				for index, child := range arr {
					if child == nil {
						continue
					}
					text, err := commentFor(child)
					if err != nil {
						return nil, err
					}
					idx := index
					comments[HclTfvarsCommentKey(itemKey, field, &idx)] = text
				}
			} else {
				text, err := commentFor(value)
				if err != nil {
					return nil, err
				}
				comments[HclTfvarsCommentKey(itemKey, field, nil)] = text
			}
		}
	}
	return comments, nil
}

// recordFromItems converts items (this package's map[string]map[string]any
// representation of PullTransformResult.Items/Originals) into the plain
// map[string]any canonjson.Value shape RenderTfvarsHcl and
// canonjson.RenderLosslessArtifactJSON expect. No deep copy is needed: each
// value is already, itself, a map[string]any.
func recordFromItems(items map[string]map[string]any) map[string]any {
	out := make(map[string]any, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

// renderDeploymentTfvars ports renderDeploymentTfvars from
// node-src/domain/transform-artifacts.ts.
func renderDeploymentTfvars(
	dep deployment.Deployment,
	items map[string]map[string]any,
	references map[string]TransformReferenceSpec,
	resourceType, tenant, variableName string,
	lookupOverrides map[string]*TransformLookupData,
) (string, error) {
	format, err := deployment.DeploymentTfvarsFormat(dep)
	if err != nil {
		return "", err
	}
	if format == "json" {
		return canonjson.RenderLosslessArtifactJSON(map[string]any{variableName: recordFromItems(items)})
	}
	configDirectory, err := deployment.DeploymentConfigDir(dep, tenant)
	if err != nil {
		return "", err
	}
	comments, err := deriveHclComments(configDirectory, items, references, lookupOverrides)
	if err != nil {
		return "", err
	}
	return RenderTfvarsHcl(recordFromItems(items), comments, variableName)
}

// lookupKeyMaps ports lookupKeyMaps from
// node-src/domain/transform-artifacts.ts.
func lookupKeyMaps(
	configDirectory string,
	references map[string]TransformReferenceSpec,
	overrides map[string]*TransformLookupData,
) (map[string]map[string]string, error) {
	output := map[string]map[string]string{}
	resolved := map[string]bool{}
	for _, spec := range references {
		if resolved[spec.Referent] {
			continue
		}
		resolved[spec.Referent] = true
		lookup, err := resolveLookup(configDirectory, spec.Referent, overrides)
		if err != nil {
			return nil, err
		}
		if lookup != nil {
			output[spec.Referent] = lookup.KeyByID
		} else {
			output[spec.Referent] = nil
		}
	}
	return output, nil
}

// compileLookup ports compileLookup from
// node-src/domain/transform-artifacts.ts.
func compileLookup(options TransformArtifactCompileOptions) (*TransformLookupData, *string, error) {
	if options.LookupNameField == nil {
		return nil, nil, nil
	}
	text, err := RenderTransformLookup(options.Result.Items, options.Result.Originals, *options.LookupNameField)
	if err != nil {
		return nil, nil, err
	}
	value, err := canonjson.ParseDataJSONLosslessly(text)
	if err != nil {
		return nil, nil, err
	}
	data, err := ParseLookupSidecar(value)
	if err != nil {
		return nil, nil, err
	}
	return &data, &text, nil
}

// CompileTransformArtifacts ports compileTransformArtifacts from
// node-src/domain/transform-artifacts.ts: "read and validate every input
// needed to publish one ordinary transform artifact set. This function
// never creates, writes, renames, or removes a filesystem entry."
func CompileTransformArtifacts(options TransformArtifactCompileOptions) (CompiledTransformArtifacts, error) {
	paths, err := ComputeTransformArtifactPaths(options.Deployment, options.ResourceType, options.Tenant)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	_, lookupText, err := compileLookup(options)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}

	var template *string
	if s, ok := options.Override["import_id"].(string); ok {
		template = &s
	}
	newImports, err := renderTransformImports(options.ResourceType, options.Result.Originals, template)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}

	oldImports, err := readOptionalUtf8(paths.Imports, options.ResourceType+" imports")
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	var moves ImportMoveDerivation
	if oldImports != nil {
		moves, err = DeriveImportMoves(options.ResourceType, *oldImports, newImports)
		if err != nil {
			return CompiledTransformArtifacts{}, err
		}
	} else {
		moves = ImportMoveDerivation{Moves: []ImportMove{}, Suppressed: []ImportMoveSuppression{}}
	}
	var renderedMoves *string
	if len(moves.Moves) > 0 {
		rendered, err := RenderMovedBlocks(options.ResourceType, moves.Moves)
		if err != nil {
			return CompiledTransformArtifacts{}, err
		}
		renderedMoves = &rendered
	}
	existingMoves, err := readOptionalUtf8(paths.Moves, options.ResourceType+" moves")
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	if existingMoves != nil && renderedMoves != nil && *existingMoves != *renderedMoves {
		return CompiledTransformArtifacts{}, fmt.Errorf(
			"unresolved/conflicting move evidence for %s: %s already contains a different migration; preserve or explicitly resolve it before generating another rename",
			options.ResourceType, paths.Moves,
		)
	}

	configText, err := renderDeploymentTfvars(
		options.Deployment, options.Result.Items, options.References,
		options.ResourceType, options.Tenant, options.VariableName, options.LookupOverrides,
	)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}

	configDirectory := path.Dir(paths.Config)
	keyMaps, err := lookupKeyMaps(configDirectory, options.References, options.LookupOverrides)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	binding, err := DeriveGeneratedBindings(options.BindingContext, options.Result.Items, keyMaps, options.ResourceType)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}

	return CompiledTransformArtifacts{
		Binding:                binding,
		ConfigText:             configText,
		ExistingMoves:          existingMoves,
		LookupText:             lookupText,
		RemoveLookupWhenAbsent: options.RemoveLookupWhenAbsent,
		Moves:                  moves,
		NewImports:             newImports,
		OnDiagnostic:           options.OnDiagnostic,
		Paths:                  paths,
		RenderedMoves:          renderedMoves,
		ResourceType:           options.ResourceType,
	}, nil
}

// CompileTransformArtifactBatch ports compileTransformArtifactBatch from
// node-src/domain/transform-artifacts.ts: "compile a complete batch before
// the caller publishes any member. Fresh lookup data from every member is
// authoritative for same-batch references."
func CompileTransformArtifactBatch(items []TransformArtifactCompileOptions) ([]CompiledTransformArtifacts, error) {
	pathOwners := map[string]string{}
	lookupsByConfigDirectory := map[string]map[string]*TransformLookupData{}
	allPaths := make([]TransformArtifactPaths, len(items))
	for i, item := range items {
		paths, err := ComputeTransformArtifactPaths(item.Deployment, item.ResourceType, item.Tenant)
		if err != nil {
			return nil, err
		}
		allPaths[i] = paths
		// Iterated in the same order as transformArtifactPaths's TS object
		// literal (config, staleConfig, generatedBindings, imports, lookup,
		// moves) so a multi-way collision is reported against the same
		// "first" path the Node source would report.
		ordered := []string{paths.Config, paths.StaleConfig, paths.GeneratedBindings, paths.Imports, paths.Lookup, paths.Moves}
		for _, outputPath := range ordered {
			if owner, ok := pathOwners[outputPath]; ok {
				return nil, fmt.Errorf(
					"transform artifact batch output collision: %s is owned by both %s and %s",
					jsonQuote(outputPath), jsonQuote(owner), jsonQuote(item.ResourceType),
				)
			}
			pathOwners[outputPath] = item.ResourceType
		}
		configDirectory := path.Dir(paths.Config)
		lookups, ok := lookupsByConfigDirectory[configDirectory]
		if !ok {
			lookups = map[string]*TransformLookupData{}
			lookupsByConfigDirectory[configDirectory] = lookups
		}
		data, _, err := compileLookup(item)
		if err != nil {
			return nil, err
		}
		lookups[item.ResourceType] = data
	}

	compiled := make([]CompiledTransformArtifacts, len(items))
	for i, item := range items {
		configDirectory := path.Dir(allPaths[i].Config)
		merged := map[string]*TransformLookupData{}
		for k, v := range item.LookupOverrides {
			merged[k] = v
		}
		for k, v := range lookupsByConfigDirectory[configDirectory] {
			merged[k] = v
		}
		item.LookupOverrides = merged
		result, err := CompileTransformArtifacts(item)
		if err != nil {
			return nil, err
		}
		compiled[i] = result
	}
	return compiled, nil
}

// PublishCompiledTransformArtifacts ports
// publishCompiledTransformArtifacts from
// node-src/domain/transform-artifacts.ts: "publish one fully compiled
// artifact set with the legacy file lifecycle." Unlike the batch publish
// path below, this writes each file directly (os.WriteFile, matching the
// TS source's plain, non-atomic node:fs/promises writeFile) rather than
// through a temp-file/rename transaction -- the TS source itself makes
// this same distinction (only publishCompiledTransformArtifactBatch stages
// through mkdtemp/rename), not something this port smooths over.
func PublishCompiledTransformArtifacts(compiled CompiledTransformArtifacts) (TransformArtifactWriteResult, error) {
	note := func(string) {}
	if compiled.OnDiagnostic != nil {
		note = compiled.OnDiagnostic
	}
	var written, removed []string

	configDirectory := path.Dir(compiled.Paths.Config)
	if err := os.MkdirAll(configDirectory, 0o777); err != nil {
		return TransformArtifactWriteResult{}, err
	}
	importsDirectory := path.Dir(compiled.Paths.Imports)
	if err := os.MkdirAll(importsDirectory, 0o777); err != nil {
		return TransformArtifactWriteResult{}, err
	}

	if compiled.LookupText != nil {
		if err := os.WriteFile(compiled.Paths.Lookup, []byte(*compiled.LookupText), 0o666); err != nil {
			return TransformArtifactWriteResult{}, err
		}
		written = append(written, compiled.Paths.Lookup)
		note("wrote " + compiled.Paths.Lookup)
	} else if compiled.RemoveLookupWhenAbsent {
		removedNow, err := removeIfPresent(compiled.Paths.Lookup)
		if err != nil {
			return TransformArtifactWriteResult{}, err
		}
		if removedNow {
			removed = append(removed, compiled.Paths.Lookup)
			note("removed stale inferred lookup " + compiled.Paths.Lookup)
		}
	}

	if compiled.ExistingMoves == nil && compiled.RenderedMoves != nil {
		if err := os.WriteFile(compiled.Paths.Moves, []byte(*compiled.RenderedMoves), 0o666); err != nil {
			return TransformArtifactWriteResult{}, err
		}
		written = append(written, compiled.Paths.Moves)
		note(fmt.Sprintf(
			"RENAME(S) DETECTED: %d item(s) re-keyed — moved blocks staged in %s; copy into the env root alongside the imports file before plan/apply (RUNBOOK: Drift)",
			len(compiled.Moves.Moves), compiled.Paths.Moves,
		))
	} else if compiled.ExistingMoves != nil {
		if compiled.RenderedMoves == nil {
			note("preserved unresolved move evidence " + compiled.Paths.Moves + " (no newly derived moves this run)")
		} else {
			note("preserved byte-identical unresolved move evidence " + compiled.Paths.Moves)
		}
	}
	for _, suppression := range compiled.Moves.Suppressed {
		note(fmt.Sprintf(
			"SUPPRESSED RENAME CANDIDATE: %s %s -> %s (import_id %s, reason=%s); no moved block emitted",
			compiled.ResourceType, jsonQuote(suppression.OldKey), jsonQuote(suppression.NewKey),
			jsonQuote(suppression.ImportID), suppression.Reason,
		))
	}

	removedStale, err := removeIfPresent(compiled.Paths.StaleConfig)
	if err != nil {
		return TransformArtifactWriteResult{}, err
	}
	if removedStale {
		removed = append(removed, compiled.Paths.StaleConfig)
		note("removed stale " + compiled.Paths.StaleConfig)
	}
	if err := os.WriteFile(compiled.Paths.Config, []byte(compiled.ConfigText), 0o666); err != nil {
		return TransformArtifactWriteResult{}, err
	}
	written = append(written, compiled.Paths.Config)

	for _, message := range compiled.Binding.Notes {
		note("NOTE bindings: " + message)
	}
	if len(compiled.Binding.Resources) > 0 {
		rendered, err := RenderGeneratedBindings(compiled.Binding.Resources)
		if err != nil {
			return TransformArtifactWriteResult{}, err
		}
		if err := os.WriteFile(compiled.Paths.GeneratedBindings, []byte(rendered), 0o666); err != nil {
			return TransformArtifactWriteResult{}, err
		}
		written = append(written, compiled.Paths.GeneratedBindings)
		note("wrote " + compiled.Paths.GeneratedBindings)
	} else {
		removedNow, err := removeIfPresent(compiled.Paths.GeneratedBindings)
		if err != nil {
			return TransformArtifactWriteResult{}, err
		}
		if removedNow {
			removed = append(removed, compiled.Paths.GeneratedBindings)
			note("removed stale " + compiled.Paths.GeneratedBindings)
		}
	}

	if err := os.WriteFile(compiled.Paths.Imports, []byte(compiled.NewImports), 0o666); err != nil {
		return TransformArtifactWriteResult{}, err
	}
	written = append(written, compiled.Paths.Imports)
	note("wrote " + compiled.Paths.Config)
	note("wrote " + compiled.Paths.Imports)

	if written == nil {
		written = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	return TransformArtifactWriteResult{Paths: compiled.Paths, Written: written, Removed: removed}, nil
}

// assertRegularBatchArtifactTarget ports assertRegularBatchArtifactTarget
// from node-src/domain/transform-artifacts.ts.
func assertRegularBatchArtifactTarget(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("transform artifact batch target is not a regular file: %s", target)
	}
	return nil
}

// batchArtifactMutations ports batchArtifactMutations from
// node-src/domain/transform-artifacts.ts. Unlike the TS source (which never
// fails: renderGeneratedBindings there cannot throw for a
// deriveGeneratedBindings-produced value), this returns an error rather
// than assuming that invariant silently -- RenderGeneratedBindings's
// contract already returns one, and propagating it here is strictly safer
// than a Go idiom for "this cannot happen" (a bare panic) would be.
func batchArtifactMutations(compiled CompiledTransformArtifacts) ([]batchArtifactMutation, error) {
	var mutations []batchArtifactMutation
	if compiled.LookupText != nil {
		mutations = append(mutations, batchArtifactMutation{
			contents: compiled.LookupText, kind: mutationWrite,
			resourceType: compiled.ResourceType, target: compiled.Paths.Lookup,
		})
	} else if compiled.RemoveLookupWhenAbsent {
		mutations = append(mutations, batchArtifactMutation{
			kind: mutationRemove, resourceType: compiled.ResourceType, target: compiled.Paths.Lookup,
		})
	}
	if compiled.ExistingMoves == nil && compiled.RenderedMoves != nil {
		mutations = append(mutations, batchArtifactMutation{
			contents: compiled.RenderedMoves, kind: mutationWrite,
			resourceType: compiled.ResourceType, target: compiled.Paths.Moves,
		})
	}
	mutations = append(mutations, batchArtifactMutation{
		kind: mutationRemove, resourceType: compiled.ResourceType, target: compiled.Paths.StaleConfig,
	})
	configText := compiled.ConfigText
	mutations = append(mutations, batchArtifactMutation{
		contents: &configText, kind: mutationWrite,
		resourceType: compiled.ResourceType, target: compiled.Paths.Config,
	})
	if len(compiled.Binding.Resources) > 0 {
		rendered, err := RenderGeneratedBindings(compiled.Binding.Resources)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, batchArtifactMutation{
			contents: &rendered, kind: mutationWrite,
			resourceType: compiled.ResourceType, target: compiled.Paths.GeneratedBindings,
		})
	} else {
		mutations = append(mutations, batchArtifactMutation{
			kind: mutationRemove, resourceType: compiled.ResourceType, target: compiled.Paths.GeneratedBindings,
		})
	}
	newImports := compiled.NewImports
	mutations = append(mutations, batchArtifactMutation{
		contents: &newImports, kind: mutationWrite,
		resourceType: compiled.ResourceType, target: compiled.Paths.Imports,
	})
	return mutations, nil
}

// removeTransactionDirectories ports removeTransactionDirectories from
// node-src/domain/transform-artifacts.ts. os.RemoveAll, like Node's
// `rm(directory, {force: true, recursive: true})`, does not error when
// directory is already absent.
func removeTransactionDirectories(directories []string) []error {
	var failures []error
	for _, directory := range directories {
		if err := os.RemoveAll(directory); err != nil {
			failures = append(failures, err)
		}
	}
	return failures
}

// prepareBatchArtifactMutations ports prepareBatchArtifactMutations from
// node-src/domain/transform-artifacts.ts.
func prepareBatchArtifactMutations(mutations []batchArtifactMutation) ([]preparedBatchArtifactMutation, []string, error) {
	return prepareBatchArtifactMutationsWithFilesystem(
		mutations,
		os.WriteFile,
		removeTransactionDirectories,
	)
}

// prepareBatchArtifactMutationsWithStageWriter keeps the stage write as an
// injected leaf so tests can exercise its otherwise unreachable failure path
// without mutable package state or timing races.
func prepareBatchArtifactMutationsWithStageWriter(
	mutations []batchArtifactMutation,
	writeStageFile func(string, []byte, os.FileMode) error,
) ([]preparedBatchArtifactMutation, []string, error) {
	return prepareBatchArtifactMutationsWithFilesystem(
		mutations,
		writeStageFile,
		removeTransactionDirectories,
	)
}

// prepareBatchArtifactMutationsWithFilesystem injects the two filesystem
// leaves needed to deterministically cover staging-plus-cleanup aggregation.
// Production passes the real functions per call; no mutable package seam is
// installed.
func prepareBatchArtifactMutationsWithFilesystem(
	mutations []batchArtifactMutation,
	writeStageFile func(string, []byte, os.FileMode) error,
	removeTransactions func([]string) []error,
) ([]preparedBatchArtifactMutation, []string, error) {
	transactionDirectoryByParent := map[string]string{}
	var transactionDirectories []string
	prepared := make([]preparedBatchArtifactMutation, 0, len(mutations))

	fail := func(original error) ([]preparedBatchArtifactMutation, []string, error) {
		cleanupFailures := removeTransactions(transactionDirectories)
		if len(cleanupFailures) == 0 {
			return nil, nil, original
		}
		return nil, nil, &multiError{
			message: "transform artifact batch staging and cleanup both failed",
			errs:    append([]error{original}, cleanupFailures...),
		}
	}

	for index, mutation := range mutations {
		parent := path.Dir(mutation.target)
		if err := os.MkdirAll(parent, 0o777); err != nil {
			return fail(err)
		}
		transactionDirectory, ok := transactionDirectoryByParent[parent]
		if !ok {
			dir, err := os.MkdirTemp(parent, ".infrawright-artifact-batch-")
			if err != nil {
				return fail(err)
			}
			transactionDirectory = dir
			transactionDirectoryByParent[parent] = dir
			transactionDirectories = append(transactionDirectories, dir)
		}
		var stagePath *string
		if mutation.kind == mutationWrite {
			if mutation.contents == nil {
				return fail(fmt.Errorf("missing staged contents for %s", mutation.target))
			}
			s := path.Join(transactionDirectory, fmt.Sprintf("stage-%d", index))
			if err := writeStageFile(s, []byte(*mutation.contents), 0o666); err != nil {
				return fail(err)
			}
			stagePath = &s
		}
		prepared = append(prepared, preparedBatchArtifactMutation{
			batchArtifactMutation: mutation,
			backupPath:            path.Join(transactionDirectory, fmt.Sprintf("backup-%d", index)),
			stagePath:             stagePath,
		})
	}
	return prepared, transactionDirectories, nil
}

// stagedFileMode reproduces the Node source's `metadata.mode & 0o7777` (the
// previous file's Unix mode bits: permission bits plus setuid/setgid/sticky)
// as an os.FileMode suitable for os.Chmod, using Go's portable
// os.FileMode bit accessors (ModeSetuid/ModeSetgid/ModeSticky) rather than a
// platform-specific syscall.Stat_t, so this file builds unmodified on every
// GOOS this module targets.
func stagedFileMode(previous os.FileInfo) os.FileMode {
	mode := previous.Mode().Perm()
	if previous.Mode()&os.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if previous.Mode()&os.ModeSetgid != 0 {
		mode |= 0o2000
	}
	if previous.Mode()&os.ModeSticky != 0 {
		mode |= 0o1000
	}
	return mode
}

// applyBatchArtifactMutations ports applyBatchArtifactMutations from
// node-src/domain/transform-artifacts.ts: the temp/rename commit loop, and
// its reverse-order rollback on any failure.
func applyBatchArtifactMutations(mutations []preparedBatchArtifactMutation) ([]appliedBatchArtifactMutation, error) {
	return applyBatchArtifactMutationsWithLstat(mutations, os.Lstat)
}

// applyBatchArtifactMutationsWithLstat injects only the post-backup metadata
// read so tests can fault the otherwise race-only rename-to-lstat boundary
// without shared mutable state.
func applyBatchArtifactMutationsWithLstat(
	mutations []preparedBatchArtifactMutation,
	lstatBackup func(string) (os.FileInfo, error),
) ([]appliedBatchArtifactMutation, error) {
	var applied []appliedBatchArtifactMutation

	fail := func(applyErr error) ([]appliedBatchArtifactMutation, error) {
		var rollbackFailures []error
		for i := len(applied) - 1; i >= 0; i-- {
			mutation := applied[i]
			if err := runBatchArtifactCommitHook(mutation.batchArtifactMutation, "rollback"); err != nil {
				rollbackFailures = append(rollbackFailures, err)
				continue
			}
			var rollbackErr error
			if mutation.kind == mutationWrite {
				if _, err := removeIfPresent(mutation.target); err != nil {
					rollbackErr = err
				}
			}
			if rollbackErr == nil && mutation.hadOriginal {
				if err := os.Rename(mutation.backupPath, mutation.target); err != nil {
					rollbackErr = err
				}
			}
			if rollbackErr != nil {
				rollbackFailures = append(rollbackFailures, rollbackErr)
			}
		}
		if len(rollbackFailures) == 0 {
			return nil, applyErr
		}
		seenDirs := map[string]bool{}
		var transactionDirectories []string
		for _, mutation := range applied {
			dir := path.Dir(mutation.backupPath)
			if !seenDirs[dir] {
				seenDirs[dir] = true
				transactionDirectories = append(transactionDirectories, dir)
			}
		}
		return nil, &BatchArtifactRollbackError{
			Errors:                 append([]error{applyErr}, rollbackFailures...),
			TransactionDirectories: transactionDirectories,
		}
	}

	for _, mutation := range mutations {
		if err := runBatchArtifactCommitHook(mutation.batchArtifactMutation, "commit"); err != nil {
			return fail(err)
		}
		hadOriginal := false
		if err := os.Rename(mutation.target, mutation.backupPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fail(err)
			}
		} else {
			hadOriginal = true
		}
		applied = append(applied, appliedBatchArtifactMutation{
			preparedBatchArtifactMutation: mutation,
			hadOriginal:                   hadOriginal,
		})

		var previous os.FileInfo
		if hadOriginal {
			info, err := lstatBackup(mutation.backupPath)
			if err != nil {
				return fail(err)
			}
			previous = info
		}
		if previous != nil && (!previous.Mode().IsRegular() || previous.Mode()&os.ModeSymlink != 0) {
			return fail(fmt.Errorf("transform artifact batch target changed to a non-regular file: %s", mutation.target))
		}
		if mutation.kind == mutationWrite {
			if mutation.stagePath == nil {
				return fail(fmt.Errorf("missing staged artifact for %s", mutation.target))
			}
			if previous != nil {
				if err := os.Chmod(*mutation.stagePath, stagedFileMode(previous)); err != nil {
					return fail(err)
				}
			}
			if err := os.Rename(*mutation.stagePath, mutation.target); err != nil {
				return fail(err)
			}
		}
	}
	return applied, nil
}

// completedBatchArtifactResult ports completedBatchArtifactResult from
// node-src/domain/transform-artifacts.ts.
func completedBatchArtifactResult(compiled CompiledTransformArtifacts, applied []appliedBatchArtifactMutation) TransformArtifactWriteResult {
	var written, removed []string
	removedSet := map[string]bool{}
	for _, mutation := range applied {
		if mutation.resourceType != compiled.ResourceType {
			continue
		}
		if mutation.kind == mutationWrite {
			written = append(written, mutation.target)
		} else if mutation.kind == mutationRemove && mutation.hadOriginal {
			removed = append(removed, mutation.target)
			removedSet[mutation.target] = true
		}
	}
	note := func(string) {}
	if compiled.OnDiagnostic != nil {
		note = compiled.OnDiagnostic
	}

	if compiled.LookupText != nil {
		note("wrote " + compiled.Paths.Lookup)
	} else if removedSet[compiled.Paths.Lookup] {
		note("removed stale inferred lookup " + compiled.Paths.Lookup)
	}
	if compiled.ExistingMoves == nil && compiled.RenderedMoves != nil {
		note(fmt.Sprintf(
			"RENAME(S) DETECTED: %d item(s) re-keyed — moved blocks staged in %s; copy into the env root alongside the imports file before plan/apply (RUNBOOK: Drift)",
			len(compiled.Moves.Moves), compiled.Paths.Moves,
		))
	} else if compiled.ExistingMoves != nil {
		if compiled.RenderedMoves == nil {
			note("preserved unresolved move evidence " + compiled.Paths.Moves + " (no newly derived moves this run)")
		} else {
			note("preserved byte-identical unresolved move evidence " + compiled.Paths.Moves)
		}
	}
	for _, suppression := range compiled.Moves.Suppressed {
		note(fmt.Sprintf(
			"SUPPRESSED RENAME CANDIDATE: %s %s -> %s (import_id %s, reason=%s); no moved block emitted",
			compiled.ResourceType, jsonQuote(suppression.OldKey), jsonQuote(suppression.NewKey),
			jsonQuote(suppression.ImportID), suppression.Reason,
		))
	}
	if removedSet[compiled.Paths.StaleConfig] {
		note("removed stale " + compiled.Paths.StaleConfig)
	}
	for _, message := range compiled.Binding.Notes {
		note("NOTE bindings: " + message)
	}
	if len(compiled.Binding.Resources) > 0 {
		note("wrote " + compiled.Paths.GeneratedBindings)
	} else if removedSet[compiled.Paths.GeneratedBindings] {
		note("removed stale " + compiled.Paths.GeneratedBindings)
	}
	note("wrote " + compiled.Paths.Config)
	note("wrote " + compiled.Paths.Imports)

	if written == nil {
		written = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	return TransformArtifactWriteResult{Paths: compiled.Paths, Written: written, Removed: removed}
}

// PublishCompiledTransformArtifactBatch ports
// publishCompiledTransformArtifactBatch from
// node-src/domain/transform-artifacts.ts: "publish an already-preflighted
// batch as one rollback-capable filesystem transaction in deterministic
// caller order."
func PublishCompiledTransformArtifactBatch(compiled []CompiledTransformArtifacts) ([]TransformArtifactWriteResult, error) {
	var mutations []batchArtifactMutation
	for _, item := range compiled {
		itemMutations, err := batchArtifactMutations(item)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, itemMutations...)
	}
	targetOwners := map[string]string{}
	for _, mutation := range mutations {
		if owner, ok := targetOwners[mutation.target]; ok {
			return nil, fmt.Errorf(
				"transform artifact batch mutation collision: %s is owned by both %s and %s",
				jsonQuote(mutation.target), jsonQuote(owner), jsonQuote(mutation.resourceType),
			)
		}
		targetOwners[mutation.target] = mutation.resourceType
	}
	for _, mutation := range mutations {
		if err := assertRegularBatchArtifactTarget(mutation.target); err != nil {
			return nil, err
		}
	}
	prepared, transactionDirectories, err := prepareBatchArtifactMutations(mutations)
	if err != nil {
		return nil, err
	}

	applied, err := applyBatchArtifactMutations(prepared)
	if err != nil {
		var rollbackErr *BatchArtifactRollbackError
		if errors.As(err, &rollbackErr) {
			return nil, err
		}
		cleanupFailures := removeTransactionDirectories(transactionDirectories)
		if len(cleanupFailures) == 0 {
			return nil, err
		}
		return nil, &multiError{
			message: "transform artifact batch publication failed and transaction cleanup also failed",
			errs:    append([]error{err}, cleanupFailures...),
		}
	}
	cleanupFailures := removeTransactionDirectories(transactionDirectories)
	if len(cleanupFailures) > 0 {
		return nil, &multiError{
			message: "transform artifact batch committed but transaction cleanup failed",
			errs:    cleanupFailures,
		}
	}
	results := make([]TransformArtifactWriteResult, len(compiled))
	for i, item := range compiled {
		results[i] = completedBatchArtifactResult(item, applied)
	}
	return results, nil
}

// WriteTransformArtifacts ports writeTransformArtifacts from
// node-src/domain/transform-artifacts.ts: "materialize one ordinary
// transform artifact set with the legacy file lifecycle."
func WriteTransformArtifacts(options TransformArtifactCompileOptions) (TransformArtifactWriteResult, error) {
	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		return TransformArtifactWriteResult{}, err
	}
	return PublishCompiledTransformArtifacts(compiled)
}

// WriteDerivedTransformArtifact ports writeDerivedTransformArtifact from
// node-src/domain/transform-artifacts.ts: "derived resources write config
// only and intentionally create no imports."
func WriteDerivedTransformArtifact(
	dep deployment.Deployment,
	items map[string]map[string]any,
	references map[string]TransformReferenceSpec,
	resourceType, sourceType, tenant, variableName string,
	onDiagnostic func(string),
) (string, error) {
	paths, err := ComputeTransformArtifactPaths(dep, resourceType, tenant)
	if err != nil {
		return "", err
	}
	configDirectory := path.Dir(paths.Config)
	if err := os.MkdirAll(configDirectory, 0o777); err != nil {
		return "", err
	}
	removedStale, err := removeIfPresent(paths.StaleConfig)
	if err != nil {
		return "", err
	}
	if removedStale && onDiagnostic != nil {
		onDiagnostic("removed stale " + paths.StaleConfig)
	}
	configText, err := renderDeploymentTfvars(dep, items, references, resourceType, tenant, variableName, nil)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(paths.Config, []byte(configText), 0o666); err != nil {
		return "", err
	}
	if onDiagnostic != nil {
		onDiagnostic(fmt.Sprintf("wrote %s (derived from %s; not importable — no imports)", paths.Config, sourceType))
	}
	return paths.Config, nil
}

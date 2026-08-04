package tfrender

// transform_artifacts.go ports the original implementation:
// artifact assembly (tfvars in json/hcl format, imports files, lookup
// sidecars) and the sequential single-artifact filesystem write path. The
// Node source's transactional batch preflight/publish/rollback machinery is
// not ported: it had no production callers (transform and adopt compile and
// publish each type sequentially). Vectors: the pure-library subset of
// the original test corpus, ported in
// transform_artifacts_test.go -- see that file's doc comment for exactly
// which of that source's tests are ported here versus skipped as
// runner/CLI-level (they exercise the original implementation,
// the original implementation, the original implementation, or
// the original implementation's pack loading, none of which are part of
// this package's scope or dependency set).
//
// # expression-bindings.ts is NOT consumed here
//
// This task's brief anticipated transform-artifacts.ts might consume
// the original implementation for its "binding context"
// handling. It does not: grepping the original implementation's
// imports (reproduced below) shows no reference to expression-bindings.js,
// and grepping the original source tree for "expression-bindings" shows its only importer
// is the original implementation, an unrelated consumer
// outside this port's scope. BindingContext/TransformReferenceSpec and the
// cross-state reference-binding derivation logic
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
// the original implementation's own
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
// the same name in the original implementation, whose full port
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
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// PullTransformResult is a LOCAL minimal port of the PullTransformResult
// interface in the original implementation. See this file's
// package-level doc comment.
type PullTransformResult struct {
	Items     map[string]map[string]any
	Originals map[string]map[string]any
	Drops     []string
}

// TransformReferenceSpec is the Go analogue of the TransformReferenceSpec
// interface in the original implementation. NameField (the TS
// interface's `name_field`) is never read by any function this file ports
// -- grepping transform-artifacts.ts confirms `.name_field`/`name_field`
// only ever appears in this interface's own declaration -- but is kept for
// structural fidelity with callers (outside this slice) that build
// References maps.
type TransformReferenceSpec struct {
	// IDField carries the edge's declared referent_id_field (see
	// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md):
	// empty selects the canonical id space (byte-identical to every edge
	// that predates this field); non-empty selects the named alternate
	// space the referent's sidecar publishes under "spaces". Tokens are
	// self-describing as of the Phase 7 revision: a canonical edge still
	// mints and reads the bare "<referent>.<key>" token, but a declared
	// alternate-space edge mints the explicit "<referent>.<key>.<field>"
	// form so committed config names its own space. A bare token or an
	// explicit ".id" token always means the canonical space, on read,
	// regardless of what any edge declares.
	IDField   string
	NameField string
	Referent  string
}

// BindingContext is the Go analogue of the BindingContext interface in
// the original implementation. Derived and Generated are the Go
// analogues of its two `ReadonlySet<string>` fields, represented as
// presence-only string sets (map[string]bool) the same way this port
// represents every other TS Set/Readonly<Set> it encounters.
type BindingContext struct {
	Mode    deployment.ReferenceBindingMode
	Derived map[string]bool
	// DataReferents marks data-only referent types. They are valid reference
	// targets (bindableReference admits them as referents) but never
	// referrers, and they are disjoint from Generated by registry validation.
	DataReferents map[string]bool
	Generated     map[string]bool
	ResourceRoots map[string]string
	References    map[string]TransformReferenceSpec
	// SetBlockFields maps a dotted reference field to the index of the first
	// segment that names a set-nested block in the referrer's provider schema.
	// A set block's members have no stable order, so an indexed path into one
	// (services[0].id) names nothing; the binding for such a field must land
	// on the complete block leaf (services) with an expression that renders
	// the entire block value. gen-env's schema-path validator refuses the
	// indexed form, so a producer that emits it writes bindings that can
	// never generate an environment.
	SetBlockFields map[string]int
	// TokensOnly restricts resolve to committed reference tokens: a raw
	// tenant ID is skipped WITHOUT consulting the referent's lookup.
	//
	// Raw-ID derivation is transform-only by contract. A raw ID names nothing
	// on its own -- only the lookup translates it -- so a derivation that
	// resolved one would make the artifact it produces a function of lookup
	// contents. That is exactly right at transform time (the lookup is being
	// written by the same run) and exactly wrong at render time: a later
	// referent-only transform that added the ID to the lookup would silently
	// rebind a committed leaf no re-transform ever touched, and if state and
	// lookup disagree, the resolver can select a different ID than the literal
	// the operator committed.
	//
	// gen-env's render-derivation sets this; transform and adopt do not, so
	// their behaviour -- including substitution's minted-coverage assert --
	// is unchanged. The two paths still agree over a tokenised leaf, which is
	// the only leaf render-derivation is responsible for.
	TokensOnly bool
}

// GeneratedBindingsResult is the Go analogue of the GeneratedBindingsResult
// interface in the original implementation. Resources is the Go
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
// interface in the original implementation.
type TransformArtifactPaths struct {
	Config            string
	GeneratedBindings string
	Imports           string
	// LegacyConfig and LegacyStaleConfig are the flat config paths that the
	// data-referent lane supersedes. The generated lane leaves them in place
	// for path compatibility; publication stale-cleans them only for data
	// referents.
	LegacyConfig      string
	LegacyStaleConfig string
	// LegacyLookup is the lookup's pre-migration location
	// (<configDir>/<type>.lookup.json, the same directory the config and
	// generated-bindings files sit in). Never written. Unlike StaleConfig,
	// publish does not stale-clean it unconditionally on every run: it is
	// only ever removed inside the same two branches that touch Lookup
	// itself -- a fresh write (LookupText != nil) or an inferred-absent
	// removal (RemoveLookupWhenAbsent, when this run's active pack root
	// declares no surviving inbound reference into this type). When neither
	// branch fires -- this run's scope simply has no opinion on this type's
	// lookup, e.g. a selective/partial run whose active root omits the
	// referrer that declares the edge -- the legacy copy is left alone
	// rather than risk deleting a lookup's only surviving copy on a run that
	// never decided it should go. Every lookup reader falls back to it when
	// Lookup does not exist on disk, so a tenant that has not re-transformed
	// since this migration keeps rendering unchanged.
	LegacyLookup string
	// Lookup is the lookup's current, authoritative location:
	// <configDir>/lookups/<type>.lookup.json. Publish creates the lookups/
	// subdirectory as needed.
	Lookup      string
	Moves       string
	StaleConfig string
}

// TransformArtifactWriteResult is the Go analogue of the
// TransformArtifactWriteResult interface in
// the original implementation.
type TransformArtifactWriteResult struct {
	Paths   TransformArtifactPaths
	Written []string
	Removed []string
}

// TransformLookupData is the Go analogue of the TransformLookupData
// interface in the original implementation, extended with IDByKey: the
// inverse lookup the plan-time reference-token fallback expression indexes.
// The parser derives it from key_by_id when a committed sidecar predates
// the field, so both directions decode for every lookup ever written.
//
// Spaces carries the optional alternate identifier spaces a referent
// publishes alongside the canonical id space (see
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md).
// Unlike IDByKey, a missing space is never derived from the canonical
// maps: the alternate attribute's values do not exist anywhere in
// ByID/IDByKey/KeyByID, so an absent space decodes to a nil Spaces entry,
// never a fallback. Spaces is nil when the sidecar carries no "spaces" key
// at all (every sidecar written before this field existed, and every
// sidecar for a referent with no declared alternate space).
type TransformLookupData struct {
	ByID    map[string]string
	IDByKey map[string]string
	KeyByID map[string]string
	Spaces  map[string]TransformLookupSpace
}

// TransformLookupSpace is one alternate identifier space a referent
// publishes, mirroring the shape of the canonical id space's two resolver
// maps (see TransformLookupData.Spaces). IDByKey decodes an item key to
// the space's identity; KeyByID is its inverse.
type TransformLookupSpace struct {
	IDByKey map[string]string
	KeyByID map[string]string
}

// TransformLookupShape selects the on-disk shape emitted for a transform
// lookup sidecar. The legacy shape preserves the generated lane's historical
// empty/flat output; the nested shape is required by data-referent consumers.
type TransformLookupShape uint8

const (
	// TransformLookupShapeLegacy preserves the generated lane's existing bytes.
	TransformLookupShapeLegacy TransformLookupShape = iota
	// TransformLookupShapeNested always emits by_id, id_by_key, and key_by_id.
	TransformLookupShapeNested
)

// TransformArtifactMode selects the artifact lane compiled and published for
// one resource. The zero value is the generated lane for compatibility with
// existing callers that predate the data-referent lane.
type TransformArtifactMode uint8

const (
	// TransformArtifactModeGenerated publishes the ordinary resource artifacts.
	TransformArtifactModeGenerated TransformArtifactMode = iota
	// TransformArtifactModeDataReferent publishes config and lookup only.
	TransformArtifactModeDataReferent
)

// TransformArtifactCompileOptions is the Go analogue of the
// TransformArtifactCompileOptions interface in
// the original implementation.
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
//
// With the batch compiler (its only production writer) removed,
// LookupOverrides is a deliberate test seam: tests populate it to compile
// against injected lookup data without writing sidecars to disk. Production
// callers leave it nil and resolve lookups from the config directory.
type TransformArtifactCompileOptions struct {
	ArtifactMode    TransformArtifactMode
	BindingContext  BindingContext
	Deployment      deployment.Deployment
	LookupNameField *string
	// PublishSpaces is the sorted, deduplicated set of alternate identifier
	// spaces (referent_id_field values) this resource's compile must publish
	// in its own lookup sidecar, because other active edges cite it through
	// those spaces (see TransformLookupData.Spaces). Computed by the caller
	// from transform.ReferentAlternateSpaces(root, resourceType) -- inbound
	// edges, unlike References below, which are this resource's own outbound
	// edges. Always nil for the data-referent lane (compileLookup enforces
	// this regardless of what a caller passes).
	PublishSpaces          []string
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
// the original implementation: "fully preflighted transform
// output; pass this to the publish functions."
type CompiledTransformArtifacts struct {
	ArtifactMode           TransformArtifactMode
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
// the original implementation: "match the scalar spelling used
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
// the original implementation: "match Python str.format's field
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
// the original implementation. template nil matches the TS
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
// the original implementation.
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
// the original implementation: "render Python's transform
// lookup sidecar, including last-key-wins IDs." It preserves the generated
// lane's legacy empty/flat shape.
func RenderTransformLookup(items, originals map[string]map[string]any, nameField string) (string, error) {
	return RenderTransformLookupWithShape(items, originals, nameField, TransformLookupShapeLegacy, nil)
}

// RenderTransformLookupWithShape renders a lookup sidecar with an explicit
// shape. Data-referent publication uses TransformLookupShapeNested so an
// empty successful fetch still has all three resolver maps.
//
// spaces names the alternate identifier space fields to publish alongside
// the canonical id space (see TransformLookupData.Spaces); the caller
// sorts and dedupes it. A nil or empty spaces produces byte-identical
// output to a caller that never knew about this parameter: no "spaces" key
// is emitted, and the canonical by_id/id_by_key/key_by_id maps are built
// exactly as before. Each space field's KeyByID/IDByKey are built from the
// same per-item merged record (originals merged under the projection) the
// canonical id path already computes -- lookupIdentity(merged[field]) --
// so a space's identity uses exactly the same scalar-rendering and
// empty/nil skip rule as the canonical id. Unlike the canonical id, an
// empty or missing space value is never an error under either shape: it
// is a silently omitted row (the nested-shape empty-ident error is a
// canonical-id-only rule). A duplicate identity within one space across
// items fails loudly, unconditional on shape, mirroring the canonical
// nested-shape seenDataIDs duplicate check.
func RenderTransformLookupWithShape(
	items, originals map[string]map[string]any,
	nameField string,
	shape TransformLookupShape,
	spaces []string,
) (string, error) {
	byID := map[string]any{}
	idByKey := map[string]any{}
	keyByID := map[string]any{}
	seenDataIDs := map[string]string{}

	spaceIDByKey := make(map[string]map[string]any, len(spaces))
	spaceKeyByID := make(map[string]map[string]any, len(spaces))
	spaceSeenIdentities := make(map[string]map[string]string, len(spaces))
	for _, field := range spaces {
		spaceIDByKey[field] = map[string]any{}
		spaceKeyByID[field] = map[string]any{}
		spaceSeenIdentities[field] = map[string]string{}
	}

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
		skipCanonical := false
		if ident == nil {
			if shape == TransformLookupShapeNested {
				return "", fmt.Errorf(
					"data referent lookup key %s has an empty canonical ID",
					jsonQuote(key),
				)
			}
			skipCanonical = true
		} else if shape == TransformLookupShapeNested && strings.TrimSpace(*ident) == "" {
			return "", fmt.Errorf(
				"data referent lookup key %s has an empty canonical ID",
				jsonQuote(key),
			)
		}
		if !skipCanonical {
			// Nested shape is the data-referent lane. Keep the legacy flat
			// renderer's last-key-wins behavior unchanged.
			if shape == TransformLookupShapeNested {
				if previousKey, exists := seenDataIDs[*ident]; exists {
					return "", fmt.Errorf(
						"duplicate data referent canonical ID %s for keys %s and %s; IDs must be unique",
						jsonQuote(*ident), jsonQuote(previousKey), jsonQuote(key),
					)
				}
				seenDataIDs[*ident] = key
			}
			display, isString := merged[nameField].(string)
			text := "<unknown>"
			if isString && strings.TrimSpace(display) != "" {
				text = display
			}
			byID[*ident] = text
			idByKey[key] = *ident
			keyByID[*ident] = key
		}
		for _, field := range spaces {
			spaceIdent, err := lookupIdentity(merged[field])
			if err != nil {
				return "", err
			}
			if spaceIdent == nil {
				continue
			}
			if previousKey, exists := spaceSeenIdentities[field][*spaceIdent]; exists {
				return "", fmt.Errorf(
					"duplicate %s identity %s for keys %s and %s; %s identities must be unique",
					jsonQuote(field), jsonQuote(*spaceIdent), jsonQuote(previousKey), jsonQuote(key), jsonQuote(field),
				)
			}
			spaceSeenIdentities[field][*spaceIdent] = key
			spaceIDByKey[field][key] = *spaceIdent
			spaceKeyByID[field][*spaceIdent] = key
		}
	}
	var payload map[string]any
	if shape != TransformLookupShapeNested && len(keyByID) == 0 && len(spaces) == 0 {
		payload = byID
	} else {
		payload = map[string]any{"by_id": byID, "id_by_key": idByKey, "key_by_id": keyByID}
		if len(spaces) > 0 {
			spacesPayload := map[string]any{}
			for _, field := range spaces {
				spacesPayload[field] = map[string]any{
					"id_by_key": spaceIDByKey[field],
					"key_by_id": spaceKeyByID[field],
				}
			}
			payload["spaces"] = spacesPayload
		}
	}
	return canonjson.RenderLosslessArtifactJSON(payload)
}

func asObject(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

// ParseLookupSidecar ports parseLookupSidecar from
// the original implementation.
func ParseLookupSidecar(value any) (TransformLookupData, error) {
	root, ok := asObject(value)
	if !ok {
		return TransformLookupData{}, errors.New("lookup sidecar must contain a JSON object")
	}
	nestedByID, hasNestedByID := asObject(root["by_id"])
	nestedIDs, hasNestedIDs := asObject(root["id_by_key"])
	nestedKeys, hasNestedKeys := asObject(root["key_by_id"])
	rawByID := root
	if hasNestedByID {
		rawByID = nestedByID
	}
	byID := map[string]string{}
	idByKey := map[string]string{}
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
	if hasNestedIDs {
		for key, ident := range nestedIDs {
			if s, ok := ident.(string); ok && s != "" {
				idByKey[key] = s
			}
		}
	} else {
		// A sidecar written before id_by_key existed still decodes both
		// directions: key_by_id is a bijection over its rows, so the
		// inverse is derivable rather than absent.
		for ident, itemKey := range keyByID {
			idByKey[itemKey] = ident
		}
	}

	var spaces map[string]TransformLookupSpace
	if rawSpaces, hasSpaces := root["spaces"]; hasSpaces {
		spacesObj, ok := asObject(rawSpaces)
		if !ok {
			return TransformLookupData{}, errors.New("lookup sidecar spaces must be a JSON object")
		}
		spaces = make(map[string]TransformLookupSpace, len(spacesObj))
		for field, rawEntry := range spacesObj {
			entryObj, ok := asObject(rawEntry)
			if !ok {
				return TransformLookupData{}, fmt.Errorf(
					"lookup sidecar spaces.%s must be a JSON object", jsonQuote(field),
				)
			}
			for entryKey := range entryObj {
				if entryKey != "id_by_key" && entryKey != "key_by_id" {
					return TransformLookupData{}, fmt.Errorf(
						"lookup sidecar spaces.%s has unknown key %s",
						jsonQuote(field), jsonQuote(entryKey),
					)
				}
			}
			spaceKeyByID := map[string]string{}
			if rawKeyByID, present := entryObj["key_by_id"]; present {
				keyByIDObj, ok := asObject(rawKeyByID)
				if !ok {
					return TransformLookupData{}, fmt.Errorf(
						"lookup sidecar spaces.%s.key_by_id must be a JSON object", jsonQuote(field),
					)
				}
				for k, v := range keyByIDObj {
					if s, ok := v.(string); ok && s != "" {
						spaceKeyByID[k] = s
					}
				}
			}
			spaceIDByKey := map[string]string{}
			if rawIDByKey, present := entryObj["id_by_key"]; present {
				idByKeyObj, ok := asObject(rawIDByKey)
				if !ok {
					return TransformLookupData{}, fmt.Errorf(
						"lookup sidecar spaces.%s.id_by_key must be a JSON object", jsonQuote(field),
					)
				}
				for k, v := range idByKeyObj {
					if s, ok := v.(string); ok && s != "" {
						spaceIDByKey[k] = s
					}
				}
			} else {
				// Within one space entry, id_by_key IS derivable from key_by_id
				// -- it is the same space, mirroring the canonical id_by_key
				// inversion precedent above. This never derives a space FROM
				// the canonical by_id/id_by_key/key_by_id maps: a space absent
				// from the sidecar entirely stays absent from Spaces (see
				// TransformLookupData.Spaces); no fallback ever crosses that
				// boundary.
				for ident, itemKey := range spaceKeyByID {
					spaceIDByKey[itemKey] = ident
				}
			}
			spaces[field] = TransformLookupSpace{IDByKey: spaceIDByKey, KeyByID: spaceKeyByID}
		}
	}

	return TransformLookupData{ByID: byID, IDByKey: idByKey, KeyByID: keyByID, Spaces: spaces}, nil
}

var integerTokenPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

// integerToken ports integerToken from
// the original implementation, returning nil where the TS source
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
// the original implementation.
func zeroSentinel(value any) bool {
	token := integerToken(value)
	return token != nil && *token == "0"
}

// bindableListElement ports bindableListElement from
// the original implementation.
func bindableListElement(value any) bool {
	if s, ok := value.(string); ok && s != "" {
		return true
	}
	return integerToken(value) != nil
}

// bindableReference ports bindableReference from
// the original implementation. The referrer must be generated, but the
// referent may be either generated or a data referent: a data root publishes
// the same iw_reference_ids output contract from its own state, so the
// canonical remote-state selector resolves identically, and envgen's resolver
// rewrite already routes data referents to the committed-lookup arm whenever
// their state is absent or unusable.
func bindableReference(resourceType, referent string, context BindingContext) bool {
	if resourceType == referent {
		return false
	}
	if !context.Generated[resourceType] {
		return false
	}
	if !context.Generated[referent] && !context.DataReferents[referent] {
		return false
	}
	if context.Derived[resourceType] || context.Derived[referent] {
		return false
	}
	if _, ok := context.ResourceRoots[resourceType]; !ok {
		return false
	}
	if _, ok := context.ResourceRoots[referent]; !ok {
		return false
	}
	return context.Mode == deployment.ReferenceBindingCrossState
}

// fieldCandidate is the Go analogue of fieldCandidates's anonymous element
// type in the original implementation.
type fieldCandidate struct {
	key   string
	path  string
	value any
}

var identifierSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fieldCandidates ports fieldCandidates from
// the original implementation.
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

// substituteReferenceTokens rewrites declared reference-field values in
// items from raw tenant IDs to qualified tokens ("<referent>.<key>"), in
// place, immediately before the config artifact renders. It applies the
// same field-level gates DeriveGeneratedBindings applies -- disabled mode,
// self-reference, bindableReference, lookup presence -- so a value is
// tokenised precisely where derivation would bind it; the two passes can
// never disagree about which leaves are reference leaves. It emits no
// diagnostics of its own: DeriveGeneratedBindings runs over the same live
// item maps immediately afterwards and remains the reporting layer for
// every skip class (missing lookups, unknown IDs, unsafe keys).
// mintedReferenceToken records one leaf the substitution rewrote, at the
// candidate-style path binding coverage is checked against.
type mintedReferenceToken struct {
	ItemKey string
	Path    string
	Token   string
}

func substituteReferenceTokens(
	items map[string]map[string]any,
	context BindingContext,
	resourceType string,
	lookupKeys map[string]map[string]string,
) []mintedReferenceToken {
	var minted []mintedReferenceToken
	if context.Mode == deployment.ReferenceBindingDisabled {
		return nil
	}
	for _, field := range canonjson.SortedStrings(mapKeys(context.References)) {
		spec, ok := context.References[field]
		if !ok {
			continue
		}
		if !bindableReference(resourceType, spec.Referent, context) {
			continue
		}
		keyMap := lookupKeys[lookupKeyMapKey(spec.Referent, spec.IDField)]
		if len(keyMap) == 0 {
			continue
		}
		// Mirror fieldCandidates' dotted-name rule exactly: a field whose
		// segments are not all identifier-shaped is one literal field name
		// containing dots, never a traversal path.
		segments := strings.Split(field, ".")
		for _, segment := range segments {
			if !identifierSegmentPattern.MatchString(segment) {
				segments = []string{field}
				break
			}
		}
		for _, key := range canonjson.SortedStrings(mapKeys(items)) {
			item, ok := items[key]
			if !ok {
				continue
			}
			substituteAtPath(item, segments, key, "", spec.Referent, spec.IDField, keyMap, &minted)
		}
	}
	return minted
}

// substituteAtPath descends container segment by segment -- fanning out
// over list elements at every level with the same index bookkeeping as
// fieldCandidates -- and rewrites the terminal segment's value(s).
// Substitution is value-level, so the set-block/plain-path distinction that
// matters for binding addressing does not arise here: both walks reach the
// same leaves, and every rewrite is recorded so CompileTransformArtifacts
// can prove a covering binding was derived for it.
func substituteAtPath(
	container map[string]any,
	segments []string,
	itemKey, concretePath, referent, idField string,
	keyMap map[string]string,
	minted *[]mintedReferenceToken,
) {
	head := segments[0]
	leafPath := head
	if concretePath != "" {
		leafPath = concretePath + "." + head
	}
	if len(segments) == 1 {
		switch value := container[head].(type) {
		case []any:
			substituteListElements(value, itemKey, leafPath, referent, idField, keyMap, minted)
		default:
			if token, ok := referenceToken(value, referent, idField, keyMap); ok {
				container[head] = token
				*minted = append(*minted, mintedReferenceToken{ItemKey: itemKey, Path: leafPath, Token: token})
			}
		}
		return
	}
	next, present := container[head]
	if !present {
		return
	}
	substituteThroughValue(next, segments[1:], itemKey, leafPath, referent, idField, keyMap, minted)
}

// substituteThroughValue continues the descent through one intermediate
// value, fanning arrays (including arrays of arrays) with fieldCandidates'
// exact index bookkeeping.
func substituteThroughValue(
	value any,
	rest []string,
	itemKey, concretePath, referent, idField string,
	keyMap map[string]string,
	minted *[]mintedReferenceToken,
) {
	switch typed := value.(type) {
	case map[string]any:
		substituteAtPath(typed, rest, itemKey, concretePath, referent, idField, keyMap, minted)
	case []any:
		for index, child := range typed {
			if child == nil {
				continue
			}
			substituteThroughValue(child, rest, itemKey, fmt.Sprintf("%s[%d]", concretePath, index), referent, idField, keyMap, minted)
		}
	}
}

// substituteListElements rewrites token-eligible elements of one terminal
// list, replicating bindValue's whole-list bail exactly: a list holding any
// non-zero-sentinel element the binder calls unbindable (null, nested
// lists, objects, empty strings) is left entirely untouched, because
// derivation will refuse to bind it and a minted token would otherwise have
// no resolver.
func substituteListElements(
	arr []any,
	itemKey, leafPath, referent, idField string,
	keyMap map[string]string,
	minted *[]mintedReferenceToken,
) {
	for _, child := range arr {
		if zeroSentinel(child) {
			continue
		}
		if !bindableListElement(child) {
			return
		}
	}
	for index, child := range arr {
		if zeroSentinel(child) {
			continue
		}
		if token, ok := referenceToken(child, referent, idField, keyMap); ok {
			arr[index] = token
			*minted = append(*minted, mintedReferenceToken{ItemKey: itemKey, Path: leafPath, Token: token})
		}
	}
}

// BindingPathCovers reports whether a binding at bindingPath resolves the
// leaf at leafPath: an exact match, or the leaf sitting anywhere below the
// binding's path (the set-block complete-leaf form binds the block and
// resolves every reference member under it). Exported because envgen's
// render-time totality gate applies the identical rule against committed
// bindings.
func BindingPathCovers(bindingPath, leafPath string) bool {
	return leafPath == bindingPath ||
		strings.HasPrefix(leafPath, bindingPath+".") ||
		strings.HasPrefix(leafPath, bindingPath+"[")
}

// assertMintedTokensCovered fails the compile if any token the substitution
// minted has no covering derived binding. Substitution and derivation walk
// the same gates over the same items, so a gap between them is a traversal
// divergence bug; publishing it would commit a token nothing resolves,
// which on a string-typed provider field would flow to the provider as a
// literal. Loud beats silent.
func assertMintedTokensCovered(minted []mintedReferenceToken, binding GeneratedBindingsResult, resourceType string) error {
	for _, m := range minted {
		fields, _ := binding.Resources[resourceType+"."+m.ItemKey].(map[string]any)
		covered := false
		for bindingPath := range fields {
			if BindingPathCovers(bindingPath, m.Path) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf(
				"substitution minted token %s at %s.%s.%s but derivation produced no covering binding; refusing to publish an unresolvable token (traversal divergence)",
				jsonQuote(m.Token), resourceType, m.ItemKey, m.Path,
			)
		}
	}
	return nil
}

// referenceToken maps a raw ID present in keyMap to its qualified token.
// Everything else -- sentinels, unknown IDs, values already token-shaped,
// non-scalar values, and keys the interpolation guard would refuse -- is
// left untouched for DeriveGeneratedBindings to classify and report. The
// interpolation check here prevents ever *minting* a committed token that
// the derive layer's unsafe_key guard would then strand unresolvable.
//
// Tokens are self-describing (Phase 7 revision,
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md): a
// canonical edge (idField "") mints the bare "<referent>.<key>" token
// unchanged; a declared alternate-space edge mints the explicit
// "<referent>.<key>.<idField>" form so the committed value names its own
// space rather than relying on the edge's declaration to decode it. Never
// mints ".id" -- that explicit spelling is accepted on read as a synonym
// for bare, but is not a minted shape.
func referenceToken(value any, referent, idField string, keyMap map[string]string) (string, bool) {
	ident, err := pythonTransformString(value)
	if err != nil {
		return "", false
	}
	if tokenShaped(ident) {
		return "", false
	}
	referentKey, ok := keyMap[ident]
	if !ok {
		return "", false
	}
	if strings.Contains(referentKey, "${") || strings.Contains(referentKey, "%{") {
		return "", false
	}
	token := referent + "." + referentKey
	if idField != "" {
		token += "." + idField
	}
	return token, true
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
	// keySets caches each referent's key_by_id value set, built on first
	// token lookup for that referent. resolve runs once per candidate value
	// and keyMap is the same map object across every call for one referent,
	// so this is keyed by referent (not rebuilt per value).
	keySets map[string]map[string]struct{}
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

// keySetFor returns the set of keys keyMap's ID->key entries map onto for
// the given referent, building and caching it the first time a token is
// seen for that referent. A token is validated by key membership alone, so
// this set -- not the ID->key map itself -- is the only structure a token
// lookup ever consults.
func (b *generatedBindingsBuilder) keySetFor(referent string, keyMap map[string]string) map[string]struct{} {
	if set, ok := b.keySets[referent]; ok {
		return set
	}
	set := make(map[string]struct{}, len(keyMap))
	for _, referentKey := range keyMap {
		set[referentKey] = struct{}{}
	}
	if b.keySets == nil {
		b.keySets = map[string]map[string]struct{}{}
	}
	b.keySets[referent] = set
	return set
}

// tokenShaped reports whether ident has the qualified-token shape (an
// identifier segment, a dot, a remainder) without regard to which referent
// it names. It classifies a value that fails the spec.Referent prefix
// check as a mismatched token rather than a plain ID: provider tenant IDs
// in this pipeline are never dotted, so a dotted, identifier-prefixed
// value is exhaustively a token for some other referent, never a
// coincidental ID.
func tokenShaped(ident string) bool {
	dot := strings.IndexByte(ident, '.')
	return dot > 0 && identifierSegmentPattern.MatchString(ident[:dot])
}

// splitReferenceTokenRemainder splits the portion of a token after its
// "<referent>." prefix into key and explicit id-space suffix. Reference
// keys are always transform.SlugifyTransformKey output, which never
// contains a dot, so the FIRST dot in the remainder (if any) unambiguously
// opens the optional "<attribute>" suffix from the Phase 7 self-describing
// grammar (docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md):
// there is no ambiguity to resolve by key-map membership because the key
// half can never itself contain the separator. space is "" when the
// remainder is bare. ok is false when a suffix is present but is not
// itself identifier-shaped -- the grammar admits only an identifier there,
// so such a value is not a recognized token in either shape and callers
// must not treat it as one.
func splitReferenceTokenRemainder(remainder string) (key, space string, ok bool) {
	dot := strings.IndexByte(remainder, '.')
	if dot < 0 {
		return remainder, "", true
	}
	space = remainder[dot+1:]
	if !identifierSegmentPattern.MatchString(space) {
		return "", "", false
	}
	return remainder[:dot], space, true
}

// normalizeIDSpace maps the explicit ".id" spelling to "", the same value
// spec.IDField carries for a canonical edge, so the two spellings compare
// equal everywhere a declared space is checked.
func normalizeIDSpace(space string) string {
	if space == "id" {
		return ""
	}
	return space
}

// displayIDSpace is normalizeIDSpace's inverse for note text: "" reads as
// "id" rather than an empty string.
func displayIDSpace(space string) string {
	if space == "" {
		return "id"
	}
	return space
}

// resolve ports deriveGeneratedBindings's local `resolve` closure, extended
// to consume the qualified reference token the P1 substitution pass commits
// in place of a raw tenant ID: "<spec.Referent>.<key>" (bare, canonical
// space) or "<spec.Referent>.<key>.<field>" (explicit space, Phase 7
// revision -- docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md).
// A token is recognised by an exact "<spec.Referent>." prefix, stripped
// verbatim, and validated by key membership in key_by_id's value set (never
// an ID->key hop; the key is already in the value); reference keys never
// contain a dot (transform.SlugifyTransformKey), so the remainder splits
// unambiguously into key and optional explicit space. The token's OWN
// suffix -- not spec.IDField -- is authoritative for which space it names;
// a bare token and an explicit ".id" token both mean canonical, and a
// suffix that disagrees with spec.IDField is a loud, fail-closed skip
// rather than a silent resolution through either space. Old-shape raw IDs
// remain valid indefinitely: the migration path this file's task brief
// requires.
func (b *generatedBindingsBuilder) resolve(spec TransformReferenceSpec, keyMap map[string]string, key, fieldPath string, value any) (*string, error) {
	ident, err := pythonTransformString(value)
	if err != nil {
		return nil, err
	}
	var referentKey string
	ownPrefix := spec.Referent + "."
	var tokenKey, tokenSpace string
	tokenShapeOK := false
	if strings.HasPrefix(ident, ownPrefix) {
		tokenKey, tokenSpace, tokenShapeOK = splitReferenceTokenRemainder(ident[len(ownPrefix):])
	}
	switch {
	case tokenShapeOK:
		if _, known := b.keySetFor(spec.Referent, keyMap)[tokenKey]; !known {
			b.count("token_key_unknown", 1)
			b.note("%s.%s.%s value %s skipped; token key is unknown to %s", b.resourceType, key, fieldPath, jsonQuote(ident), spec.Referent)
			return nil, nil
		}
		// Tokens are self-describing (Phase 7 revision,
		// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md):
		// the token's own suffix -- not the edge's declared IDField -- names
		// which space it decodes through. A bare token and an explicit ".id"
		// token both name the canonical space. A committed token whose space
		// disagrees with what this edge declares is never silently resolved
		// through either space; it is skipped here, loud and fail-closed, the
		// same way a stranded token is today -- the skip leaves the leaf
		// uncovered, which gen-env's committed-token totality gate then
		// refuses outright.
		if normalizeIDSpace(tokenSpace) != spec.IDField {
			b.count("space_mismatch", 1)
			b.note(
				"%s.%s.%s value %s skipped; token names id space %s but this edge declares %s",
				b.resourceType, key, fieldPath, jsonQuote(ident), displayIDSpace(tokenSpace), displayIDSpace(spec.IDField),
			)
			return nil, nil
		}
		referentKey = tokenKey
	case tokenShaped(ident):
		b.count("token_referent_mismatch", 1)
		b.note("%s.%s.%s value %s skipped; token does not name %s", b.resourceType, key, fieldPath, jsonQuote(ident), spec.Referent)
		return nil, nil
	default:
		// The keyMap is deliberately not consulted before this return: under
		// TokensOnly the outcome for a raw ID must not depend on the lookup at
		// all, not merely produce no binding when the lookup misses. See
		// BindingContext.TokensOnly.
		if b.context.TokensOnly {
			b.count("raw_id_render_only", 1)
			b.note("%s.%s.%s value %s skipped; raw ids bind at transform, not render", b.resourceType, key, fieldPath, jsonQuote(ident))
			return nil, nil
		}
		found, ok := keyMap[ident]
		if !ok {
			b.count("id_absent", 1)
			b.note("%s.%s.%s value %s skipped; id is absent from %s lookup", b.resourceType, key, fieldPath, jsonQuote(ident), spec.Referent)
			return nil, nil
		}
		referentKey = found
	}
	if strings.Contains(referentKey, "${") || strings.Contains(referentKey, "%{") {
		b.count("unsafe_key", 1)
		b.note("%s.%s.%s value %s skipped; referent key contains a template interpolation", b.resourceType, key, fieldPath, jsonQuote(ident))
		return nil, nil
	}
	referentRoot, ok := b.context.ResourceRoots[spec.Referent]
	if !ok {
		return nil, fmt.Errorf("cross-state reference %s has no deployment root", spec.Referent)
	}
	quoted, err := RenderHclQuotedString(referentKey)
	if err != nil {
		return nil, err
	}
	// IDField "" selects the canonical iw_reference_ids output (byte-
	// identical to every edge that predates this field); a declared
	// alternate space selects its sibling output, iw_reference_ids_<field>
	// (see docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md).
	outputName := "iw_reference_ids"
	if spec.IDField != "" {
		outputName = "iw_reference_ids_" + spec.IDField
	}
	expr := "data.terraform_remote_state." + referentRoot + ".outputs." + outputName + "." + spec.Referent + "[" + quoted + "]"
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

// bindSetBlockField binds a reference field whose dotted path crosses a
// set-nested block. An indexed path into a set block names nothing -- set
// members have no stable order -- and gen-env's schema-path validator refuses
// it with "bind the complete block leaf". So this emits exactly that: one
// binding per concrete block occurrence, at the path of the set block itself,
// whose expression renders the entire block value with the reference members
// resolved to remote-state lookups and everything else reproduced literally.
func (b *generatedBindingsBuilder) bindSetBlockField(
	spec TransformReferenceSpec,
	keyMap map[string]string,
	items map[string]map[string]any,
	field string,
	setIndex int,
	reason string,
) error {
	segments := strings.Split(field, ".")
	prefix := segments[:setIndex+1]
	remainder := segments[setIndex+1:]
	for _, key := range canonjson.SortedStrings(mapKeys(items)) {
		item, ok := items[key]
		if !ok {
			continue
		}
		var visit func(value any, segmentIndex int, concretePath string) error
		visit = func(value any, segmentIndex int, concretePath string) error {
			if segmentIndex == len(prefix) {
				expression, err := b.renderSetBlockLeaf(spec, keyMap, key, concretePath, value, remainder)
				if err != nil {
					return err
				}
				if expression != nil {
					b.assign(key, concretePath, *expression, reason)
				}
				return nil
			}
			if arr, ok := value.([]any); ok {
				for index, child := range arr {
					if child == nil {
						continue
					}
					if err := visit(child, segmentIndex, fmt.Sprintf("%s[%d]", concretePath, index)); err != nil {
						return err
					}
				}
				return nil
			}
			record, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			childValue, present := record[prefix[segmentIndex]]
			if !present {
				return nil
			}
			nextPath := prefix[segmentIndex]
			if concretePath != "" {
				nextPath = concretePath + "." + prefix[segmentIndex]
			}
			return visit(childValue, segmentIndex+1, nextPath)
		}
		if err := visit(item, 0, ""); err != nil {
			return err
		}
	}
	return nil
}

// renderSetBlockLeaf renders one set block's complete value as an HCL
// expression, resolving the reference member named by remainder. A block
// value this cannot reproduce faithfully binds nothing -- the tfvars literal
// underneath stays authoritative -- because a complete-leaf expression that
// silently dropped or reshaped a member would rewrite configuration the
// operator never asked to change.
func (b *generatedBindingsBuilder) renderSetBlockLeaf(
	spec TransformReferenceSpec,
	keyMap map[string]string,
	key, leafPath string,
	value any,
	remainder []string,
) (*string, error) {
	arr, isArray := value.([]any)
	if !isArray {
		if value == nil {
			return nil, nil
		}
		b.count("unbindable_set_block", 1)
		b.skipped++
		b.note("%s.%s.%s skipped; set block value is not a list", b.resourceType, key, leafPath)
		return nil, nil
	}
	boundBefore := b.bound
	elements := make([]string, 0, len(arr))
	for index, element := range arr {
		rendered, err := b.renderSetBlockMember(spec, keyMap, key, fmt.Sprintf("%s[%d]", leafPath, index), element, remainder)
		if err != nil {
			return nil, err
		}
		if rendered == nil {
			b.count("unbindable_set_block", 1)
			b.skipped++
			b.note("%s.%s.%s skipped; set block member cannot be rendered faithfully", b.resourceType, key, leafPath)
			return nil, nil
		}
		elements = append(elements, *rendered)
	}
	if b.bound == boundBefore {
		return nil, nil
	}
	expr := "[" + strings.Join(elements, ", ") + "]"
	return &expr, nil
}

// renderSetBlockMember renders one value inside a set block. At the remainder
// path's end sits the reference member, whose values resolve through the
// referent lookup exactly as list bindings do; every other member is
// reproduced as a literal, faithfully typed -- a number stays a number --
// because the expression replaces the whole block value in the generated
// root.
func (b *generatedBindingsBuilder) renderSetBlockMember(
	spec TransformReferenceSpec,
	keyMap map[string]string,
	key, fieldPath string,
	value any,
	remainder []string,
) (*string, error) {
	if len(remainder) == 0 {
		if arr, ok := value.([]any); ok {
			fragments := make([]string, 0, len(arr))
			for index, child := range arr {
				if zeroSentinel(child) {
					continue
				}
				if !bindableListElement(child) {
					return nil, nil
				}
				resolved, err := b.resolve(spec, keyMap, key, fmt.Sprintf("%s[%d]", fieldPath, index), child)
				if err != nil {
					return nil, err
				}
				if resolved == nil {
					b.skipped++
					literal, ok, err := renderHclLiteral(child)
					if err != nil {
						return nil, err
					}
					if !ok {
						return nil, nil
					}
					fragments = append(fragments, literal)
					continue
				}
				b.bound++
				fragments = append(fragments, *resolved)
			}
			expr := "[" + strings.Join(fragments, ", ") + "]"
			return &expr, nil
		}
		if value == nil || !bindableListElement(value) {
			return nil, nil
		}
		resolved, err := b.resolve(spec, keyMap, key, fieldPath, value)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			b.bound++
			return resolved, nil
		}
		b.skipped++
		literal, ok, err := renderHclLiteral(value)
		if err != nil || !ok {
			return nil, err
		}
		return &literal, nil
	}
	record, isRecord := value.(map[string]any)
	if !isRecord {
		return nil, nil
	}
	memberNames := canonjson.SortedStrings(mapKeys(record))
	parts := make([]string, 0, len(record))
	for _, name := range memberNames {
		var rendered *string
		if name == remainder[0] {
			child, err := b.renderSetBlockMember(spec, keyMap, key, fieldPath+"."+name, record[name], remainder[1:])
			if err != nil {
				return nil, err
			}
			rendered = child
		} else {
			literal, ok, err := renderHclLiteral(record[name])
			if err != nil {
				return nil, err
			}
			if ok {
				rendered = &literal
			}
		}
		if rendered == nil {
			return nil, nil
		}
		parts = append(parts, name+" = "+*rendered)
	}
	expr := "{ " + strings.Join(parts, ", ") + " }"
	return &expr, nil
}

// renderHclLiteral reproduces a JSON value as HCL, faithfully typed. The
// false return marks a value this cannot render (and the binding that
// contains it must not be emitted); it is distinct from an error, which marks
// malformed input.
func renderHclLiteral(value any) (string, bool, error) {
	switch typed := value.(type) {
	case nil:
		return "null", true, nil
	case bool:
		if typed {
			return "true", true, nil
		}
		return "false", true, nil
	case string:
		quoted, err := RenderHclQuotedString(typed)
		if err != nil {
			return "", false, err
		}
		return quoted, true, nil
	case json.Number:
		token, err := canonjson.CanonicalNumberToken(string(typed))
		if err != nil {
			return "", false, nil
		}
		return token, true, nil
	case float64:
		token, err := pythonTransformString(typed)
		if err != nil {
			return "", false, nil
		}
		return token, true, nil
	case []any:
		parts := make([]string, 0, len(typed))
		for _, child := range typed {
			rendered, ok, err := renderHclLiteral(child)
			if err != nil || !ok {
				return "", false, err
			}
			parts = append(parts, rendered)
		}
		return "[" + strings.Join(parts, ", ") + "]", true, nil
	case map[string]any:
		parts := make([]string, 0, len(typed))
		for _, name := range canonjson.SortedStrings(mapKeys(typed)) {
			rendered, ok, err := renderHclLiteral(typed[name])
			if err != nil || !ok {
				return "", false, err
			}
			parts = append(parts, name+" = "+rendered)
		}
		return "{ " + strings.Join(parts, ", ") + " }", true, nil
	default:
		return "", false, nil
	}
}

func (b *generatedBindingsBuilder) reasonFor(spec TransformReferenceSpec) string {
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
// the original implementation. Lookup reads stay in the caller.
func DeriveGeneratedBindings(context BindingContext, items map[string]map[string]any, lookupKeys map[string]map[string]string, resourceType string) (GeneratedBindingsResult, error) {
	b := &generatedBindingsBuilder{
		resourceType: resourceType,
		context:      context,
		resources:    map[string]any{},
	}
	if context.Mode == deployment.ReferenceBindingDisabled {
		return GeneratedBindingsResult{Resources: b.resources, Notes: b.notes}, nil
	}
	// Two reference fields crossing the same set block would each bind the
	// complete block leaf at the same (item, path) key: the second assign
	// overwrites the first, whose references are then reproduced literally
	// inside the surviving expression (adversarial-review finding). Grouped
	// block resolution is not implemented, so the shape is refused up
	// front, before any value can be minted or bound. No shipped pack
	// declares it (corpus audited 2026-07-30).
	blockOwners := map[string]string{}
	for _, field := range canonjson.SortedStrings(mapKeys(context.References)) {
		setIndex, throughSet := context.SetBlockFields[field]
		if !throughSet {
			continue
		}
		segments := strings.Split(field, ".")
		prefix := strings.Join(segments[:setIndex+1], ".")
		if other, taken := blockOwners[prefix]; taken {
			return GeneratedBindingsResult{}, fmt.Errorf(
				"reference fields %s and %s both cross set block %s of %s; binding them independently would overwrite one resolution with the other, so this shape is refused until grouped set-block resolution exists",
				other, field, prefix, resourceType,
			)
		}
		blockOwners[prefix] = field
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
		keyMap := lookupKeys[lookupKeyMapKey(spec.Referent, spec.IDField)]
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
			if setIndex, throughSet := context.SetBlockFields[field]; throughSet {
				if err := b.bindSetBlockField(spec, keyMap, items, field, setIndex, reason); err != nil {
					return GeneratedBindingsResult{}, err
				}
				continue
			}
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
// the original implementation.
func RenderGeneratedBindings(resources map[string]any) (string, error) {
	return canonjson.RenderLosslessArtifactJSON(map[string]any{"resources": resources})
}

// ComputeTransformArtifactPaths ports transformArtifactPaths from
// the original implementation. Named ComputeTransformArtifactPaths
// rather than TransformArtifactPaths (the TS function and its return-type
// interface share one lowercase/uppercase-only name in TS, which Go's
// single, case-sensitive-but-not-namespace-separated identifier space for
// types and funcs cannot reproduce; the TransformArtifactPaths identifier
// above is reserved for the struct).
func ComputeTransformArtifactPaths(
	dep deployment.Deployment,
	resourceType, tenant string,
	artifactMode TransformArtifactMode,
) (TransformArtifactPaths, error) {
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
	configArtifactDirectory := configDirectory
	if artifactMode == TransformArtifactModeDataReferent {
		configArtifactDirectory = path.Join(configDirectory, "data")
	}
	config := path.Join(configArtifactDirectory, resourceType+ext)
	staleConfig := strings.TrimSuffix(config, ".json")
	if format == "hcl" {
		staleConfig = config + ".json"
	}
	legacyConfig := path.Join(configDirectory, resourceType+ext)
	legacyStaleConfig := strings.TrimSuffix(legacyConfig, ".json")
	if format == "hcl" {
		legacyStaleConfig = legacyConfig + ".json"
	}
	return TransformArtifactPaths{
		Config:            config,
		GeneratedBindings: path.Join(configDirectory, resourceType+".generated.expressions.json"),
		Imports:           path.Join(importsDirectory, resourceType+"_imports.tf"),
		LegacyConfig:      legacyConfig,
		LegacyStaleConfig: legacyStaleConfig,
		Lookup:            path.Join(configDirectory, "lookups", resourceType+".lookup.json"),
		LegacyLookup:      path.Join(configDirectory, resourceType+".lookup.json"),
		Moves:             path.Join(importsDirectory, resourceType+"_moves.tf"),
		StaleConfig:       staleConfig,
	}, nil
}

// removeIfPresent ports removeIfPresent from
// the original implementation.
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

// readOptionalUtf8 ports readOptionalUtf8 from the original implementation, kept
// package-private per this port's per-package convention for this small
// helper -- see go/internal/deployment/deployment.go's own copy, which
// this one mirrors exactly (including its procerr.ProcessFailure codes:
// unlike the original implementation's own throws, which are all
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

// loadLookup ports loadLookup from the original implementation.
func loadLookup(file string) (*TransformLookupData, error) {
	text, err := readOptionalUtf8(file, fmt.Sprintf("lookup for %s", path.Base(file)))
	if err != nil {
		return nil, err
	}
	if text == nil {
		return nil, nil
	}
	// Lookups are read from two possible locations (current and legacy), and
	// since the key-shrinkage guard they are read during compiles that never
	// mention the lookup otherwise. A bare parse error names neither which file
	// nor which of the two paths it came from, so the exact path is attached.
	value, err := canonjson.ParseDataJSONLosslessly(*text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	data, err := ParseLookupSidecar(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &data, nil
}

// tokenDependents reports the committed config artifacts in
// configDirectory that carry a qualified reference token for resourceType.
// The scan is textual (a quoted string opening with "<resourceType>."), the
// same signal the renderer keys on, so both format variants of tfvars are
// served and a refusal errs toward keeping the lookup.
func tokenDependents(configDirectory, resourceType string) ([]string, error) {
	entries, err := os.ReadDir(configDirectory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	prefix := resourceType + "."
	var dependents []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() ||
			!(strings.HasSuffix(name, ".auto.tfvars.json") || strings.HasSuffix(name, ".auto.tfvars")) {
			continue
		}
		raw, err := os.ReadFile(path.Join(configDirectory, name))
		if err != nil {
			return nil, err
		}
		// JSON configs are parsed so only string VALUES count -- a dotted
		// map key or field name can never block retirement. HCL configs
		// cannot be parsed here; their quoted-text scan stays conservative.
		if strings.HasSuffix(name, ".auto.tfvars.json") {
			if parsed, parseErr := canonjson.ParseDataJSONLosslessly(string(raw)); parseErr == nil {
				if jsonValueHasPrefix(parsed, prefix) {
					dependents = append(dependents, name)
				}
				continue
			}
		}
		if strings.Contains(string(raw), `"`+prefix) {
			dependents = append(dependents, name)
		}
	}
	return canonjson.SortedStrings(dependents), nil
}

// configOwnerType maps a committed tfvars filename back to the resource type
// that owns it, or "" when the name is not a config artifact.
func configOwnerType(name string) string {
	for _, suffix := range []string{".auto.tfvars.json", ".auto.tfvars"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return ""
}

// jsonStringValues collects every string VALUE in the decoded JSON. Keys and
// field names are never collected: a token is always a whole value, so exact
// value equality is both the precise test and the one that cannot be tripped
// by a dotted map key.
func jsonStringValues(value any, into map[string]bool) {
	switch typed := value.(type) {
	case string:
		into[typed] = true
	case []any:
		for _, child := range typed {
			jsonStringValues(child, into)
		}
	case map[string]any:
		for _, child := range typed {
			jsonStringValues(child, into)
		}
	}
}

// droppedBookKeys reports every key the committed lookup decodes that the
// freshly compiled one no longer does. The committed lookup is read through
// resolveLookup, so a tenant still holding its lookup at the pre-migration
// path is compared against, not silently treated as having no lookup at all.
func droppedBookKeys(configDirectory, resourceType string, fresh *TransformLookupData) ([]string, error) {
	committed, err := resolveLookup(configDirectory, resourceType, nil)
	if err != nil || committed == nil {
		return nil, err
	}
	var dropped []string
	for key := range committed.IDByKey {
		if _, kept := fresh.IDByKey[key]; !kept {
			dropped = append(dropped, key)
		}
	}
	return canonjson.SortedStrings(dropped), nil
}

// configTextTokenKeys reports which of the dropped keys one config document
// names as "<resourceType>.<key>".
//
// A JSON document is parsed and matched on whole string VALUES: a token is
// always an entire value, so exact equality is both the precise test and the
// one a dotted map key cannot trip. An HCL document cannot be parsed here, so
// it is matched on the quoted token text -- and an unparseable JSON document
// falls to the same textual rule rather than being read as clean.
func configTextTokenKeys(text string, jsonFormat bool, resourceType string, dropped []string) []string {
	values := map[string]bool{}
	parsed := false
	if jsonFormat {
		if decoded, err := canonjson.ParseDataJSONLosslessly(text); err == nil {
			jsonStringValues(decoded, values)
			parsed = true
		}
	}
	var named []string
	for _, key := range dropped {
		token := resourceType + "." + key
		var held bool
		if parsed {
			// A committed value may name this key through either token
			// spelling -- bare or the explicit "<referent>.<key>.<field>"
			// form an alternate-space edge mints (Phase 7 revision,
			// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md)
			// -- so the key comparison must strip an optional trailing
			// identifier suffix, not require an exact string match.
			for value := range values {
				if configValueNamesTokenKey(value, token) {
					held = true
					break
				}
			}
		} else {
			held = strings.Contains(text, `"`+token+`"`) || configTextSuffixedTokenPattern(token).MatchString(text)
		}
		if held {
			named = append(named, key)
		}
	}
	return named
}

// configValueNamesTokenKey reports whether committed value v names token
// (a bare "<resourceType>.<key>") either exactly or with an explicit
// "<attribute>" suffix appended.
func configValueNamesTokenKey(v, token string) bool {
	if v == token {
		return true
	}
	suffix, ok := strings.CutPrefix(v, token+".")
	return ok && identifierSegmentPattern.MatchString(suffix)
}

// configTextSuffixedTokenPattern matches token committed with an explicit
// id-space suffix inside unparsed (HCL) config text.
func configTextSuffixedTokenPattern(token string) *regexp.Regexp {
	return regexp.MustCompile(`"` + regexp.QuoteMeta(token) + `\.[A-Za-z_][A-Za-z0-9_]*"`)
}

// tokenKeyDependents reports, per dropped key, the committed config artifacts
// in configDirectory that still name "<resourceType>.<key>". Every config
// artifact is scanned, this type's own included: no file is exempt, because no
// publication path here is transactional enough to justify excusing one (see
// assertNoLookupKeyStranding).
func tokenKeyDependents(
	configDirectory, resourceType string,
	dropped []string,
) (map[string][]string, error) {
	entries, err := os.ReadDir(configDirectory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	dependents := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || configOwnerType(name) == "" {
			continue
		}
		raw, err := os.ReadFile(path.Join(configDirectory, name))
		if err != nil {
			return nil, err
		}
		for _, key := range configTextTokenKeys(
			string(raw), strings.HasSuffix(name, ".auto.tfvars.json"), resourceType, dropped,
		) {
			dependents[key] = append(dependents[key], name)
		}
	}
	for key, files := range dependents {
		dependents[key] = canonjson.SortedStrings(files)
	}
	return dependents, nil
}

// assertNoLookupKeyStranding refuses a lookup update that would drop a key some
// committed config still names by token.
//
// This is the symmetric half of the whole-lookup retirement guard above, and it
// is what makes lookup membership a SOUND signal for every consumer that keys on
// it -- gen-env's dropped-edge orphan scan most of all. Without it an ordinary
// referent re-transform (item renamed or deleted) shrinks the key set, the
// orphaned token stops decoding, and every render-time gate goes blind to it
// (adversarial-review finding, round 5).
//
// No dependent is exempt, including one the same run also selects. An
// exemption is only sound inside a successfully preflighted, rollback-capable
// publication transaction, and this repository has no such invocation path:
// the transform and adopt runners compile and publish each type immediately
// and independently and continue past a later member that skips or fails
// (round-3 re-review's deterministic repro), and even the batch helper's
// rollback is best-effort. A selection-scoped exemption therefore published
// the new lookup and then simply did not repair the dependent.
//
// The compiling type's OWN config is scanned too, on both sides of the write.
// It was briefly skipped on the argument that a type never mints a token
// naming itself (bindableReference refuses a self-reference outright). That
// argument covers MINTING a declared self-reference and nothing else: gen-env
// classifies a token by lookup membership over every string leaf, with no
// own-config exception, so an ordinary description reading
// "<type>.<key>" is a token claim to every render-time gate. Two checks,
// guarding two different failures, neither subsuming the other:
//
//   - the COMMITTED copy on disk guards the publication window.
//     PublishCompiledTransformArtifacts writes the lookup before the config, and
//     writes them directly rather than transactionally, so a failure between
//     the two leaves the old config beside the new lookup. A planned replacement
//     is not a transactional justification.
//   - the freshly compiled CONFIG TEXT guards the steady state after a
//     successful publish: a value this compile is about to commit that the new
//     lookup will not decode. A disk-only scan sees a clean tree and misses it
//     entirely.
//
// The cost is a real deadlock: renaming a referent item refuses until the
// referrer's committed reference is updated, and re-transforming the referrer
// first re-mints the same departing key from the still-committed lookup, so the
// operator must break the tie by hand. That is accepted deliberately -- loud
// and self-consistent beats quiet and stranded -- and is documented in the
// design spec's residuals. The message below states the manual remedy and
// claims no automatic one.
//
// Residual, deliberately named rather than claimed away: this guards the
// pipeline's own writes. A lookup deleted or edited by hand outside the pipeline
// is not covered.
func assertNoLookupKeyStranding(
	configDirectory, resourceType, configFile, configText string,
	fresh *TransformLookupData,
) error {
	dropped, err := droppedBookKeys(configDirectory, resourceType, fresh)
	if err != nil || len(dropped) == 0 {
		return err
	}
	dependents, err := tokenKeyDependents(configDirectory, resourceType, dropped)
	if err != nil {
		return err
	}
	if dependents == nil {
		dependents = map[string][]string{}
	}
	for _, key := range configTextTokenKeys(
		configText, strings.HasSuffix(configFile, ".json"), resourceType, dropped,
	) {
		dependents[key] = append(dependents[key], path.Base(configFile)+" (this run's pending output)")
	}
	if len(dependents) == 0 {
		return nil
	}
	var stranded []string
	for _, key := range dropped {
		if files, ok := dependents[key]; ok {
			stranded = append(stranded, fmt.Sprintf("%s.%s (%s)", resourceType, key, strings.Join(files, ", ")))
		}
	}
	return fmt.Errorf(
		"cannot publish %s's lookup sidecar: it would drop key(s) committed config still references by token — %s; nothing published. Re-running transform for those dependents first will NOT help (they re-mint the same key from the still-committed lookup): either restore the referent item whose key is leaving the lookup, or update each dependent's committed reference to the new key by hand (or delete that config and re-transform it after this one succeeds)",
		resourceType, strings.Join(stranded, "; "),
	)
}

// jsonValueHasPrefix reports whether any string VALUE in the decoded JSON
// carries the prefix -- keys and field names are never inspected.
func jsonValueHasPrefix(value any, prefix string) bool {
	switch typed := value.(type) {
	case string:
		return strings.HasPrefix(typed, prefix)
	case []any:
		for _, child := range typed {
			if jsonValueHasPrefix(child, prefix) {
				return true
			}
		}
	case map[string]any:
		for _, child := range typed {
			if jsonValueHasPrefix(child, prefix) {
				return true
			}
		}
	}
	return false
}

// resolveLookup ports resolveLookup from
// the original implementation. See TransformArtifactCompileOptions's
// LookupOverrides doc comment for why no separate "overrides provided"
// flag is threaded here.
//
// Dual-read for the Part B lookup migration (config/<tenant>/<type>.lookup.json
// -> config/<tenant>/lookups/<type>.lookup.json): the current path is tried
// first; only when it is absent (loadLookup's nil-without-error result) does
// this fall back to the legacy path. A malformed file at the current path
// still fails loudly rather than silently falling through to a stale legacy
// copy. Every resolveLookup caller -- deriveHclComments (HCL comments) and
// lookupKeyMaps (bindings derivation, including gen-env's LookupKeyMaps) --
// inherits this fallback for free.
func resolveLookup(configDirectory, referent string, overrides map[string]*TransformLookupData) (*TransformLookupData, error) {
	if data, ok := overrides[referent]; ok {
		return data, nil
	}
	data, err := loadLookup(path.Join(configDirectory, "lookups", referent+".lookup.json"))
	if err != nil {
		return nil, err
	}
	if data != nil {
		return data, nil
	}
	return loadLookup(path.Join(configDirectory, referent+".lookup.json"))
}

// lookupSidecarSuffix is the committed lookup's filename suffix, shared by
// both locations ComputeTransformArtifactPaths names (Lookup and
// LegacyLookup) and by resolveLookup's dual read.
const lookupSidecarSuffix = ".lookup.json"

// CommittedLookupReferents lists every resource type carrying a committed lookup
// under configDirectory, reading BOTH locations the migration spans: the
// current one (ComputeTransformArtifactPaths.Lookup, <dir>/lookups/) and the
// legacy one (ComputeTransformArtifactPaths.LegacyLookup, <dir>/ itself). A
// type present in both is listed once.
//
// Exported for gen-env's dropped-edge orphan scan, which must recognise a
// committed token whose referent the CURRENT pack no longer declares an edge
// to: pack metadata cannot name that type, but its lookup -- the only decoder
// the token has -- is still on disk. An absent directory is not an error;
// a tenant may have no lookups at all.
func CommittedLookupReferents(configDirectory string) ([]string, error) {
	seen := map[string]bool{}
	for _, directory := range []string{
		path.Join(configDirectory, "lookups"),
		configDirectory,
	} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, lookupSidecarSuffix) {
				continue
			}
			seen[strings.TrimSuffix(name, lookupSidecarSuffix)] = true
		}
	}
	referents := make([]string, 0, len(seen))
	for referent := range seen {
		referents = append(referents, referent)
	}
	return canonjson.SortedStrings(referents), nil
}

// CommittedLookupKeys returns, per referent, the set of item keys its committed
// lookup decodes -- id_by_key's keys, including the parser's inversion of
// key_by_id for a sidecar written before id_by_key existed. A referent with
// no committed lookup gets no entry at all, so a caller cannot mistake "no
// lookup" for "lookup with no keys".
//
// Lookup membership is what makes a token claim distinguishable from an
// innocent dotted string: only a key the lookup decodes could ever have been
// minted, and the retirement guard (tokenDependents) refuses to remove a lookup
// while any committed config still references it by token, so a lookup's
// presence is a sound signal rather than a race.
func CommittedLookupKeys(configDirectory string, referents []string) (map[string]map[string]bool, error) {
	output := map[string]map[string]bool{}
	for _, referent := range referents {
		if _, done := output[referent]; done {
			continue
		}
		lookup, err := resolveLookup(configDirectory, referent, nil)
		if err != nil {
			return nil, err
		}
		if lookup == nil {
			continue
		}
		keys := make(map[string]bool, len(lookup.IDByKey))
		for key := range lookup.IDByKey {
			keys[key] = true
		}
		output[referent] = keys
	}
	return output, nil
}

var systemConstantPattern = regexp.MustCompile(`^[A-Z0-9_]+$`)

// systemConstant ports systemConstant from
// the original implementation.
func systemConstant(value string) bool {
	return !strings.HasPrefix(value, "CUSTOM_") && value == strings.ToUpper(value) && systemConstantPattern.MatchString(value)
}

// displayFor ports displayFor from the original implementation.
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

// displayForReference resolves a committed reference value to its display
// name: a qualified token maps key -> id through the lookup before the
// ordinary id -> display lookup, so tokenised HCL trees keep the same
// human-readable comments raw-ID trees always had. Every other value keeps
// displayFor's legacy semantics.
func displayForReference(value any, referent string, lookup *TransformLookupData) (string, error) {
	if text, ok := value.(string); ok {
		prefix := referent + "."
		if strings.HasPrefix(text, prefix) {
			if id, ok := lookup.IDByKey[text[len(prefix):]]; ok {
				return displayFor(id, lookup.ByID)
			}
		}
	}
	return displayFor(value, lookup.ByID)
}

// deriveHclComments ports deriveHclComments from
// the original implementation.
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
				display, err := displayForReference(child, spec.Referent, lookup)
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
// the original implementation.
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
// the original implementation.
// lookupKeyMapKey names one entry of lookupKeyMaps' output: the referent
// alone for the canonical id space (idField ""), so every pre-existing
// caller's map key is byte-identical to before this field existed; a
// referent+"#"+field composite for a declared alternate space. "#" is not
// a valid resource-type or identifier-field character, so the composite
// can never collide with a bare referent name.
func lookupKeyMapKey(referent, idField string) string {
	if idField == "" {
		return referent
	}
	return referent + "#" + idField
}

func lookupKeyMaps(
	configDirectory string,
	references map[string]TransformReferenceSpec,
	overrides map[string]*TransformLookupData,
) (map[string]map[string]string, error) {
	output := map[string]map[string]string{}
	lookups := map[string]*TransformLookupData{}
	lookupsResolved := map[string]bool{}
	resolved := map[string]bool{}
	for _, spec := range references {
		mapKey := lookupKeyMapKey(spec.Referent, spec.IDField)
		if resolved[mapKey] {
			continue
		}
		resolved[mapKey] = true
		if !lookupsResolved[spec.Referent] {
			lookup, err := resolveLookup(configDirectory, spec.Referent, overrides)
			if err != nil {
				return nil, err
			}
			lookups[spec.Referent] = lookup
			lookupsResolved[spec.Referent] = true
		}
		lookup := lookups[spec.Referent]
		if lookup == nil {
			output[mapKey] = nil
			continue
		}
		if spec.IDField == "" {
			output[mapKey] = lookup.KeyByID
			continue
		}
		// A missing space is deliberately never derived from the canonical
		// maps (see TransformLookupData.Spaces): the alternate attribute's
		// values do not exist there, so absence stays a nil key map, which
		// the derivation and substitution layers both already treat as a
		// missing-lookup report.
		if lookup.Spaces == nil {
			output[mapKey] = nil
			continue
		}
		space, ok := lookup.Spaces[spec.IDField]
		if !ok {
			output[mapKey] = nil
			continue
		}
		output[mapKey] = space.KeyByID
	}
	return output, nil
}

// LookupKeyMaps exposes lookupKeyMaps to render-time binding derivation
// (gen-env, once the committed generated-bindings cache became optional).
// Only the on-disk arm is offered: the override map exists for a compile
// batch whose members' fresh lookups are authoritative for same-batch
// references, and no such batch exists at render time -- the renderer reads
// exactly the lookups that are committed. Keeping this a delegation rather
// than a second lookup reader is what guarantees the derivation gen-env runs
// sees byte-identical key maps to the one transform ran, including a
// referent whose lookup is absent (a nil entry, which the deriver reports as
// a missing lookup rather than binding past).
func LookupKeyMaps(
	configDirectory string,
	references map[string]TransformReferenceSpec,
) (map[string]map[string]string, error) {
	return lookupKeyMaps(configDirectory, references, nil)
}

// compileLookup ports compileLookup from
// the original implementation.
func compileLookup(options TransformArtifactCompileOptions) (*TransformLookupData, *string, error) {
	if options.LookupNameField == nil {
		return nil, nil, nil
	}
	shape := TransformLookupShapeLegacy
	var spaces []string
	if options.ArtifactMode == TransformArtifactModeDataReferent {
		shape = TransformLookupShapeNested
	} else {
		spaces = options.PublishSpaces
	}
	text, err := RenderTransformLookupWithShape(
		options.Result.Items, options.Result.Originals, *options.LookupNameField, shape, spaces,
	)
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
// the original implementation: "read and validate every input
// needed to publish one ordinary transform artifact set. This function
// never creates, writes, renames, or removes a filesystem entry."
func CompileTransformArtifacts(options TransformArtifactCompileOptions) (CompiledTransformArtifacts, error) {
	paths, err := ComputeTransformArtifactPaths(
		options.Deployment, options.ResourceType, options.Tenant, options.ArtifactMode,
	)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	configDirectory, err := deployment.DeploymentConfigDir(options.Deployment, options.Tenant)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	freshLookup, lookupText, err := compileLookup(options)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	if options.ArtifactMode == TransformArtifactModeDataReferent {
		configText, err := renderDeploymentTfvars(
			options.Deployment, options.Result.Items, options.References,
			options.ResourceType, options.Tenant, options.VariableName, options.LookupOverrides,
		)
		if err != nil {
			return CompiledTransformArtifacts{}, err
		}
		// Data-referent items cannot contain generated reference tokens, but the
		// existing lookup-shrinkage guard still protects committed dependents
		// when a data lookup retires a previously published key.
		if lookupText != nil && freshLookup != nil {
			if err := assertNoLookupKeyStranding(
				configDirectory, options.ResourceType, paths.Config, configText, freshLookup,
			); err != nil {
				return CompiledTransformArtifacts{}, err
			}
		}
		return CompiledTransformArtifacts{
			ArtifactMode:           options.ArtifactMode,
			ConfigText:             configText,
			LookupText:             lookupText,
			RemoveLookupWhenAbsent: options.RemoveLookupWhenAbsent,
			OnDiagnostic:           options.OnDiagnostic,
			Paths:                  paths,
			ResourceType:           options.ResourceType,
		}, nil
	}
	// Once committed configs reference this type by token, its lookup is the
	// only decoder those tokens have: inferred-lifecycle removal must refuse
	// while any dependent survives, not strand them.
	if lookupText == nil && options.RemoveLookupWhenAbsent {
		dependents, err := tokenDependents(configDirectory, options.ResourceType)
		if err != nil {
			return CompiledTransformArtifacts{}, err
		}
		if len(dependents) > 0 {
			return CompiledTransformArtifacts{}, fmt.Errorf(
				"cannot remove %s's lookup sidecar: committed config still references it by token (%s); re-run transform for the dependents or restore the referent before retiring its lookup",
				options.ResourceType, strings.Join(dependents, ", "),
			)
		}
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

	// The key maps are resolved before the config renders so committed
	// reference fields can be rewritten to qualified tokens first. The
	// sidecar compiled above this point deliberately still maps real IDs
	// -- it is the lookup the tokens decode through -- and imports/moves
	// render from Originals, which the substitution never touches.
	keyMaps, err := lookupKeyMaps(configDirectory, options.References, options.LookupOverrides)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	// Tokens are a JSON-format contract: only JSON tfvars can be
	// leaf-verified end to end by the render-time totality gate, so
	// HCL-format deployments keep literal IDs entirely (and envgen refuses
	// token-shaped values that appear in an HCL config by other means).
	tfvarsFormat, err := deployment.DeploymentTfvarsFormat(options.Deployment)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	var minted []mintedReferenceToken
	if tfvarsFormat == "json" {
		minted = substituteReferenceTokens(options.Result.Items, options.BindingContext, options.ResourceType, keyMaps)
	}

	configText, err := renderDeploymentTfvars(
		options.Deployment, options.Result.Items, options.References,
		options.ResourceType, options.Tenant, options.VariableName, options.LookupOverrides,
	)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}

	// A lookup that keeps existing but stops decoding a key strands its tokens
	// exactly as thoroughly as a removed lookup does, so the same refusal applies
	// key by key. This runs after the config renders because the check needs
	// this compile's own pending config text as well as what is on disk; it is
	// still pure compilation, so nothing has been published when it refuses.
	if lookupText != nil && freshLookup != nil {
		if err := assertNoLookupKeyStranding(
			configDirectory, options.ResourceType, paths.Config, configText, freshLookup,
		); err != nil {
			return CompiledTransformArtifacts{}, err
		}
	}

	binding, err := DeriveGeneratedBindings(options.BindingContext, options.Result.Items, keyMaps, options.ResourceType)
	if err != nil {
		return CompiledTransformArtifacts{}, err
	}
	if err := assertMintedTokensCovered(minted, binding, options.ResourceType); err != nil {
		return CompiledTransformArtifacts{}, err
	}

	return CompiledTransformArtifacts{
		ArtifactMode:           options.ArtifactMode,
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

// PublishCompiledTransformArtifacts ports
// publishCompiledTransformArtifacts from
// the original implementation: "publish one fully compiled
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
	dataLane := compiled.ArtifactMode == TransformArtifactModeDataReferent

	configDirectory := path.Dir(compiled.Paths.Config)
	if err := os.MkdirAll(configDirectory, 0o777); err != nil {
		return TransformArtifactWriteResult{}, err
	}
	if !dataLane {
		importsDirectory := path.Dir(compiled.Paths.Imports)
		if err := os.MkdirAll(importsDirectory, 0o777); err != nil {
			return TransformArtifactWriteResult{}, err
		}
	}
	lookupDirectory := path.Dir(compiled.Paths.Lookup)
	if err := os.MkdirAll(lookupDirectory, 0o777); err != nil {
		return TransformArtifactWriteResult{}, err
	}
	if dataLane {
		for _, stalePath := range []string{compiled.Paths.Imports, compiled.Paths.Moves} {
			removedNow, err := removeIfPresent(stalePath)
			if err != nil {
				return TransformArtifactWriteResult{}, err
			}
			if removedNow {
				removed = append(removed, stalePath)
				note("removed stale " + stalePath)
			}
		}
	}

	if compiled.LookupText != nil {
		if err := os.WriteFile(compiled.Paths.Lookup, []byte(*compiled.LookupText), 0o666); err != nil {
			return TransformArtifactWriteResult{}, err
		}
		written = append(written, compiled.Paths.Lookup)
		note("wrote " + compiled.Paths.Lookup)
		// The lookup just landed at its current location; any copy left at
		// the pre-migration path is stale the instant this write commits,
		// the same "cache the instant a fresher artifact exists" logic
		// GeneratedBindings already applies to itself.
		removedLegacy, err := removeIfPresent(compiled.Paths.LegacyLookup)
		if err != nil {
			return TransformArtifactWriteResult{}, err
		}
		if removedLegacy {
			removed = append(removed, compiled.Paths.LegacyLookup)
			note("removed stale legacy lookup " + compiled.Paths.LegacyLookup)
		}
	} else if compiled.RemoveLookupWhenAbsent {
		removedNow, err := removeIfPresent(compiled.Paths.Lookup)
		if err != nil {
			return TransformArtifactWriteResult{}, err
		}
		if removedNow {
			removed = append(removed, compiled.Paths.Lookup)
			note("removed stale inferred lookup " + compiled.Paths.Lookup)
		}
		removedLegacy, err := removeIfPresent(compiled.Paths.LegacyLookup)
		if err != nil {
			return TransformArtifactWriteResult{}, err
		}
		if removedLegacy {
			removed = append(removed, compiled.Paths.LegacyLookup)
			note("removed stale legacy lookup " + compiled.Paths.LegacyLookup)
		}
	}

	if !dataLane && compiled.ExistingMoves == nil && compiled.RenderedMoves != nil {
		if err := os.WriteFile(compiled.Paths.Moves, []byte(*compiled.RenderedMoves), 0o666); err != nil {
			return TransformArtifactWriteResult{}, err
		}
		written = append(written, compiled.Paths.Moves)
		note(fmt.Sprintf(
			"RENAME(S) DETECTED: %d item(s) re-keyed — moved blocks staged in %s; copy into the env root alongside the imports file before plan/apply (RUNBOOK: Drift)",
			len(compiled.Moves.Moves), compiled.Paths.Moves,
		))
	} else if !dataLane && compiled.ExistingMoves != nil {
		if compiled.RenderedMoves == nil {
			note("preserved unresolved move evidence " + compiled.Paths.Moves + " (no newly derived moves this run)")
		} else {
			note("preserved byte-identical unresolved move evidence " + compiled.Paths.Moves)
		}
	}
	if !dataLane {
		for _, suppression := range compiled.Moves.Suppressed {
			note(fmt.Sprintf(
				"SUPPRESSED RENAME CANDIDATE: %s %s -> %s (import_id %s, reason=%s); no moved block emitted",
				compiled.ResourceType, jsonQuote(suppression.OldKey), jsonQuote(suppression.NewKey),
				jsonQuote(suppression.ImportID), suppression.Reason,
			))
		}
	}

	removedStale, err := removeIfPresent(compiled.Paths.StaleConfig)
	if err != nil {
		return TransformArtifactWriteResult{}, err
	}
	if removedStale {
		removed = append(removed, compiled.Paths.StaleConfig)
		note("removed stale " + compiled.Paths.StaleConfig)
	}
	if dataLane {
		for _, legacyPath := range []string{
			compiled.Paths.LegacyConfig,
			compiled.Paths.LegacyStaleConfig,
		} {
			removedLegacy, err := removeIfPresent(legacyPath)
			if err != nil {
				return TransformArtifactWriteResult{}, err
			}
			if removedLegacy {
				removed = append(removed, legacyPath)
				note("removed stale legacy config " + legacyPath)
			}
		}
	}
	if err := os.WriteFile(compiled.Paths.Config, []byte(compiled.ConfigText), 0o666); err != nil {
		return TransformArtifactWriteResult{}, err
	}
	written = append(written, compiled.Paths.Config)

	for _, message := range compiled.Binding.Notes {
		note("NOTE bindings: " + message)
	}
	// compiled.Binding is never written to disk: it is a pure function of
	// (items, pack reference edges, schema, lookups) that gen-env now
	// recomputes at render time via this same DeriveGeneratedBindings (the
	// predecessor commit's render-derivation bridge), so a committed
	// .generated.expressions.json is a stale cache the instant it exists.
	// Any copy left over from before this change -- or from a tree that has
	// not re-transformed yet -- is stale-cleaned like any other retired
	// artifact so it disappears on the tree's next transform/adopt run.
	removedBindings, err := removeIfPresent(compiled.Paths.GeneratedBindings)
	if err != nil {
		return TransformArtifactWriteResult{}, err
	}
	if removedBindings {
		removed = append(removed, compiled.Paths.GeneratedBindings)
		note("removed stale " + compiled.Paths.GeneratedBindings)
	}

	if dataLane {
		note("wrote " + compiled.Paths.Config)
	} else {
		if err := os.WriteFile(compiled.Paths.Imports, []byte(compiled.NewImports), 0o666); err != nil {
			return TransformArtifactWriteResult{}, err
		}
		written = append(written, compiled.Paths.Imports)
		note("wrote " + compiled.Paths.Config)
		note("wrote " + compiled.Paths.Imports)
	}

	if written == nil {
		written = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	return TransformArtifactWriteResult{Paths: compiled.Paths, Written: written, Removed: removed}, nil
}

// WriteTransformArtifacts ports writeTransformArtifacts from
// the original implementation: "materialize one ordinary
// transform artifact set with the legacy file lifecycle."
func WriteTransformArtifacts(options TransformArtifactCompileOptions) (TransformArtifactWriteResult, error) {
	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		return TransformArtifactWriteResult{}, err
	}
	return PublishCompiledTransformArtifacts(compiled)
}

// WriteDerivedTransformArtifact ports writeDerivedTransformArtifact from
// the original implementation: "derived resources write config
// only and intentionally create no imports."
func WriteDerivedTransformArtifact(
	dep deployment.Deployment,
	items map[string]map[string]any,
	references map[string]TransformReferenceSpec,
	resourceType, sourceType, tenant, variableName string,
	onDiagnostic func(string),
) (string, error) {
	paths, err := ComputeTransformArtifactPaths(
		dep, resourceType, tenant, TransformArtifactModeGenerated,
	)
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

// Package transformadoptparity compares the real Transform and Adopt kernels
// against small, source-backed parity fixtures. It is deliberately pure: the
// caller supplies a loaded pack root and fixture paths; this package neither
// reads environment variables nor starts Terraform or contacts providers.
package transformadoptparity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/adopt"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/textcompat"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

const (
	ReportKind    = "infrawright.transform_adopt_parity"
	ReportVersion = 1
)

// Result is the public outcome classification A6 maps to its CLI contract.
type Result string

const (
	ResultEqual                 Result = "equal"
	ResultClassifiedDifferences Result = "classified_differences"
	ResultEvidenceGates         Result = "evidence_gates"
	ResultReviewRequired        Result = "review_required"
)

var integerToken = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)
var listIndexToken = regexp.MustCompile(`^[+-]?[0-9]+$`)

// Error is the category marker for invalid parity fixtures and parity
// operations. Its prose is intentionally not a CLI contract.
type Error struct{ Message string }

func (e *Error) Error() string               { return e.Message }
func failf(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

// Context is the explicit repository and pack-root boundary for all work.
type Context struct {
	RepositoryRoot string
	Root           metadata.LoadedPackRoot
}

type Side struct {
	Present bool `json:"present"`
	Value   any  `json:"value,omitempty"`
}
type Expectation struct {
	Path           string   `json:"path"`
	Transform      Side     `json:"transform"`
	Adopt          Side     `json:"adopt"`
	Classification string   `json:"classification"`
	Disposition    string   `json:"disposition"`
	Reason         string   `json:"reason"`
	Evidence       []string `json:"evidence"`
}
type Fixture struct {
	FixtureVersion      int                       `json:"fixture_version"`
	Name                string                    `json:"name"`
	ResourceType        string                    `json:"resource_type"`
	Provenance          map[string]any            `json:"provenance"`
	RawItems            []any                     `json:"raw_items"`
	ProviderState       map[string]map[string]any `json:"provider_state"`
	ExpectedDifferences []Expectation             `json:"expected_differences"`
}
type Difference struct {
	Path      string `json:"path"`
	Transform Side   `json:"transform"`
	Adopt     Side   `json:"adopt"`
}

func record(value any, where string) (map[string]any, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, failf("%s must be an object", where)
	}
	return record, nil
}
func requireKeys(value map[string]any, keys []string, where string) error {
	missing := make([]string, 0)
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			missing = append(missing, key)
		}
	}
	missing = canonjson.SortedStrings(missing)
	if len(missing) > 0 {
		return failf("%s is missing required key %s", where, missing[0])
	}
	return nil
}
func rejectUnknown(value map[string]any, keys []string, where string) error {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	unknown := []string{}
	for key := range value {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	unknown = canonjson.SortedStrings(unknown)
	if len(unknown) > 0 {
		return failf("%s has unknown key %s", where, unknown[0])
	}
	return nil
}
func nonEmpty(value any, where string) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", failf("%s must be a non-empty string", where)
	}
	return text, nil
}
func stringList(value any, where string) ([]string, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, failf("%s must be a non-empty list", where)
	}
	out := make([]string, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		text, err := nonEmpty(item, fmt.Sprintf("%s[%d]", where, i))
		if err != nil {
			return nil, err
		}
		if seen[text] {
			return nil, failf("%s must not contain duplicates", where)
		}
		seen[text] = true
		out[i] = text
	}
	return out, nil
}
func validateJSON(value any, where string) error {
	switch typed := value.(type) {
	case nil, string, bool:
		return nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return failf("%s contains a non-finite number", where)
		}
		return nil
	case json.Number:
		if _, err := canonjson.CanonicalNumberToken(string(typed)); err != nil {
			return failf("%s contains a non-finite number", where)
		}
		return nil
	case []any:
		for i, item := range typed {
			if err := validateJSON(item, fmt.Sprintf("%s[%d]", where, i)); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for key, item := range typed {
			if err := validateJSON(item, where+"."+key); err != nil {
				return err
			}
		}
		return nil
	default:
		return failf("%s contains unsupported JSON value", where)
	}
}
func validateSide(value any, where string) (Side, error) {
	record, err := record(value, where)
	if err != nil {
		return Side{}, err
	}
	if err := rejectUnknown(record, []string{"present", "value"}, where); err != nil {
		return Side{}, err
	}
	if err := requireKeys(record, []string{"present"}, where); err != nil {
		return Side{}, err
	}
	present, ok := record["present"].(bool)
	if !ok {
		return Side{}, failf("%s.present must be a boolean", where)
	}
	value, hasValue := record["value"]
	if present && !hasValue {
		return Side{}, failf("%s.value is required when present is true", where)
	}
	if !present && hasValue {
		return Side{}, failf("%s.value must be absent when present is false", where)
	}
	if hasValue {
		if err := validateJSON(value, where+".value"); err != nil {
			return Side{}, err
		}
	}
	return Side{Present: present, Value: value}, nil
}
func pinnedGithub(url string, repository *string, version string) bool {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(url, prefix) {
		return false
	}
	rest := strings.TrimPrefix(url, prefix)
	marker := strings.Index(rest, "/blob/")
	if marker <= 0 {
		return false
	}
	repo, source := rest[:marker], rest[marker+6:]
	slash := strings.IndexByte(source, '/')
	if slash <= 0 || repository != nil && repo != *repository {
		return false
	}
	ref, path := source[:slash], source[slash+1:]
	return (ref == version || ref == "v"+version) && path != "" && !strings.HasPrefix(path, "#")
}
func repositoryRoot(context Context) string {
	if context.RepositoryRoot != "" {
		return filepath.Clean(context.RepositoryRoot)
	}
	return filepath.Dir(context.Root.Root)
}

func validateProvenance(value any, resourceType string, context Context, where string) (map[string]any, error) {
	p, err := record(value, where)
	if err != nil {
		return nil, err
	}
	keys := []string{"status", "provider_version", "sources", "dependency_sources", "local_sources", "sanitized", "note"}
	if err := rejectUnknown(p, keys, where); err != nil {
		return nil, err
	}
	if err := requireKeys(p, keys, where); err != nil {
		return nil, err
	}
	status, _ := p["status"].(string)
	if status != "source_derived" && status != "sanitized_live" {
		return nil, failf("%s.status must be one of sanitized_live, source_derived", where)
	}
	version, err := nonEmpty(p["provider_version"], where+".provider_version")
	if err != nil {
		return nil, err
	}
	resource, ok := context.Root.Resources[resourceType]
	if !ok {
		return nil, failf("unknown active resource type %s", resourceType)
	}
	manifest, err := metadata.ManifestForProvider(context.Root.Packs, resource.Provider)
	if err != nil {
		return nil, err
	}
	pin, _ := manifest.Data["pin"].(string)
	if pin == "" {
		return nil, failf("%s resource provider %s has no pack pin", where, resource.Provider)
	}
	if pin != version {
		return nil, failf("%s.provider_version %s does not match active %s pack pin %s", where, version, resource.Provider, pin)
	}
	sources, err := stringList(p["sources"], where+".sources")
	if err != nil {
		return nil, err
	}
	for i, source := range sources {
		if !pinnedGithub(source, nil, version) {
			return nil, failf("%s.sources[%d] must use a GitHub blob ref pinned to provider version %s", where, i, version)
		}
	}
	local, err := stringList(p["local_sources"], where+".local_sources")
	if err != nil {
		return nil, err
	}
	root := repositoryRoot(context)
	for i, source := range local {
		if filepath.IsAbs(source) {
			return nil, failf("%s.local_sources[%d] must stay within the repository", where, i)
		}
		candidate := filepath.Join(root, filepath.Clean(source))
		rel, relErr := filepath.Rel(root, candidate)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, failf("%s.local_sources[%d] must stay within the repository", where, i)
		}
		info, statErr := os.Stat(candidate)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, failf("%s.local_sources[%d] does not exist: %s", where, i, source)
		}
	}
	deps, ok := p["dependency_sources"].([]any)
	if !ok {
		return nil, failf("%s.dependency_sources must be a list", where)
	}
	urls := map[string]bool{}
	for i, raw := range deps {
		label := fmt.Sprintf("%s.dependency_sources[%d]", where, i)
		dep, err := record(raw, label)
		if err != nil {
			return nil, err
		}
		if err := rejectUnknown(dep, []string{"name", "version", "url"}, label); err != nil {
			return nil, err
		}
		if err := requireKeys(dep, []string{"name", "version", "url"}, label); err != nil {
			return nil, err
		}
		name, err := nonEmpty(dep["name"], label+".name")
		if err != nil {
			return nil, err
		}
		ver, err := nonEmpty(dep["version"], label+".version")
		if err != nil {
			return nil, err
		}
		url, err := nonEmpty(dep["url"], label+".url")
		if err != nil {
			return nil, err
		}
		if !pinnedGithub(url, &name, ver) {
			return nil, failf("%s.url must reference %s at version %s", label, name, ver)
		}
		if urls[url] {
			return nil, failf("%s.dependency_sources must not contain duplicate URLs", where)
		}
		urls[url] = true
	}
	if p["sanitized"] != true {
		return nil, failf("%s.sanitized must be true; live/private state is not accepted", where)
	}
	if _, err := nonEmpty(p["note"], where+".note"); err != nil {
		return nil, err
	}
	return p, nil
}

// ValidateFixture rejects malformed, unpinned, or unsanitized fixture data.
func ValidateFixture(value any, context Context) (Fixture, error) {
	f, err := record(value, "parity fixture")
	if err != nil {
		return Fixture{}, err
	}
	keys := []string{"fixture_version", "name", "resource_type", "provenance", "raw_items", "provider_state", "expected_differences"}
	if err := rejectUnknown(f, keys, "parity fixture"); err != nil {
		return Fixture{}, err
	}
	if err := requireKeys(f, keys, "parity fixture"); err != nil {
		return Fixture{}, err
	}
	version, isNumber := f["fixture_version"].(json.Number)
	if f["fixture_version"] != float64(1) && (!isNumber || string(version) != "1") {
		return Fixture{}, failf("parity fixture has unsupported fixture_version %v", f["fixture_version"])
	}
	name, err := nonEmpty(f["name"], "parity fixture.name")
	if err != nil {
		return Fixture{}, err
	}
	resourceType, err := nonEmpty(f["resource_type"], "parity fixture.resource_type")
	if err != nil {
		return Fixture{}, err
	}
	if _, ok := context.Root.Resources[resourceType]; !ok {
		return Fixture{}, failf("unknown active resource type %s", resourceType)
	}
	provenance, err := validateProvenance(f["provenance"], resourceType, context, "parity fixture.provenance")
	if err != nil {
		return Fixture{}, err
	}
	raw, ok := f["raw_items"].([]any)
	if !ok || len(raw) == 0 {
		return Fixture{}, failf("parity fixture.raw_items must be a non-empty list")
	}
	for i, item := range raw {
		if _, err := record(item, fmt.Sprintf("parity fixture.raw_items[%d]", i)); err != nil {
			return Fixture{}, err
		}
		if err := validateJSON(item, fmt.Sprintf("parity fixture.raw_items[%d]", i)); err != nil {
			return Fixture{}, err
		}
	}
	stateRaw, err := record(f["provider_state"], "parity fixture.provider_state")
	if err != nil {
		return Fixture{}, err
	}
	if len(stateRaw) == 0 {
		return Fixture{}, failf("parity fixture.provider_state must be a non-empty object")
	}
	state := make(map[string]map[string]any, len(stateRaw))
	for id, item := range stateRaw {
		if _, err := nonEmpty(id, "parity fixture.provider_state key"); err != nil {
			return Fixture{}, err
		}
		label := "parity fixture.provider_state." + id
		entry, err := record(item, label)
		if err != nil {
			return Fixture{}, err
		}
		if err := rejectUnknown(entry, []string{"values", "sensitive_values"}, label); err != nil {
			return Fixture{}, err
		}
		if err := requireKeys(entry, []string{"values"}, label); err != nil {
			return Fixture{}, err
		}
		values, err := record(entry["values"], label+".values")
		if err != nil {
			return Fixture{}, err
		}
		if err := validateJSON(values, label+".values"); err != nil {
			return Fixture{}, err
		}
		if sensitive, has := entry["sensitive_values"]; has {
			if err := validateJSON(sensitive, label+".sensitive_values"); err != nil {
				return Fixture{}, err
			}
		}
		state[id] = entry
	}
	declared := map[string]bool{}
	for _, key := range []string{"sources", "local_sources"} {
		for _, source := range provenance[key].([]any) {
			declared[source.(string)] = true
		}
	}
	for _, item := range provenance["dependency_sources"].([]any) {
		declared[item.(map[string]any)["url"].(string)] = true
	}
	expectsRaw, ok := f["expected_differences"].([]any)
	if !ok {
		return Fixture{}, failf("parity fixture.expected_differences must be a list")
	}
	expects := make([]Expectation, len(expectsRaw))
	seen := map[string]bool{}
	for i, rawExpectation := range expectsRaw {
		label := fmt.Sprintf("parity fixture.expected_differences[%d]", i)
		entry, err := record(rawExpectation, label)
		if err != nil {
			return Fixture{}, err
		}
		expectKeys := []string{"path", "transform", "adopt", "classification", "disposition", "reason", "evidence"}
		if err := rejectUnknown(entry, expectKeys, label); err != nil {
			return Fixture{}, err
		}
		if err := requireKeys(entry, expectKeys, label); err != nil {
			return Fixture{}, err
		}
		path, ok := entry["path"].(string)
		if !ok || path != "" && !strings.HasPrefix(path, "/") {
			return Fixture{}, failf("%s.path must be an RFC 6901 JSON pointer", label)
		}
		if seen[path] {
			return Fixture{}, failf("parity fixture.expected_differences contains duplicate path %s", path)
		}
		seen[path] = true
		transformSide, err := validateSide(entry["transform"], label+".transform")
		if err != nil {
			return Fixture{}, err
		}
		adoptSide, err := validateSide(entry["adopt"], label+".adopt")
		if err != nil {
			return Fixture{}, err
		}
		classification, _ := entry["classification"].(string)
		if !map[string]bool{"semantic_mismatch": true, "validation_asymmetry": true, "representational_difference": true, "provider_normalization": true, "other": true}[classification] {
			return Fixture{}, failf("%s.classification must be one of other, provider_normalization, representational_difference, semantic_mismatch, validation_asymmetry", label)
		}
		disposition, _ := entry["disposition"].(string)
		if disposition != "accepted" && disposition != "evidence_gate" {
			return Fixture{}, failf("%s.disposition must be one of accepted, evidence_gate", label)
		}
		reason, err := nonEmpty(entry["reason"], label+".reason")
		if err != nil {
			return Fixture{}, err
		}
		evidence, err := stringList(entry["evidence"], label+".evidence")
		if err != nil {
			return Fixture{}, err
		}
		for j, source := range evidence {
			if !declared[source] {
				return Fixture{}, failf("%s.evidence[%d] is not declared by fixture provenance", label, j)
			}
		}
		expects[i] = Expectation{path, transformSide, adoptSide, classification, disposition, reason, evidence}
	}
	return Fixture{FixtureVersion: 1, Name: name, ResourceType: resourceType, Provenance: provenance, RawItems: raw, ProviderState: state, ExpectedDifferences: expects}, nil
}

// LoadFixture parses and validates one lossless fixture JSON file.
func LoadFixture(source string, context Context) (Fixture, error) {
	bytes, err := os.ReadFile(source)
	if err != nil {
		return Fixture{}, failf("%s is not valid JSON: %v", source, err)
	}
	value, err := canonjson.ParseDataJSONLosslessly(string(bytes))
	if err != nil {
		return Fixture{}, failf("%s is not valid JSON: %v", source, err)
	}
	return ValidateFixture(value, context)
}

func clone(value any) any {
	switch value := value.(type) {
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = clone(value[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, v := range value {
			out[k] = clone(v)
		}
		return out
	default:
		return value
	}
}
func numberKind(value any) string {
	switch value := value.(type) {
	case json.Number:
		if integerToken.MatchString(string(value)) {
			return "integer"
		}
		return "float"
	case float64:
		if math.Trunc(value) == value && !(value == 0 && math.Signbit(value)) {
			return "integer"
		}
		return "float"
	default:
		return ""
	}
}
func strictRender(value any) (string, error) {
	rendered, err := canonjson.RenderLosslessArtifactJSON(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(rendered, "\n"), nil
}
func strictEqual(left, right any) (bool, error) {
	leftKind, rightKind := numberKind(left), numberKind(right)
	if leftKind != "" || rightKind != "" {
		leftRendered, err := strictRender(left)
		if err != nil {
			return false, err
		}
		rightRendered, err := strictRender(right)
		if err != nil {
			return false, err
		}
		return leftKind == rightKind && leftRendered == rightRendered, nil
	}
	switch l := left.(type) {
	case nil:
		return right == nil, nil
	case bool:
		r, ok := right.(bool)
		return ok && l == r, nil
	case string:
		r, ok := right.(string)
		return ok && l == r, nil
	case []any:
		r, ok := right.([]any)
		if !ok || len(l) != len(r) {
			return false, nil
		}
		for i := range l {
			equal, err := strictEqual(l[i], r[i])
			if err != nil {
				return false, err
			}
			if !equal {
				return false, nil
			}
		}
		return true, nil
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok || len(l) != len(r) {
			return false, nil
		}
		for k, v := range l {
			rv, has := r[k]
			if !has {
				return false, nil
			}
			equal, err := strictEqual(v, rv)
			if err != nil {
				return false, err
			}
			if !equal {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, nil
	}
}
func side(present bool, value any) Side { return Side{Present: present, Value: value} }
func pointer(segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	encoded := make([]string, len(segments))
	for i, s := range segments {
		encoded[i] = strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
	}
	return "/" + strings.Join(encoded, "/")
}

// Differences returns strict structural differences, preserving missingness.
func Differences(transformValue, adoptValue any) ([]Difference, error) {
	if _, err := strictRender(transformValue); err != nil {
		return nil, err
	}
	if _, err := strictRender(adoptValue); err != nil {
		return nil, err
	}
	return differences(transformValue, adoptValue, nil)
}
func differences(left, right any, segments []string) ([]Difference, error) {
	lk, rk := numberKind(left), numberKind(right)
	if lk != "" || rk != "" {
		if lk != rk {
			return []Difference{{pointer(segments), side(true, left), side(true, right)}}, nil
		}
	} else {
		_, lo := left.(map[string]any)
		_, ro := right.(map[string]any)
		_, la := left.([]any)
		_, ra := right.([]any)
		if lo != ro || la != ra || (left == nil) != (right == nil) || fmt.Sprintf("%T", left) != fmt.Sprintf("%T", right) {
			return []Difference{{pointer(segments), side(true, left), side(true, right)}}, nil
		}
	}
	if l, ok := left.(map[string]any); ok {
		r := right.(map[string]any)
		keys := map[string]bool{}
		for k := range l {
			keys[k] = true
		}
		for k := range r {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		out := []Difference{}
		for _, key := range canonjson.SortedStrings(names) {
			lv, lok := l[key]
			rv, rok := r[key]
			if !lok {
				out = append(out, Difference{pointer(append(append([]string{}, segments...), key)), side(false, nil), side(true, rv)})
			} else if !rok {
				out = append(out, Difference{pointer(append(append([]string{}, segments...), key)), side(true, lv), side(false, nil)})
			} else {
				children, err := differences(lv, rv, append(append([]string{}, segments...), key))
				if err != nil {
					return nil, err
				}
				out = append(out, children...)
			}
		}
		return out, nil
	}
	if l, ok := left.([]any); ok {
		r := right.([]any)
		out := []Difference{}
		max := len(l)
		if len(r) > max {
			max = len(r)
		}
		for i := 0; i < max; i++ {
			s := strconv.Itoa(i)
			if i >= len(l) {
				out = append(out, Difference{pointer(append(append([]string{}, segments...), s)), side(false, nil), side(true, r[i])})
			} else if i >= len(r) {
				out = append(out, Difference{pointer(append(append([]string{}, segments...), s)), side(true, l[i]), side(false, nil)})
			} else {
				children, err := differences(l[i], r[i], append(append([]string{}, segments...), s))
				if err != nil {
					return nil, err
				}
				out = append(out, children...)
			}
		}
		return out, nil
	}
	equal, err := strictEqual(left, right)
	if err != nil {
		return nil, err
	}
	if equal {
		return nil, nil
	}
	return []Difference{{pointer(segments), side(true, left), side(true, right)}}, nil
}

func tokens(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, failf("difference path is not a JSON pointer")
	}
	raw := strings.Split(path[1:], "/")
	for i := range raw {
		raw[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(raw[i])
	}
	return raw, nil
}
func index(token string, length int, appendOK bool) (int, error) {
	if !listIndexToken.MatchString(token) {
		return 0, failf("difference path list index %s is invalid", token)
	}
	i, err := strconv.Atoi(token)
	if err != nil || i < 0 || i > length || (!appendOK && i == length) {
		return 0, failf("difference path list index %s is out of range", token)
	}
	return i, nil
}
func setAt(current any, path []string, value any) (any, error) {
	if len(path) == 0 {
		return clone(value), nil
	}
	token := path[0]
	switch parent := current.(type) {
	case map[string]any:
		if len(path) == 1 {
			parent[token] = clone(value)
			return parent, nil
		}
		child, ok := parent[token]
		if !ok {
			return nil, failf("difference path parent %s is missing", token)
		}
		next, err := setAt(child, path[1:], value)
		if err != nil {
			return nil, err
		}
		parent[token] = next
		return parent, nil
	case []any:
		i, err := index(token, len(parent), len(path) == 1)
		if err != nil {
			return nil, err
		}
		if len(path) == 1 {
			if i == len(parent) {
				return append(parent, clone(value)), nil
			}
			parent[i] = clone(value)
			return parent, nil
		}
		child, err := setAt(parent[i], path[1:], value)
		if err != nil {
			return nil, err
		}
		parent[i] = child
		return parent, nil
	default:
		return nil, failf("difference path traverses a scalar at %s", token)
	}
}
func deleteAt(current any, path []string) (any, error) {
	if len(path) == 0 {
		return nil, failf("difference cannot delete the report root")
	}
	token := path[0]
	switch parent := current.(type) {
	case map[string]any:
		if len(path) == 1 {
			if _, ok := parent[token]; !ok {
				return nil, failf("difference delete path /%s is missing", token)
			}
			delete(parent, token)
			return parent, nil
		}
		child, ok := parent[token]
		if !ok {
			return nil, failf("difference path parent %s is missing", token)
		}
		next, err := deleteAt(child, path[1:])
		if err != nil {
			return nil, err
		}
		parent[token] = next
		return parent, nil
	case []any:
		i, err := index(token, len(parent), false)
		if err != nil {
			return nil, err
		}
		if len(path) == 1 {
			return append(parent[:i], parent[i+1:]...), nil
		}
		next, err := deleteAt(parent[i], path[1:])
		if err != nil {
			return nil, err
		}
		parent[i] = next
		return parent, nil
	default:
		return nil, failf("difference path traverses a scalar at %s", token)
	}
}
func set(root any, path string, value any) (any, error) {
	ts, err := tokens(path)
	if err != nil {
		return nil, err
	}
	return setAt(root, ts, value)
}
func deleteAtPath(root any, path string) (any, error) {
	ts, err := tokens(path)
	if err != nil {
		return nil, err
	}
	return deleteAt(root, ts)
}

// Replay applies adopt-present values forward and adopt-absent values in reverse.
func Replay(transformPayload any, entries []Difference) (any, error) {
	result := clone(transformPayload)
	var err error
	for _, entry := range entries {
		if entry.Adopt.Present {
			result, err = set(result, entry.Path, entry.Adopt.Value)
			if err != nil {
				return nil, err
			}
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].Adopt.Present {
			result, err = deleteAtPath(result, entries[i].Path)
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func differenceKey(d Difference) (string, error) {
	return strictRender(map[string]any{"path": d.Path, "transform": sideMap(d.Transform), "adopt": sideMap(d.Adopt)})
}
func itemsValue(items map[string]map[string]any) map[string]any {
	output := make(map[string]any, len(items))
	for key, value := range items {
		output[key] = value
	}
	return output
}

func renderedItems(items map[string]map[string]any) (string, string, error) {
	return renderedItemValue(itemsValue(items))
}

func renderedItemValue(items map[string]any) (string, string, error) {
	rendered, err := canonjson.RenderLosslessArtifactJSON(map[string]any{"items": items})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(rendered))
	return rendered, hex.EncodeToString(sum[:]), nil
}

type fixtureStateTracker struct {
	fixture   Fixture
	requested map[string]bool
}

func newFixtureStateTracker(fixture Fixture) *fixtureStateTracker {
	return &fixtureStateTracker{fixture: fixture, requested: map[string]bool{}}
}

func (tracker *fixtureStateTracker) checkCoverage() error {
	available := map[string]bool{}
	for id := range tracker.fixture.ProviderState {
		available[id] = true
	}
	missing, extra := []string{}, []string{}
	for id := range tracker.requested {
		if !available[id] {
			missing = append(missing, id)
		}
	}
	for id := range available {
		if !tracker.requested[id] {
			extra = append(extra, id)
		}
	}
	missing = canonjson.SortedStrings(missing)
	extra = canonjson.SortedStrings(extra)
	if len(missing) > 0 {
		return failf("provider_state is missing import id %s", missing[0])
	}
	if len(extra) > 0 {
		return failf("provider_state has unreferenced import id %s", extra[0])
	}
	return nil
}

func (tracker *fixtureStateTracker) loader(request adopt.AdoptionStateRequest) (map[string]adopt.OracleStateObject, error) {
	for _, id := range request.KeyToImportID {
		tracker.requested[id] = true
	}
	if err := tracker.checkCoverage(); err != nil {
		return nil, err
	}
	out := map[string]adopt.OracleStateObject{}
	for key, id := range request.KeyToImportID {
		state := tracker.fixture.ProviderState[id]
		values, _ := state["values"].(map[string]any)
		sensitive := state["sensitive_values"]
		if sensitive == nil {
			sensitive = map[string]any{}
		}
		out[key] = adopt.OracleStateObject{Address: "fixture", Values: values, SensitiveValues: sensitive}
	}
	return out, nil
}

// Compare executes the actual Transform and Adopt kernels for one fixture.
func Compare(input Fixture, context Context) (map[string]any, error) {
	return compare(input, context, Differences)
}

// compare has a package-private comparator seam solely for completeness-guard
// tests. Production callers always use Compare and cannot replace it.
func compare(input Fixture, context Context, comparator func(any, any) ([]Difference, error)) (map[string]any, error) {
	fixture, err := ValidateFixture(fixtureToValue(input), context)
	if err != nil {
		return nil, err
	}
	resource := context.Root.Resources[fixture.ResourceType]
	schema, err := context.Root.LoadResourceSchema(fixture.ResourceType)
	if err != nil {
		return nil, err
	}
	transformed, err := transform.TransformLoadedItems(transform.TransformLoadedItemsOptions{Resource: resource, Schema: schema, RawItems: fixture.RawItems, HTMLUnescape: textcompat.HTMLUnescape, UnescapeHTML: transformrun.ShouldUnescapeForTransform(context.Root, fixture.ResourceType)})
	if err != nil {
		return nil, err
	}
	policy, err := adopt.LoadAdoptionPolicy(context.Root, nil)
	if err != nil {
		return nil, err
	}
	states := newFixtureStateTracker(fixture)
	adopted, err := adopt.AdoptResourceItems(policy, fixture.RawItems, resource, context.Root, states.loader, nil)
	if err != nil {
		return nil, err
	}
	if err := states.checkCoverage(); err != nil {
		return nil, err
	}
	tr, th, err := renderedItems(transformed.Items)
	if err != nil {
		return nil, err
	}
	ar, ah, err := renderedItems(adopted.Items)
	if err != nil {
		return nil, err
	}
	transformPayload := map[string]any{"items": itemsValue(transformed.Items)}
	adoptPayload := map[string]any{"items": itemsValue(adopted.Items)}
	actual, err := comparator(transformPayload, adoptPayload)
	if err != nil {
		return nil, err
	}
	replayed, err := Replay(transformPayload, actual)
	if err != nil {
		return nil, err
	}
	replayRecord, ok := replayed.(map[string]any)
	if !ok {
		return nil, failf("reconstructed parity payload must be an object")
	}
	replayItems, ok := replayRecord["items"].(map[string]any)
	if !ok {
		return nil, failf("reconstructed parity payload.items must be an object")
	}
	rr, _, err := renderedItemValue(replayItems)
	if err != nil {
		return nil, err
	}
	unaccounted := rr != ar
	expected := map[string]Expectation{}
	for _, entry := range fixture.ExpectedDifferences {
		key, err := differenceKey(Difference{entry.Path, entry.Transform, entry.Adopt})
		if err != nil {
			return nil, err
		}
		expected[key] = entry
	}
	differences := make([]any, len(actual))
	unclassified, evidenceGates, accepted := 0, 0, 0
	for i, entry := range actual {
		key, err := differenceKey(entry)
		if err != nil {
			return nil, err
		}
		expect, found := expected[key]
		if !found {
			differences[i] = map[string]any{"path": entry.Path, "transform": sideMap(entry.Transform), "adopt": sideMap(entry.Adopt), "status": "unclassified"}
			unclassified++
			continue
		}
		delete(expected, key)
		differences[i] = map[string]any{"path": entry.Path, "transform": sideMap(entry.Transform), "adopt": sideMap(entry.Adopt), "status": "classified", "classification": expect.Classification, "disposition": expect.Disposition, "reason": expect.Reason, "evidence": stringValues(expect.Evidence)}
		if expect.Disposition == "evidence_gate" {
			evidenceGates++
		} else {
			accepted++
		}
	}
	stale := make([]any, 0, len(expected))
	staleEntries := make([]Expectation, 0, len(expected))
	for _, entry := range expected {
		staleEntries = append(staleEntries, entry)
	}
	sort.SliceStable(staleEntries, func(i, j int) bool {
		return canonjson.ComparePythonStrings(staleEntries[i].Path, staleEntries[j].Path) < 0
	})
	for _, entry := range staleEntries {
		stale = append(stale, expectationMap(entry))
	}
	drops := make([]any, len(transformed.Drops))
	for i, d := range canonjson.SortedStrings(transformed.Drops) {
		drops[i] = d
	}
	result := "equal"
	if unclassified > 0 || len(stale) > 0 || len(drops) > 0 || unaccounted {
		result = "review_required"
	} else if evidenceGates > 0 {
		result = "evidence_gates"
	} else if len(actual) > 0 {
		result = "classified_differences"
	}
	provenance, ok := clone(fixture.Provenance).(map[string]any)
	if !ok {
		return nil, failf("fixture provenance must be an object")
	}
	return map[string]any{"name": fixture.Name, "resource_type": fixture.ResourceType, "provenance": provenance, "result": result, "outputs": map[string]any{"byte_equal": tr == ar, "unaccounted_byte_difference": unaccounted, "transform_sha256": th, "adopt_sha256": ah}, "differences": differences, "stale_expectations": stale, "transform_unacknowledged_drops": drops, "summary": map[string]any{"differences": float64(len(actual)), "classified": float64(len(actual) - unclassified), "unclassified": float64(unclassified), "evidence_gates": float64(evidenceGates), "accepted": float64(accepted), "stale_expectations": float64(len(stale)), "unacknowledged_drops": float64(len(drops)), "unaccounted_byte_differences": boolNumber(unaccounted)}}, nil
}
func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
func sideMap(s Side) map[string]any {
	if s.Present {
		return map[string]any{"present": true, "value": s.Value}
	}
	return map[string]any{"present": false}
}
func expectationMap(e Expectation) map[string]any {
	return map[string]any{"path": e.Path, "transform": sideMap(e.Transform), "adopt": sideMap(e.Adopt), "classification": e.Classification, "disposition": e.Disposition, "reason": e.Reason, "evidence": stringValues(e.Evidence)}
}

func stringValues(values []string) []any {
	output := make([]any, len(values))
	for i, value := range values {
		output[i] = value
	}
	return output
}
func fixtureToValue(f Fixture) map[string]any {
	states := map[string]any{}
	for k, v := range f.ProviderState {
		states[k] = v
	}
	expects := make([]any, len(f.ExpectedDifferences))
	for i, e := range f.ExpectedDifferences {
		expects[i] = expectationMap(e)
	}
	return map[string]any{"fixture_version": float64(f.FixtureVersion), "name": f.Name, "resource_type": f.ResourceType, "provenance": f.Provenance, "raw_items": f.RawItems, "provider_state": states, "expected_differences": expects}
}

// Build compares fixtures sequentially and returns the deterministic report.
func Build(fixtures []Fixture, context Context) (map[string]any, error) {
	names := map[string]bool{}
	results := make([]map[string]any, 0, len(fixtures))
	for _, fixture := range fixtures {
		validated, err := ValidateFixture(fixtureToValue(fixture), context)
		if err != nil {
			return nil, err
		}
		if names[validated.Name] {
			return nil, failf("duplicate fixture name %s", validated.Name)
		}
		names[validated.Name] = true
		result, err := Compare(validated, context)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return canonjson.ComparePythonStrings(results[i]["name"].(string), results[j]["name"].(string)) < 0
	})
	count := func(kind string) float64 {
		n := 0
		for _, r := range results {
			if r["result"] == kind {
				n++
			}
		}
		return float64(n)
	}
	sum := func(key string) float64 {
		total := float64(0)
		for _, r := range results {
			total += r["summary"].(map[string]any)[key].(float64)
		}
		return total
	}
	summary := map[string]any{"fixtures": float64(len(results)), "equal": count("equal"), "classified_differences": count("classified_differences"), "evidence_gate_fixtures": count("evidence_gates"), "review_required": count("review_required"), "differences": sum("differences"), "classified": sum("classified"), "unclassified": sum("unclassified"), "evidence_gates": sum("evidence_gates"), "accepted": sum("accepted"), "stale_expectations": sum("stale_expectations"), "unacknowledged_drops": sum("unacknowledged_drops"), "unaccounted_byte_differences": sum("unaccounted_byte_differences")}
	result := "equal"
	if summary["review_required"].(float64) > 0 {
		result = "review_required"
	} else if summary["evidence_gate_fixtures"].(float64) > 0 {
		result = "evidence_gates"
	} else if summary["classified_differences"].(float64) > 0 {
		result = "classified_differences"
	}
	out := make([]any, len(results))
	for i, r := range results {
		out[i] = r
	}
	return map[string]any{"kind": ReportKind, "report_version": float64(ReportVersion), "result": result, "summary": summary, "fixtures": out}, nil
}

// Render renders the report using the frozen artifact JSON dialect.
func Render(report map[string]any) (string, error) {
	return canonjson.RenderLosslessArtifactJSON(report)
}

// ResultClassification reads the outcome of a report produced by Compare or
// Build without assigning CLI exit semantics. It is not a general report
// validator and must not be used to accept arbitrary externally supplied maps.
func ResultClassification(report map[string]any) (Result, error) {
	value, ok := report["result"].(string)
	if !ok {
		return "", failf("parity report result is missing")
	}
	result := Result(value)
	switch result {
	case ResultEqual, ResultClassifiedDifferences, ResultEvidenceGates, ResultReviewRequired:
		return result, nil
	default:
		return "", failf("parity report result %s is invalid", value)
	}
}

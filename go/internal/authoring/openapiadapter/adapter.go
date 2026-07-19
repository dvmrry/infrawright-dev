package openapiadapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/yaml"
	yaml3 "github.com/oasdiff/yaml3"
)

const (
	virtualScheme = "infrawright-openapi"
	maxRefDepth   = 64
)

var operationMethods = map[string]struct{}{
	"get": {}, "put": {}, "post": {}, "delete": {}, "options": {}, "head": {}, "patch": {}, "trace": {},
}

var supportedOpenAPI = regexp.MustCompile(`^3\.(0|1)\.[0-9]+$`)
var strictJSONNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Analyze validates source evidence, then builds a sealed optional-adapter
// result. Operational document failures are recorded in diagnostics rather
// than returned as errors; only invalid source, cancellation, and violated
// package invariants are errors.
func Analyze(ctx context.Context, status sourcebind.OpenAPIStatus, source contracts.SourceEvidenceReport) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("openapi analysis cancelled: %w", err)
	}
	if err := contracts.ValidateSourceEvidenceReport(source); err != nil {
		return Result{}, fmt.Errorf("validate source evidence: %w", err)
	}
	if !status.Available {
		if status.Err != nil {
			return seal(source, contracts.OpenAPIUnavailable, reason(contracts.OpenAPIReasonUnreadable), nil, nil)
		}
		return seal(source, contracts.OpenAPIAbsent, nil, nil, nil)
	}
	if status.Err != nil {
		return seal(source, contracts.OpenAPIUnavailable, reason(contracts.OpenAPIReasonUnreadable), nil, nil)
	}
	files, root, err := captureFiles(status.Files)
	if err != nil {
		return seal(source, contracts.OpenAPIUnavailable, reason(contracts.OpenAPIReasonLocalRefUnresolved), nil, nil)
	}
	rootGraph, err := parseDocument(files[root])
	if err != nil {
		return seal(source, contracts.OpenAPIUnavailable, reason(contracts.OpenAPIReasonMalformed), nil, nil)
	}
	if !validRoot(rootGraph) {
		return seal(source, contracts.OpenAPIUnavailable, reason(contracts.OpenAPIReasonInvalidRoot), nil, nil)
	}
	document := Document{root: root, raw: cloneBytes(files), files: map[string]any{root: cloneValue(rootGraph)}}
	operations, closureErr := inventoryRequired(ctx, document, source)
	if closureErr != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("openapi analysis cancelled: %w", ctx.Err())
		}
		return seal(source, contracts.OpenAPIUnavailable, reason(contracts.OpenAPIReasonLocalRefUnresolved), nil, nil)
	}
	document.operations = operations
	if err := validateClosed(ctx, files, root); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("openapi analysis cancelled: %w", ctx.Err())
		}
		// The raw closure is intentionally narrower than OpenAPI validation. It
		// establishes that every source comparison input is independently safe.
		return seal(source, contracts.OpenAPIDegraded, reason(contracts.OpenAPIReasonDegradedOperation), nil, operations)
	}
	all, err := inventoryAll(ctx, document)
	if err != nil {
		return Result{}, fmt.Errorf("build strict operation inventory: %w", err)
	}
	document.operations = all
	return seal(source, contracts.OpenAPIUsable, nil, &document, all)
}

func seal(source contracts.SourceEvidenceReport, state contracts.OpenAPIDocumentState, why *contracts.OpenAPIReasonCode, document *Document, operations []Operation) (Result, error) {
	comparisons := comparisonRows(source, state, operations)
	renderedSource, err := contracts.RenderSourceEvidenceReport(source)
	if err != nil {
		return Result{}, fmt.Errorf("render source evidence: %w", err)
	}
	report := contracts.OpenAPIDiagnosticsReport{
		Kind:                 "infrawright.openapi_diagnostics",
		SchemaVersion:        1,
		SourceTrust:          source.SourceTrust,
		SourceManifestSHA256: cloneString(source.SourceManifestSHA256),
		SourceReportSHA256:   sha256Hex([]byte(renderedSource)),
		DocumentState:        state,
		ReasonCode:           why,
		Comparisons:          comparisons,
		Summary:              summary(source, state, comparisons),
	}
	rendered, err := contracts.RenderOpenAPIDiagnosticsReport(report, source)
	if err != nil {
		return Result{}, fmt.Errorf("render openapi diagnostics invariant: %w", err)
	}
	canonical := []byte(rendered)
	if _, err := contracts.DecodeOpenAPIDiagnosticsReport(canonical, source); err != nil {
		return Result{}, fmt.Errorf("decode sealed openapi diagnostics invariant: %w", err)
	}
	var detached *Document
	if document != nil {
		copy := cloneDocument(*document)
		detached = &copy
	}
	return Result{canonical: append([]byte(nil), canonical...), document: detached}, nil
}

func captureFiles(input []sourcebind.CapturedFile) (map[string][]byte, string, error) {
	if len(input) == 0 {
		return nil, "", fmt.Errorf("captured openapi has no root")
	}
	files := make(map[string][]byte, len(input))
	for i, file := range input {
		key, err := canonicalPath(file.Path)
		if err != nil || key != file.Path || file.SHA256 != sha256Hex(file.Bytes) {
			return nil, "", fmt.Errorf("invalid captured openapi file")
		}
		if _, exists := files[key]; exists {
			return nil, "", fmt.Errorf("duplicate captured openapi path")
		}
		files[key] = append([]byte(nil), file.Bytes...)
		if i == 0 {
			if key == "" {
				return nil, "", fmt.Errorf("empty root path")
			}
		}
	}
	return files, input[0].Path, nil
}

func parseDocument(data []byte) (any, error) {
	value, err := canonjson.ParseDataJSONLosslessly(string(data))
	if err == nil {
		return value, nil
	}
	var yamlValue any
	if _, yamlErr := yaml.Unmarshal(data, &yamlValue, yaml.DecodeOpts{DisableTimestamps: true}); yamlErr != nil {
		return nil, fmt.Errorf("parse JSON: %w; parse YAML: %v", err, yamlErr)
	}
	converted, yamlErr := json.Marshal(yamlValue)
	if yamlErr != nil {
		return nil, fmt.Errorf("convert YAML: %w", yamlErr)
	}
	value, yamlErr = canonjson.ParseDataJSONLosslessly(string(converted))
	if yamlErr != nil {
		return nil, yamlErr
	}
	decoder := yaml3.NewDecoder(bytes.NewReader(data))
	decoder.DisableTimestamps(true)
	var node yaml3.Node
	if yamlErr := decoder.Decode(&node); yamlErr != nil {
		return nil, fmt.Errorf("parse YAML nodes: %w", yamlErr)
	}
	return overlayYAMLNumberLexemes(value, &node), nil
}

func validRoot(value any) bool {
	root, ok := value.(map[string]any)
	if !ok || !supportedOpenAPI.MatchString(stringValue(root["openapi"])) {
		return false
	}
	info, ok := root["info"].(map[string]any)
	if !ok || strings.TrimSpace(stringValue(info["title"])) == "" || strings.TrimSpace(stringValue(info["version"])) == "" {
		return false
	}
	_, ok = root["paths"].(map[string]any)
	return ok
}

func validateClosed(ctx context.Context, files map[string][]byte, root string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("validate closed OpenAPI cancelled: %w", err)
	}
	authority, err := virtualAuthority()
	if err != nil {
		return fmt.Errorf("generate closed OpenAPI authority: %w", err)
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = false
	loader.JoinFunc = func(base, relative *url.URL) *url.URL {
		if !safeLoaderRelative(relative) {
			return &url.URL{Scheme: "rejected-openapi-ref"}
		}
		return base.ResolveReference(relative)
	}
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if location == nil || location.Scheme != virtualScheme || location.Host != authority || location.User != nil || location.Opaque != "" || location.RawPath != "" || location.ForceQuery || location.RawQuery != "" || location.Fragment != "" {
			return nil, fmt.Errorf("closed OpenAPI reader rejected URI")
		}
		key, err := canonicalPath(strings.TrimPrefix(location.Path, "/"))
		if err != nil {
			return nil, fmt.Errorf("closed OpenAPI reader rejected path")
		}
		bytes, ok := files[key]
		if !ok {
			return nil, fmt.Errorf("closed OpenAPI reader rejected unlisted path")
		}
		graph, err := parseDocument(bytes)
		if err != nil {
			return nil, fmt.Errorf("closed OpenAPI reader rejected malformed content")
		}
		converted, err := json.Marshal(finiteValidationClone(graph))
		if err != nil {
			return nil, fmt.Errorf("closed OpenAPI reader could not sanitize content")
		}
		return append([]byte(nil), converted...), nil
	}
	location := &url.URL{Scheme: virtualScheme, Host: authority, Path: "/" + root}
	doc, err := loader.LoadFromURI(location)
	if err != nil {
		return fmt.Errorf("closed OpenAPI load: %w", err)
	}
	return doc.Validate(ctx)
}

func virtualAuthority() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func overlayYAMLNumberLexemes(value any, node *yaml3.Node) any {
	if node == nil {
		return value
	}
	if node.Kind == yaml3.DocumentNode && len(node.Content) != 0 {
		return overlayYAMLNumberLexemes(value, node.Content[0])
	}
	if node.Kind == yaml3.AliasNode {
		return overlayYAMLNumberLexemes(value, node.Alias)
	}
	if node.Kind == yaml3.ScalarNode && node.Style&(yaml3.TaggedStyle|yaml3.SingleQuotedStyle|yaml3.DoubleQuotedStyle|yaml3.LiteralStyle|yaml3.FoldedStyle) == 0 && strictJSONNumber.MatchString(node.Value) {
		return json.Number(node.Value)
	}
	switch typed := value.(type) {
	case map[string]any:
		if node.Kind != yaml3.MappingNode {
			return value
		}
		out := cloneValue(typed).(map[string]any)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index].Value
			if item, ok := typed[key]; ok {
				out[key] = overlayYAMLNumberLexemes(item, node.Content[index+1])
			}
		}
		return out
	case []any:
		if node.Kind != yaml3.SequenceNode {
			return value
		}
		out := append([]any(nil), typed...)
		for index := range out {
			if index < len(node.Content) {
				out[index] = overlayYAMLNumberLexemes(typed[index], node.Content[index])
			}
		}
		return out
	}
	return value
}

func canonicalPath(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.Contains(value, "%") || strings.HasPrefix(value, "/") || strings.Contains(value, "?") || strings.Contains(value, "#") {
		return "", fmt.Errorf("unsafe local path")
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe local path segment")
		}
	}
	if path.Clean(value) != value {
		return "", fmt.Errorf("noncanonical local path")
	}
	return value, nil
}

func safeLoaderRelative(relative *url.URL) bool {
	if relative == nil || relative.Scheme != "" || relative.Host != "" || relative.User != nil || relative.Opaque != "" || relative.ForceQuery || relative.RawQuery != "" {
		return false
	}
	if relative.RawFragment != "" || strings.Contains(relative.Fragment, "%") {
		return false
	}
	if relative.Path == "" {
		return relative.Fragment == "" || strings.HasPrefix(relative.Fragment, "/")
	}
	if relative.RawPath != "" {
		if relative.RawPath != relative.Path || strings.Contains(relative.RawPath, "%") {
			return false
		}
	} else if relative.EscapedPath() != relative.Path {
		// A canonical encoded alias such as %20 is decoded into Path with an
		// empty RawPath. A literal space retains RawPath == Path and is allowed.
		return false
	}
	_, err := canonicalPath(relative.Path)
	return err == nil
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func reason(value contracts.OpenAPIReasonCode) *contracts.OpenAPIReasonCode { return &value }

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func stringValue(value any) string { result, _ := value.(string); return result }

func finiteValidationClone(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if n, err := typed.Float64(); err != nil || math.IsInf(n, 0) {
			if strings.HasPrefix(string(typed), "-") {
				return -math.MaxFloat64
			}
			return math.MaxFloat64
		}
		return typed
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = finiteValidationClone(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = finiteValidationClone(item)
		}
		return out
	default:
		return typed
	}
}

func cloneGraphs(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = cloneValue(item)
	}
	return out
}
func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = cloneValue(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = cloneValue(v)
		}
		return out
	default:
		return typed
	}
}
func cloneDocument(document Document) Document {
	return Document{root: document.root, raw: cloneBytes(document.raw), files: cloneGraphs(document.files), operations: document.Operations(), metadataOnly: document.metadataOnly}
}

func cloneBytes(value map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(value))
	for key, bytes := range value {
		out[key] = append([]byte(nil), bytes...)
	}
	return out
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

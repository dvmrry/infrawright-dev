package openapimap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func TestHelperAndBoundaryVectors(t *testing.T) {
	t.Parallel()
	if got := RoundPythonRatio4(1, 32); got != 0.0312 {
		t.Fatalf("1/32 = %v", got)
	}
	if got := RoundPythonRatio4(1, 160); got != 0.0063 {
		t.Fatalf("1/160 = %v", got)
	}
	if got := RoundPythonRatio4(0, 0); got != 0 {
		t.Fatalf("0/0 = %v", got)
	}
	if got := RoundPythonRatio4(-3, 32); got != -0.0938 {
		t.Fatalf("-3/32 = %v", got)
	}
	if got := RoundPythonRatio4(-1, 32); got != -0.0312 {
		t.Fatalf("-1/32 = %v", got)
	}
	if got := plural("address"); got != "addresses" {
		t.Fatalf("plural address = %s", got)
	}
	if got := canonicalParts("/a/{id}/b/"); len(got) != 3 || got[1] != "{}" {
		t.Fatalf("canonical parts = %#v", got)
	}
	variants := fetchVariants("/api/v1/zcc/devices/{id}", "zcc", "/api/v1/")
	if got := len(variants); got != 3 {
		t.Fatalf("variants = %#v", variants)
	}
	if _, err := providerFromSchema(Object{"provider_schemas": Object{"a/x": Object{}, "b/x": Object{}}}, nil); err == nil {
		t.Fatal("ambiguous provider accepted")
	}
	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{{Template: "/things/{id}"}, {Template: "/things/{id}/"}, {Template: "/things//{id}"}, {Template: "/things/{id}//"}, {Template: "/things/{id}/extra"}}}
	if got := detailPaths(view, "/things"); len(got) != 2 || got[0] != "/things/{id}" || got[1] != "/things/{id}/" {
		t.Fatalf("detail paths = %#v", got)
	}
}

func TestOptionsAPIPrefixAndRegistryOptionalRenderedVectors(t *testing.T) {
	t.Parallel()
	document, err := documentFor(t, Object{"openapi": "3.0.3", "paths": Object{"/things": Object{"get": Object{}}}})
	if err != nil {
		t.Fatal(err)
	}
	schema := Object{"resource_schemas": Object{"example_thing": Object{"block": Object{"attributes": Object{"name": Object{"required": true, "type": "string"}}}}}}
	omitted, err := Build(context.Background(), Options{SchemaData: schema, Document: document, ResourcePrefix: "example"})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	explicit, err := Build(context.Background(), Options{SchemaData: schema, Document: document, ResourcePrefix: "example", APIPrefix: &empty})
	if err != nil {
		t.Fatal(err)
	}
	omittedBytes, err := omitted.Render()
	if err != nil {
		t.Fatal(err)
	}
	explicitBytes, err := explicit.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(omittedBytes), `"api_prefix": "/api/"`) || !strings.Contains(string(explicitBytes), `"api_prefix": ""`) {
		t.Fatalf("prefix render omitted=%s explicit=%s", omittedBytes, explicitBytes)
	}

	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{{Template: "/things", Methods: []string{"get"}}}}
	registry := Object{
		"absent_product":         Object{"fetch": Object{"path": "/things"}},
		"false_product":          Object{"product": false, "fetch": Object{"path": "/things"}},
		"nested_pagination":      Object{"product": "", "pagination": "top", "fetch": Object{"path": "/things", "pagination": "nested"}},
		"top_level_pagination":   Object{"product": "", "pagination": "top", "fetch": Object{"path": "/things"}},
		"falsy_pagination":       Object{"product": "", "fetch": Object{"path": "/things", "pagination": false}},
		"absent_reason":          Object{"product": "", "status": "graphql_source", "read": Object{}},
		"falsy_status_optionals": Object{"product": "", "status": false, "read": Object{"path": "/things", "operation_id": "", "path_kind": 0}},
	}
	fetchBytes, err := canonjson.Render(renderableJSON(registryCoverage(view, "", "", registry, "fetch")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fetchBytes, `"absent_product"`) || strings.Contains(fetchBytes, `"false_product"`) || !strings.Contains(fetchBytes, `"pagination": ""`) || !strings.Contains(fetchBytes, `"pagination": "nested"`) || !strings.Contains(fetchBytes, `"pagination": false`) {
		t.Fatalf("fetch optional parity bytes: %s", fetchBytes)
	}
	read := registryCoverage(view, "", "", registry, "read")
	readBytes, err := canonjson.Render(renderableJSON(read))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readBytes, `"reason": "graphql_source"`) || strings.Contains(readBytes, `"operation_id"`) || strings.Contains(readBytes, `"path_kind"`) {
		t.Fatalf("read optional parity bytes: %s", readBytes)
	}
}

func TestBuildConsumesCanonicalSourceEvidenceArtifact(t *testing.T) {
	artifact := sourceEvidenceArtifact(t)
	value, err := canonjson.Decode(artifact)
	if err != nil {
		t.Fatalf("canonjson.Decode(source-registry.json) error = %v, want nil", err)
	}
	registry, ok := value.(Object)
	if !ok {
		t.Fatalf("source-registry.json root = %T, want object", value)
	}
	document, err := documentFor(t, Object{
		"openapi": "3.0.3",
		"paths": Object{
			"/v1/direct/{id}":  Object{"get": Object{}},
			"/v1/catalog/{id}": Object{"get": Object{}},
		},
	})
	if err != nil {
		t.Fatalf("documentFor(source evidence OpenAPI) error = %v", err)
	}
	report, err := Build(context.Background(), Options{
		SchemaData:          recordedValueFromFile(t, "tests/fixtures/authoring/source-first-v2/provider-schema.json"),
		Document:            document,
		ProviderSource:      stringPointer("registry.terraform.io/fixture/sourcefirst"),
		ResourcePrefix:      "sourcefirst",
		APIPrefix:           stringPointer(""),
		RegistryData:        &registry,
		InputProvenanceData: sourceInputProvenanceArtifact(t),
	})
	if err != nil {
		t.Fatalf("Build(canonical source-registry.json) error = %v, want nil", err)
	}
	read := object(report.Data()["registry_read_coverage"])
	resources := objects(read["resources"])
	if got, want := len(resources), 8; got != want {
		t.Fatalf("Build(canonical source-registry.json) read resources = %d, want %d", got, want)
	}
	byResource := map[string]Object{}
	for _, resource := range resources {
		byResource[str(resource["resource"])] = resource
	}
	for resource, wantStatus := range map[string]string{
		"sourcefirst_direct_http":    "matched",
		"sourcefirst_sdk_http":       "matched",
		"sourcefirst_ambiguous":      "ambiguous_source_operation",
		"sourcefirst_dynamic":        "dynamic",
		"sourcefirst_no_source":      "no_source",
		"sourcefirst_not_applicable": "not_applicable",
		"sourcefirst_sdk_symbol":     "observed_sdk_call",
		"sourcefirst_unresolved":     "unresolved",
	} {
		got, ok := byResource[resource]
		if !ok {
			t.Errorf("Build(canonical source-registry.json) missing resource %q", resource)
			continue
		}
		if gotStatus := str(got["status"]); gotStatus != wantStatus {
			t.Errorf("Build(canonical source-registry.json) %s status = %q, want %q", resource, gotStatus, wantStatus)
		}
	}
	if got := str(byResource["sourcefirst_ambiguous"]["source_reason_code"]); got != "multiple_viable_candidates" {
		t.Errorf("Build(canonical source-registry.json) ambiguous reason = %q, want multiple_viable_candidates", got)
	}
	if got := str(byResource["sourcefirst_direct_http"]["source_trust"]); got != "verified" {
		t.Errorf("Build(canonical source-registry.json) source row trust = %q, want verified", got)
	}
	surface := object(report.Data()["surface_map"])
	surfaceStatus := map[string]string{}
	for _, value := range objects(surface["records"]) {
		if str(value["source"]) == "source_read_registry" {
			surfaceStatus[str(value["resource_type"])] = str(value["match_status"])
		}
	}
	for resource, wantStatus := range map[string]string{
		"sourcefirst_direct_http":    "matched",
		"sourcefirst_sdk_http":       "matched",
		"sourcefirst_ambiguous":      "ambiguous",
		"sourcefirst_dynamic":        "unsupported_for_now",
		"sourcefirst_sdk_symbol":     "unsupported_for_now",
		"sourcefirst_unresolved":     "missing",
		"sourcefirst_no_source":      "missing",
		"sourcefirst_not_applicable": "missing",
	} {
		if got := surfaceStatus[resource]; got != wantStatus {
			t.Errorf("Build(canonical source-registry.json) %s surface status = %q, want %q", resource, got, wantStatus)
		}
	}
	if _, fabricated := byResource["sourcefirst_ambiguous"]["read_path"]; fabricated {
		t.Error("Build(canonical source-registry.json) fabricated a read path for multichain evidence")
	}
	summary := object(read["summary"])
	wantCounts := map[string]int{
		"ambiguous": 1, "dynamic": 1, "no_source": 1, "not_applicable": 1,
		"observed_http": 2, "observed_sdk_call": 1, "unresolved": 1,
	}
	gotCounts := object(summary["source_classification_counts"])
	for key, want := range wantCounts {
		if got := fmt.Sprint(gotCounts[key]); got != fmt.Sprint(want) {
			t.Errorf("Build(canonical source-registry.json) source classification count %s = %s, want %d", key, got, want)
		}
	}
	if got := str(summary["source_trust"]); got != "verified" {
		t.Errorf("Build(canonical source-registry.json) source trust = %q, want verified", got)
	}
	if got := str(summary["source_manifest_sha256"]); len(got) != 64 {
		t.Errorf("Build(canonical source-registry.json) source manifest digest = %q, want SHA-256", got)
	}
	if got := str(summary["input_provenance_sha256"]); len(got) != 64 {
		t.Errorf("Build(canonical source-registry.json) input provenance digest = %q, want SHA-256", got)
	}
	if got := fmt.Sprint(summary["source_applicable_total"]); got != "7" {
		t.Errorf("Build(canonical source-registry.json) source applicable total = %s, want 7", got)
	}
	sourceCoverage := object(summary["source_endpoint_coverage"])
	if got := str(sourceCoverage["state"]); got != "ratio" {
		t.Errorf("Build(canonical source-registry.json) source endpoint coverage state = %q, want ratio", got)
	}
	if got := fmt.Sprint(sourceCoverage["numerator"]); got != "2" {
		t.Errorf("Build(canonical source-registry.json) source endpoint coverage numerator = %s, want 2", got)
	}
	if got := fmt.Sprint(sourceCoverage["denominator"]); got != "7" {
		t.Errorf("Build(canonical source-registry.json) source endpoint coverage denominator = %s, want 7", got)
	}
}

func TestBuildRejectsMalformedDeclaredCanonicalSourceEvidence(t *testing.T) {
	value, err := canonjson.Decode(sourceEvidenceArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := value.(Object)
	delete(registry, "resources")
	document, err := documentFor(t, Object{"openapi": "3.0.3", "paths": Object{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(context.Background(), Options{
		SchemaData:   Object{"resource_schemas": Object{}},
		Document:     document,
		RegistryData: &registry,
	})
	if err == nil {
		t.Fatal("Build(malformed declared canonical source evidence) error = nil, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "canonical source evidence") {
		t.Fatalf("Build(malformed declared canonical source evidence) error = %v, want canonical evidence diagnostic", err)
	}
	rendered, renderErr := canonjson.Render(registry)
	if renderErr != nil {
		t.Fatal(renderErr)
	}
	if _, err := Build(context.Background(), Options{
		SchemaData:         Object{"resource_schemas": Object{}},
		Document:           document,
		SourceEvidenceData: []byte(rendered),
	}); err == nil {
		t.Fatal("Build(malformed raw canonical source evidence) error = nil, want fail-closed error")
	}
}

func TestBuildRejectsUnverifiedCanonicalSourceEvidenceOnBothRoutes(t *testing.T) {
	reportData, provenanceData := unverifiedSourceEvidencePair(t)
	value, err := canonjson.Decode(reportData)
	if err != nil {
		t.Fatal(err)
	}
	registry := value.(Object)
	document, err := documentFor(t, Object{"openapi": "3.0.3", "paths": Object{}})
	if err != nil {
		t.Fatal(err)
	}
	for name, options := range map[string]Options{
		"declared registry": {
			SchemaData:          Object{"resource_schemas": Object{}},
			Document:            document,
			RegistryData:        &registry,
			InputProvenanceData: provenanceData,
		},
		"raw source evidence": {
			SchemaData:          Object{"resource_schemas": Object{}},
			Document:            document,
			SourceEvidenceData:  reportData,
			InputProvenanceData: provenanceData,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Build(context.Background(), options); err == nil || !strings.Contains(err.Error(), "verified source trust") {
				t.Fatalf("Build(%s) error = %v, want verified-trust rejection", name, err)
			}
		})
	}
}

func TestBuildRejectsCanonicalEvidenceWithoutOrWithMismatchedProvenance(t *testing.T) {
	reportData := sourceEvidenceArtifact(t)
	value, err := canonjson.Decode(reportData)
	if err != nil {
		t.Fatal(err)
	}
	registry := value.(Object)
	document, err := documentFor(t, Object{"openapi": "3.0.3", "paths": Object{}})
	if err != nil {
		t.Fatal(err)
	}
	base := func() Options {
		return Options{
			SchemaData:   Object{"resource_schemas": Object{}},
			Document:     document,
			RegistryData: &registry,
		}
	}
	if _, err := Build(context.Background(), base()); err == nil || !strings.Contains(err.Error(), "requires input provenance") {
		t.Fatalf("Build(canonical source evidence without provenance) error = %v, want missing-provenance rejection", err)
	}
	for name, mutate := range map[string]func(*contracts.SourceEvidenceReport){
		"input provenance digest": func(report *contracts.SourceEvidenceReport) {
			report.InputProvenanceSHA256 = strings.Repeat("0", 64)
		},
		"source manifest digest": func(report *contracts.SourceEvidenceReport) {
			changed := strings.Repeat("0", 64)
			report.SourceManifestSHA256 = &changed
		},
	} {
		t.Run(name, func(t *testing.T) {
			report, err := contracts.DecodeSourceEvidenceReport(reportData)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&report)
			mutated, err := contracts.RenderSourceEvidenceReport(report)
			if err != nil {
				t.Fatal(err)
			}
			mutatedValue, err := canonjson.Decode([]byte(mutated))
			if err != nil {
				t.Fatal(err)
			}
			mutatedRegistry := mutatedValue.(Object)
			for route, options := range map[string]Options{
				"declared registry": func() Options {
					options := base()
					options.RegistryData = &mutatedRegistry
					options.InputProvenanceData = sourceInputProvenanceArtifact(t)
					return options
				}(),
				"raw source evidence": func() Options {
					options := base()
					options.RegistryData = nil
					options.SourceEvidenceData = []byte(mutated)
					options.InputProvenanceData = sourceInputProvenanceArtifact(t)
					return options
				}(),
			} {
				if _, err := Build(context.Background(), options); err == nil || !strings.Contains(err.Error(), "bind canonical source evidence") {
					t.Errorf("Build(%s with mismatched provenance digest) error = %v, want binding rejection", route, err)
				}
			}
		})
	}
}

func TestBuildPreservesLegacyRegistryInputShape(t *testing.T) {
	document, err := documentFor(t, Object{"openapi": "3.0.3", "paths": Object{"/things": Object{"get": Object{}}}})
	if err != nil {
		t.Fatal(err)
	}
	registry := Object{"example_thing": Object{"fetch": Object{"path": "/things"}, "product": "example"}}
	report, err := Build(context.Background(), Options{
		SchemaData:     Object{"resource_schemas": Object{"example_thing": Object{"block": Object{}}}},
		Document:       document,
		ResourcePrefix: "example",
		RegistryData:   &registry,
	})
	if err != nil {
		t.Fatalf("Build(legacy registry) error = %v, want nil", err)
	}
	fetch := object(report.Data()["registry_fetch_coverage"])
	if got := len(objects(fetch["resources"])); got != 1 {
		t.Fatalf("Build(legacy registry) fetch resources = %d, want 1", got)
	}
	if _, present := object(fetch["summary"])["source_classification_counts"]; present {
		t.Error("Build(legacy registry) added source classification counts to legacy fetch coverage")
	}
}

func sourceEvidenceArtifact(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) = false")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "tests", "fixtures", "authoring", "source-first-v2", "expected", "source-evidence-report-v1.json"))
	if err != nil {
		t.Fatalf("ReadFile(source-registry.json) error = %v", err)
	}
	return data
}

func sourceInputProvenanceArtifact(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) = false")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "tests", "fixtures", "authoring", "source-first-v2", "expected", "input-provenance.json"))
	if err != nil {
		t.Fatalf("ReadFile(input-provenance.json) error = %v", err)
	}
	return data
}

func unverifiedSourceEvidencePair(t *testing.T) ([]byte, []byte) {
	t.Helper()
	report, err := contracts.DecodeSourceEvidenceReport(sourceEvidenceArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := contracts.DecodeInputProvenance(sourceInputProvenanceArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest := verified.SourceManifest
	observation := &contracts.UnverifiedSourceObservation{
		ProviderModulePath: manifest.Provider.ModulePath,
		ProviderFiles:      append([]contracts.FileBinding(nil), manifest.Provider.Files...),
		TerraformSchema:    manifest.TerraformSchema,
		Selection:          manifest.Selection,
	}
	for _, sdk := range manifest.SDKs {
		observation.SDKs = append(observation.SDKs, contracts.UnverifiedSDKObservation{
			ModulePath:    sdk.ModulePath,
			ModuleVersion: sdk.ModuleVersion,
			Files:         append([]contracts.FileBinding(nil), sdk.Files...),
		})
	}
	unverified := contracts.InputProvenance{
		Kind:                  verified.Kind,
		SchemaVersion:         verified.SchemaVersion,
		SourceTrust:           contracts.SourceTrustUnverified,
		UnverifiedObservation: observation,
	}
	provenance, err := contracts.RenderInputProvenance(unverified)
	if err != nil {
		t.Fatalf("RenderInputProvenance(unverified) error = %v", err)
	}
	report.SourceTrust = contracts.SourceTrustUnverified
	report.SourceManifestSHA256 = nil
	for resource, row := range report.Resources {
		row.LegacyMapped = false
		report.Resources[resource] = row
	}
	digest := sha256.Sum256([]byte(provenance))
	report.InputProvenanceSHA256 = hex.EncodeToString(digest[:])
	rendered, err := contracts.RenderSourceEvidenceReport(report)
	if err != nil {
		t.Fatalf("RenderSourceEvidenceReport(unverified) error = %v", err)
	}
	return []byte(rendered), []byte(provenance)
}

func recordedValueFromFile(t *testing.T, name string) Object {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) = false")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", name, err)
	}
	value, err := canonjson.Decode(data)
	if err != nil {
		t.Fatalf("canonjson.Decode(%q) error = %v", name, err)
	}
	return recordedValue(value)
}

func stringPointer(value string) *string { return &value }

func documentFor(t *testing.T, value Object) (openapiadapter.Document, error) {
	t.Helper()
	rendered, err := canonjson.Render(value)
	if err != nil {
		return openapiadapter.Document{}, err
	}
	bytes := []byte(rendered)
	sum := sha256.Sum256(bytes)
	return openapiadapter.ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{{Path: "root.json", Bytes: bytes, SHA256: hex.EncodeToString(sum[:])}}})
}

func recordedValue(value any) Object {
	object := object(value)
	if nested, ok := object["json"].(map[string]any); ok {
		return nested
	}
	return object
}

func anyObjects(value any) []Object { return objects(value) }

func firstDifference(left, right string) string {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return left[testMax(0, i-80):min(len(left), i+80)] + " != " + right[testMax(0, i-80):min(len(right), i+80)]
		}
	}
	return "different lengths"
}

func testMax(left, right int) int {
	if left > right {
		return left
	}
	return right
}

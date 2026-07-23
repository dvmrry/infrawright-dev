package openapimap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const mappingCompatibilitySHA256 = "efa5e183cdb1817a704d0eedd355dde1bbc3d5ca449a4fa3068cec0bfabc1f6e"

func TestMappingCompatibilityReports(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join("testdata", "mapping_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != mappingCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, mappingCompatibilitySHA256)
	}
	fixture, err := canonjson.Decode(fixtureBytes)
	if err != nil {
		t.Fatalf("canonjson.Decode(%q) error: %v", fixturePath, err)
	}
	fixtureObject := object(fixture)
	groups := []struct {
		name  string
		cases any
		want  int
	}{
		{name: "live", cases: fixtureObject["live_reports"], want: 6},
		{name: "retained", cases: fixtureObject["retained_reports"], want: 13},
	}
	for _, group := range groups {
		cases := anyObjects(group.cases)
		if got := len(cases); got != group.want {
			t.Fatalf("%s compatibility reports = %d, want %d", group.name, got, group.want)
		}
		for _, test := range cases {
			t.Run(group.name+"/"+str(test["name"]), func(t *testing.T) {
				input := object(test["input"])
				document, err := documentFor(t, recordedValue(input["openapi"]))
				if err != nil {
					t.Fatalf("documentFor(%s) error: %v", test["name"], err)
				}
				var providerSource *string
				if text, ok := input["provider_source"].(string); ok {
					providerSource = &text
				}
				registry := recordedValue(input["registry_data"])
				if input["registry_data"] == nil {
					registry = defaultRegistry(t)
					if len(registry) == 0 {
						t.Fatal("defaultRegistry() returned an empty registry")
					}
				}
				apiPrefix := str(input["api_prefix"])
				report, err := Build(context.Background(), Options{
					SchemaData:     recordedValue(input["schema"]),
					Document:       document,
					ProviderSource: providerSource,
					ResourcePrefix: str(input["resource_prefix"]),
					APIPrefix:      &apiPrefix,
					RegistryData:   &registry,
				})
				if err != nil {
					t.Fatalf("Build(%s) error: %v", test["name"], err)
				}
				got, err := report.Render()
				if err != nil {
					t.Fatalf("Report.Render(%s) error: %v", test["name"], err)
				}
				want, err := canonjson.Render(test["report"])
				if err != nil {
					t.Fatalf("canonjson.Render(%s report) error: %v", test["name"], err)
				}
				if string(got) != want {
					t.Errorf("Build(%s) report mismatch at %s", test["name"], firstDifference(want, string(got)))
				}
			})
		}
	}
}

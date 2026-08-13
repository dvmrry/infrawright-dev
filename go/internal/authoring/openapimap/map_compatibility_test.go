package openapimap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/fixtureupdate"
)

const mappingCompatibilitySHA256 = "adfa5f11d07220c1fdd95827d05dd6ad5293c94448dc366df06c3f9beeded91a"

// updateMappingCompatibility is the IW_UPDATE_FIXTURES=1 refresh path: every
// case's inputs are fully recorded in the fixture, so the expected report is
// derivable by replaying Build over them. Adding a case is therefore a hand
// edit of inputs only (leave "report" empty or stale); this mode fills every
// case's report and rewrites the pinned constant. Review the diff before
// committing.
func updateMappingCompatibility(t *testing.T, fixturePath string, fixtureBytes []byte) {
	t.Helper()
	fixture, err := canonjson.Decode(fixtureBytes)
	if err != nil {
		t.Fatalf("canonjson.Decode(%q) error: %v", fixturePath, err)
	}
	fixtureObject := object(fixture)
	for _, groupName := range []string{"live_reports", "retained_reports"} {
		for _, test := range anyObjects(fixtureObject[groupName]) {
			input := object(test["input"])
			resourcePrefix := str(input["resource_prefix"])
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
				requireDefaultRegistryPack(t, resourcePrefix)
				registry = defaultRegistry(t)
			}
			apiPrefix := str(input["api_prefix"])
			report, err := Build(context.Background(), Options{
				SchemaData:     recordedValue(input["schema"]),
				Document:       document,
				ProviderSource: providerSource,
				ResourcePrefix: resourcePrefix,
				APIPrefix:      &apiPrefix,
				RegistryData:   &registry,
			})
			if err != nil {
				t.Fatalf("Build(%s) error: %v", test["name"], err)
			}
			rendered, err := report.Render()
			if err != nil {
				t.Fatalf("Report.Render(%s) error: %v", test["name"], err)
			}
			expected, err := canonjson.Decode([]byte(rendered))
			if err != nil {
				t.Fatalf("canonjson.Decode(%s rendered report) error: %v", test["name"], err)
			}
			test["report"] = expected
		}
	}
	// Deterministic re-encode: sorted keys, compact. The original capture's
	// insertion order is not reconstructible from a decoded value, so the
	// first update run re-canonicalizes the whole fixture once; every later
	// run is byte-idempotent.
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("encode mapping compatibility fixture: %v", err)
	}
	encoded = append(encoded, 0x0a)
	if err := os.WriteFile(fixturePath, encoded, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(encoded)
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	if err := fixtureupdate.ReplaceConst(sourcePath, "mappingCompatibilitySHA256", hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("fixtureupdate.ReplaceConst error: %v", err)
	}
	t.Skipf("mapping compatibility snapshot regenerated; review the diff before committing")
}

func TestMappingCompatibilityReports(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join("testdata", "mapping_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	if fixtureupdate.Requested() {
		updateMappingCompatibility(t, fixturePath, fixtureBytes)
		return
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
	// The pinned fixture digest is the membership authority; group sizes are
	// whatever the fixture carries. Hardcoded counts here only made adding a
	// recorded case a two-file magic-number edit.
	groups := []struct {
		name  string
		cases any
	}{
		{name: "live", cases: fixtureObject["live_reports"]},
		{name: "retained", cases: fixtureObject["retained_reports"]},
	}
	for _, group := range groups {
		cases := anyObjects(group.cases)
		if len(cases) == 0 {
			t.Fatalf("%s compatibility reports are empty", group.name)
		}
		for _, test := range cases {
			t.Run(group.name+"/"+str(test["name"]), func(t *testing.T) {
				input := object(test["input"])
				resourcePrefix := str(input["resource_prefix"])
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
					requireDefaultRegistryPack(t, resourcePrefix)
					registry = defaultRegistry(t)
				}
				apiPrefix := str(input["api_prefix"])
				report, err := Build(context.Background(), Options{
					SchemaData:     recordedValue(input["schema"]),
					Document:       document,
					ProviderSource: providerSource,
					ResourcePrefix: resourcePrefix,
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

func requireDefaultRegistryPack(t *testing.T, resourcePrefix string) {
	t.Helper()
	switch resourcePrefix {
	case "netbox", "zpa", "ztc":
	default:
		return
	}
	packPath := filepath.Join("..", "..", "..", "..", "packs", resourcePrefix)
	if _, err := os.Stat(packPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("%s pack is not installed", resourcePrefix)
		}
		t.Fatalf("os.Stat(%q) error = %v, want nil", packPath, err)
	}
}

package envgen

// TestSetBlockRenderDerivationMatchesCommittedCacheByteForByte extends the
// Part A parity fixture family in reference_resolvers_test.go
// (TestDerivedBindingsMatchCommittedCacheByteForByte) to the one binding
// shape that family never exercised end to end: a reference field whose
// dotted path crosses a set-nested block (BindingContext.SetBlockFields),
// the "bind the complete block leaf" case documented on
// TransformArtifactPaths.SetBlockFields and pinned in isolation by
// set_block_binding_roundtrip_test.go. See
// docs/superpowers/specs/2026-07-31-sidecar-minimization-design.md Part A.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

// syntheticSetBlockPackRoot builds the smallest pack universe carrying a
// set-nested reference field -- zia_firewall_filtering_network_service_groups
// .services.id, the shape set_block_binding_roundtrip_test.go's
// ziaSetBlockSchema names as "the downstream wave-2 blocker": a set block
// whose element carries a set(number) reference attribute. It is kept
// separate from syntheticRootForTopology (pack_scope_test.go) rather than
// added to it: that root's zpa_server_group/zia_url_filtering_rules nested
// reference fields are deliberately list-nested (several other tests pin
// their indexed-path behavior), and folding a set-nested block into an
// existing field there would change what those tests exercise. A dedicated,
// minimal root keeps this fixture's blast radius at zero.
func syntheticSetBlockPackRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := t.TempDir()

	computedString := func() metadata.JsonObject { return terraformTestAttribute("string", "computed") }
	optionalString := func() metadata.JsonObject { return terraformTestAttribute("string", "optional") }

	ziaSchemas := metadata.JsonObject{
		"zia_firewall_filtering_network_service": terraformTestBlock(metadata.JsonObject{
			"id":   computedString(),
			"name": optionalString(),
		}),
		"zia_firewall_filtering_network_service_groups": metadata.JsonObject{"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"id":   computedString(),
				"name": optionalString(),
			},
			"block_types": metadata.JsonObject{
				"services": metadata.JsonObject{
					"nesting_mode": "set",
					"block": metadata.JsonObject{"attributes": metadata.JsonObject{
						"id": terraformTestAttribute([]any{"set", "number"}, "required"),
					}},
				},
			},
		}},
	}
	ziaRegistry := metadata.JsonObject{}
	for resourceType := range ziaSchemas {
		ziaRegistry[resourceType] = metadata.JsonObject{"generate": true, "product": "zia"}
	}
	writeSyntheticTopologyPack(t, packsRoot, "zia", metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"zia_": "zia"},
		"provider_sources":  metadata.JsonObject{"zia": "zscaler/zia"},
		"references": metadata.JsonObject{
			"zia_firewall_filtering_network_service_groups": metadata.JsonObject{
				"services.id": metadata.JsonObject{"name_field": "name", "referent": "zia_firewall_filtering_network_service"},
			},
		},
	}, ziaRegistry, ziaSchemas)

	profilePath := filepath.Join(packsRoot, "synthetic.packset.json")
	writeJSONFile(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []any{"zia"}, "shared": []any{},
	})
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(synthetic set-block fixture) error: %v", err)
	}
	return loaded
}

// TestSetBlockRenderDerivationMatchesCommittedCacheByteForByte drives a
// set-nested reference field through both halves of the Part A bridge: a
// committed .generated.expressions.json cache, and render-derivation over
// the same tokenised config with that cache absent. Same derivation engine,
// same inputs, so the two must produce byte-identical
// expression_bindings.tf -- a divergence here would mean the set-block
// complete-leaf path (bindSetBlockField/renderSetBlockLeaf) disagrees with
// itself depending on whether its output touched disk in between, which is
// exactly the class of defect the scalar-field parity test upstream cannot
// see.
//
// The committed cache is not hand-authored: its exact composite-expression
// bytes (the "[{ id = [...] }]" grammar TestCompositeExpressionAllowlist
// pins) are produced by calling tfrender.DeriveGeneratedBindings directly --
// the same function, over the same BindingContext
// (transformrun.TransformBindingContext), that deriveGeneratedBindingLayer
// calls at render time. That is what makes the comparison meaningful: both
// halves run the identical derivation; only the write-then-reread hop
// differs.
func TestSetBlockRenderDerivationMatchesCommittedCacheByteForByte(t *testing.T) {
	root := syntheticSetBlockPackRoot(t)
	workspace := temporaryDirectory(t, "infrawright-gen-env-set-block-derivation-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace,
		"roots":   map[string]any{"zia": map[string]any{"cross_state_references": true}},
	})

	const referrerType = "zia_firewall_filtering_network_service_groups"
	const referentType = "zia_firewall_filtering_network_service"

	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, referentType+".auto.tfvars.json"), map[string]any{
		"items": map[string]any{"service_one": map[string]any{"name": "Service One"}},
	})
	writeJSONFile(t, filepath.Join(config, "lookups", referentType+".lookup.json"), map[string]any{
		"by_id":     map[string]any{"svc-1": "Service One"},
		"id_by_key": map[string]any{"service_one": "svc-1"},
		"key_by_id": map[string]any{"svc-1": "service_one"},
	})
	writeJSONFile(t, filepath.Join(config, referrerType+".auto.tfvars.json"), map[string]any{
		"items": map[string]any{"group_one": map[string]any{
			"name": "IoT Group",
			"services": []any{
				map[string]any{"id": []any{referentType + ".service_one"}},
			},
		}},
	})

	dep := loadDeploymentFile(t, deploymentPath)
	tenant := "tenant"

	fullResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Deployment: dep, Root: root, Selectors: []string{}, Tenant: &tenant,
	})
	if err != nil {
		t.Fatalf("roots.LoadedRootTopology error = %v, want nil", err)
	}
	topology := fullResult.Topology

	resource, err := resourceMetadata(root, referrerType)
	if err != nil {
		t.Fatalf("resourceMetadata(%s) error = %v, want nil", referrerType, err)
	}
	schema, err := root.LoadResourceSchema(resource.Type)
	if err != nil {
		t.Fatalf("LoadResourceSchema(%s) error = %v, want nil", referrerType, err)
	}
	references := transformrun.TransformReferenceSpecs(root, resource)
	if _, ok := references["services.id"]; !ok {
		t.Fatalf("TransformReferenceSpecs(%s) = %v, want services.id declared", referrerType, references)
	}
	context, err := transformrun.TransformBindingContext(dep, root, resource, topology.ResourceRoots, references, schema)
	if err != nil {
		t.Fatalf("TransformBindingContext error = %v, want nil", err)
	}
	// This is the fixture's load-bearing assertion: without a schema-derived
	// set-block index for services.id, the rest of the test would silently
	// exercise the ordinary scalar/list binding path the parity test upstream
	// already covers, proving nothing new.
	if index, ok := context.SetBlockFields["services.id"]; !ok || index != 0 {
		t.Fatalf("SetBlockFields[services.id] = (%d, %v), want (0, true) -- fixture does not exercise the set-block path", index, ok)
	}

	configPath, err := configFile(dep, tenant, resource.Type)
	if err != nil {
		t.Fatalf("configFile error = %v, want nil", err)
	}
	items, err := loadConfigItems(configPath, variableName(topology, resource.Type))
	if err != nil {
		t.Fatalf("loadConfigItems error = %v, want nil", err)
	}
	keyMaps, err := tfrender.LookupKeyMaps(path.Dir(configPath), references)
	if err != nil {
		t.Fatalf("LookupKeyMaps error = %v, want nil", err)
	}
	result, err := tfrender.DeriveGeneratedBindings(context, items, keyMaps, resource.Type)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings error = %v, want nil", err)
	}
	if len(result.Resources) == 0 {
		t.Fatal("DeriveGeneratedBindings bound nothing; the fixture has nothing to prove")
	}
	cacheText, err := tfrender.RenderGeneratedBindings(result.Resources)
	if err != nil {
		t.Fatalf("RenderGeneratedBindings error = %v, want nil", err)
	}

	cacheFile, err := generatedBindingsFile(dep, tenant, resource.Type)
	if err != nil {
		t.Fatalf("generatedBindingsFile error = %v, want nil", err)
	}
	if err := os.WriteFile(cacheFile, []byte(cacheText), 0o666); err != nil {
		t.Fatalf("os.WriteFile(cache) error = %v, want nil", err)
	}

	outputRoot := filepath.Join(workspace, "generated")
	generate := func() (map[string]string, []string) {
		t.Helper()
		diagnostics := make([]string, 0)
		run := outputRoot
		if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
			Deployment:   dep,
			FormatHcl:    identityFormatter,
			OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
			OutputRoot:   &run,
			Root:         root,
			Selectors:    []string{referrerType},
			Tenant:       tenant,
		}); err != nil {
			t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
		}
		return snapshotTree(t, filepath.Join(outputRoot, "tenant", referrerType)), diagnostics
	}

	bridged, _ := generate()
	bridgedBindings, present := bridged[expressionBindingsTF]
	if !present {
		t.Fatalf("bridge tree = %v, want %s to compare against", mapKeysForTest(bridged), expressionBindingsTF)
	}
	// Guards against a vacuous pass: the bytes must actually carry the
	// set-block resolver, not merely an empty or unrelated bindings file.
	if !strings.Contains(bridgedBindings, "try(data.terraform_remote_state.zia_firewall_filtering_network_service") ||
		!strings.Contains(bridgedBindings, "infrawright_reference_lookup_zia_firewall_filtering_network_service") {
		t.Fatalf("%s = %q, want the bridge path's set-block lookup-first resolver", expressionBindingsTF, bridgedBindings)
	}

	if err := os.Remove(cacheFile); err != nil {
		t.Fatalf("os.Remove(cache) error = %v, want nil", err)
	}
	derived, diagnostics := generate()
	// The derivation accounting proves the second run actually derived rather
	// than reusing anything the first run left in the output tree.
	if !containsString(diagnostics, "NOTE bindings: "+referrerType+": 1 bound, 0 skipped") {
		t.Fatalf("diagnostics = %v, want the render-derivation accounting", diagnostics)
	}
	if got := derived[expressionBindingsTF]; got != bridgedBindings {
		t.Errorf("%s derived at render =\n%q\nwant the bridge path's bytes:\n%q", expressionBindingsTF, got, bridgedBindings)
	}
	if !equalTrees(derived, bridged) {
		t.Errorf("render-derived tree differs from the bridged tree:\ngot  %v\nwant %v",
			mapKeysForTest(derived), mapKeysForTest(bridged))
	}
}

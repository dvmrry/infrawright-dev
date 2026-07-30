package envgen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
)

const providerlessTerraformDataConfiguration = `terraform {
  required_version = ">= 1.4.0"
}

resource "terraform_data" "source" {
  input = {
    name = "provider-neutral-source"
  }
}

resource "terraform_data" "consumer" {
  input = terraform_data.source.output
}

output "consumer" {
  value = terraform_data.consumer.output
}
`

type providerlessTerraformPlan struct {
	Configuration struct {
		ProviderConfig map[string]struct {
			FullName string `json:"full_name"`
		} `json:"provider_config"`
		RootModule struct {
			Resources []struct {
				Address     string `json:"address"`
				Expressions map[string]struct {
					References []string `json:"references"`
				} `json:"expressions"`
			} `json:"resources"`
		} `json:"root_module"`
	} `json:"configuration"`
	ResourceChanges []struct {
		Address      string `json:"address"`
		ProviderName string `json:"provider_name"`
		Change       struct {
			Actions []string `json:"actions"`
		} `json:"change"`
	} `json:"resource_changes"`
}

func runProviderNeutralTerraformCommand(
	t *testing.T,
	executable string,
	directory string,
	environment []string,
	arguments ...string,
) []byte {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	if environment == nil {
		environment = os.Environ()
	}
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform %s in %s failed: %v\n%s", strings.Join(arguments, " "), directory, err, output)
	}
	return output
}

func loadProviderNeutralReferenceRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := filepath.Join("testdata", "provider_neutral_reference", "packs")
	profilePath := filepath.Join(packsRoot, "reference.packset.json")
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   packsRoot,
		ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(provider-neutral reference fixture): %v", err)
	}
	return root
}

func TestProviderNeutralReferenceBuildExercisesPackLoadAndCrossStateGeneration(t *testing.T) {
	workspace := t.TempDir()
	moduleRoot := filepath.Join(workspace, "modules")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"module_dir": moduleRoot,
		"overlay":    workspace,
		"roots": map[string]any{
			"fixture": map[string]any{"cross_state_references": true},
		},
	})
	configRoot := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(configRoot, "fixture_source.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"source_one": map[string]any{"name": "Source One"},
		},
	})
	writeJSONFile(t, filepath.Join(configRoot, "fixture_consumer.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"consumer_one": map[string]any{"name": "Consumer One", "source_id": "source-id"},
		},
	})
	writeJSONFile(t, filepath.Join(configRoot, "fixture_consumer.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"fixture_consumer.consumer_one": map[string]any{
				"source_id": map[string]any{
					"expression": `data.terraform_remote_state.fixture_source.outputs.infrawright_reference_ids.fixture_source["source_one"]`,
				},
			},
		},
	})

	root := loadProviderNeutralReferenceRoot(t)
	resourceTypes := modulesgen.ActiveGeneratedResourceTypes(root)
	wantResourceTypes := []string{"fixture_consumer", "fixture_source"}
	if !reflect.DeepEqual(resourceTypes, wantResourceTypes) {
		t.Fatalf("ActiveGeneratedResourceTypes(provider-neutral reference fixture) = %v, want %v", resourceTypes, wantResourceTypes)
	}
	formatter := modulesgen.NewHCLFormatter()
	generatedModules, err := modulesgen.GenerateActiveModules(root, modulesgen.GenerateModuleOptions{
		OutputRoot: moduleRoot,
		FormatHCL:  formatter,
	})
	if err != nil {
		t.Fatalf("GenerateActiveModules(provider-neutral reference fixture): %v", err)
	}
	if len(generatedModules) != len(wantResourceTypes) {
		t.Errorf("GenerateActiveModules(provider-neutral reference fixture) count = %d, want %d", len(generatedModules), len(wantResourceTypes))
	}
	if _, err := modulesgen.ValidateGeneratedModuleTree(moduleRoot, wantResourceTypes); err != nil {
		t.Errorf("ValidateGeneratedModuleTree(provider-neutral reference fixture): %v", err)
	}

	outputRoot := filepath.Join(workspace, "generated")
	generated, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, deploymentPath),
		FormatHcl:  formatter.FormatHCL,
		OutputRoot: &outputRoot,
		Root:       root,
		Selectors:  []string{"fixture_consumer"},
		Tenant:     "tenant",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots(provider-neutral reference fixture): %v", err)
	}
	if got := rootLabels(generated); !reflect.DeepEqual(got, wantResourceTypes) {
		t.Fatalf("GenerateEnvironmentRoots(provider-neutral reference fixture) labels = %v, want %v", got, wantResourceTypes)
	}

	sourceMain := readFileString(t, filepath.Join(outputRoot, "tenant", "fixture_source", "main.tf"))
	mustMatch(t, sourceMain, `output "infrawright_reference_ids"`)
	mustMatch(t, sourceMain, `fixture_source = \{ for key, item in module\.fixture_source\.items : key => item\.id \}`)
	consumerMain := readFileString(t, filepath.Join(outputRoot, "tenant", "fixture_consumer", "main.tf"))
	mustMatch(t, consumerMain, `data "terraform_remote_state" "fixture_source"`)
	mustMatch(t, consumerMain, `path = "\.\./fixture_source/terraform\.tfstate"`)
	overlay := readFileString(t, filepath.Join(outputRoot, "tenant", "fixture_consumer", "expression_bindings.tf"))
	mustMatch(t, overlay, `data\.terraform_remote_state\.fixture_source\.outputs\.infrawright_reference_ids\.fixture_source\["source_one"\]`)
	smoke := readFileString(t, filepath.Join(outputRoot, "tenant", "fixture_consumer", "tests", "smoke.tftest.hcl"))
	mustMatch(t, smoke, `override_data \{\n  target = data\.terraform_remote_state\.fixture_source\n  values = \{`)
	mustMatch(t, smoke, `fixture_source = \{\n          "source_one" = "20090001"`)

	t.Run("Terraform parses generated HCL", func(t *testing.T) {
		executable := terraformTestExecutable(t)
		for _, directory := range []string{moduleRoot, outputRoot} {
			runProviderNeutralTerraformCommand(t, executable, directory, nil, "fmt", "-check", "-recursive", ".")
		}
	})
}

func TestProviderlessTerraformDataReferencePlansWithBuiltInProvider(t *testing.T) {
	executable := terraformTestExecutable(t)
	workspace := t.TempDir()
	configurationPath := filepath.Join(workspace, "main.tf")
	if err := os.WriteFile(configurationPath, []byte(providerlessTerraformDataConfiguration), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s): %v", configurationPath, err)
	}
	environment := append(os.Environ(),
		"CHECKPOINT_DISABLE=1",
		"HOME="+t.TempDir(),
		"TF_DATA_DIR="+filepath.Join(workspace, ".terraform-data"),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
	)
	runProviderNeutralTerraformCommand(t, executable, workspace, environment, "init", "-backend=false", "-input=false", "-no-color")
	runProviderNeutralTerraformCommand(t, executable, workspace, environment, "validate", "-no-color")
	runProviderNeutralTerraformCommand(t, executable, workspace, environment, "plan", "-input=false", "-no-color", "-out=reference.tfplan")
	planJSON := runProviderNeutralTerraformCommand(t, executable, workspace, environment, "show", "-json", "reference.tfplan")

	var plan providerlessTerraformPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		t.Fatalf("json.Unmarshal(terraform show -json): %v", err)
	}
	var gotProviders []string
	for _, provider := range plan.Configuration.ProviderConfig {
		gotProviders = append(gotProviders, provider.FullName)
	}
	sort.Strings(gotProviders)
	wantProviders := []string{"terraform.io/builtin/terraform"}
	if !reflect.DeepEqual(gotProviders, wantProviders) {
		t.Errorf("terraform_data reference provider names = %v, want %v", gotProviders, wantProviders)
	}

	wantResources := map[string]bool{
		"terraform_data.consumer": true,
		"terraform_data.source":   true,
	}
	gotResources := make(map[string]bool, len(plan.ResourceChanges))
	for _, resource := range plan.ResourceChanges {
		gotResources[resource.Address] = true
		if resource.ProviderName != "terraform.io/builtin/terraform" {
			t.Errorf("terraform_data reference resource %s provider = %q, want terraform.io/builtin/terraform", resource.Address, resource.ProviderName)
		}
		if wantActions := []string{"create"}; !reflect.DeepEqual(resource.Change.Actions, wantActions) {
			t.Errorf("terraform_data reference resource %s actions = %v, want %v", resource.Address, resource.Change.Actions, wantActions)
		}
	}
	if !reflect.DeepEqual(gotResources, wantResources) {
		t.Errorf("terraform_data reference planned resources = %v, want %v", gotResources, wantResources)
	}

	consumerFound := false
	consumerReferencesSource := false
	for _, resource := range plan.Configuration.RootModule.Resources {
		if resource.Address != "terraform_data.consumer" {
			continue
		}
		consumerFound = true
		for _, reference := range resource.Expressions["input"].References {
			if reference == "terraform_data.source.output" {
				consumerReferencesSource = true
			}
		}
	}
	if !consumerFound || !consumerReferencesSource {
		t.Errorf("terraform_data reference consumer found=%t references source output=%t, want both true", consumerFound, consumerReferencesSource)
	}
}

package envgen

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

func TestDataModuleInterfaceMatchesEnvironmentRootSelectors(t *testing.T) {
	const resourceType = "zia_location_groups"
	root := syntheticRootWithDataReferent(t)

	module, err := modulesgen.RenderModuleFiles(root, resourceType)
	if err != nil {
		t.Fatalf("RenderModuleFiles(%q) error = %v, want nil", resourceType, err)
	}
	outputDeclaration := regexp.MustCompile(`output\s+"([A-Za-z_][A-Za-z0-9_]*)"\s*\{`)
	childOutputs := map[string]bool{}
	for _, file := range module.Files {
		for _, match := range outputDeclaration.FindAllStringSubmatch(file.Content, -1) {
			childOutputs[match[1]] = true
		}
	}

	workspace := t.TempDir()
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay":    workspace,
		"module_dir": filepath.Join(workspace, "modules"),
	})
	loadedDeployment := loadDeploymentFile(t, deploymentPath)
	tenant := "tenant"
	topology := roots.RootTopology{
		ResourceRoots: map[string]string{resourceType: resourceType},
	}
	rootMain, err := RenderEnvironmentMain(RenderEnvironmentMainOptions{
		Deployment:           loadedDeployment,
		EnvironmentDirectory: filepath.Join(workspace, "envs", tenant, resourceType),
		Label:                resourceType,
		Members:              []string{resourceType},
		ReferenceOutputTypes: []string{resourceType},
		Root:                 root,
		Tenant:               tenant,
		Topology:             topology,
	})
	if err != nil {
		t.Fatalf("RenderEnvironmentMain(%q) error = %v, want nil", resourceType, err)
	}

	selectorPattern := regexp.MustCompile(`module\.` + resourceType + `\.([A-Za-z_][A-Za-z0-9_]*)`)
	selectors := selectorPattern.FindAllStringSubmatch(rootMain, -1)
	if len(selectors) == 0 {
		t.Fatalf("RenderEnvironmentMain(%q) = %q, want at least one module selector", resourceType, rootMain)
	}
	for _, selector := range selectors {
		outputName := selector[1]
		if !childOutputs[outputName] {
			t.Errorf("RenderEnvironmentMain(%q) selects module.%s.%s, but the rendered child declares outputs %q", resourceType, resourceType, outputName, strings.Join(sortedOutputNames(childOutputs), ", "))
		}
	}
}

func sortedOutputNames(outputs map[string]bool) []string {
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

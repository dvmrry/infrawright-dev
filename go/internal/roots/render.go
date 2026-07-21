package roots

// render.go retains the established stdout shapes for root topology,
// changed-path scope, and plan roots.
// The Go topology types are tag-less structs, so each renderer hand-builds
// the canonical JSON value tree with the exact key names from
// node-src/domain/types.ts. Committed Go v2 authority goldens pin the complete
// resulting bytes because the frozen Node topology remains v1.

import (
	"encoding/json"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringsValue(values []string) []any {
	output := make([]any, len(values))
	for index, value := range values {
		output[index] = value
	}
	return output
}

func topologyValue(topology RootTopology) map[string]any {
	rootsValue := make([]any, len(topology.Roots))
	for index, root := range topology.Roots {
		rootsValue[index] = map[string]any{
			"label":    root.Label,
			"provider": nullableString(root.Provider),
			"members":  stringsValue(root.Members),
			"env_dir":  nullableString(root.EnvDir),
		}
	}
	var directories any
	if topology.Directories != nil {
		directories = map[string]any{
			"config":  topology.Directories.Config,
			"imports": topology.Directories.Imports,
			"envs":    topology.Directories.Envs,
		}
	}
	resourceRoots := map[string]any{}
	for resourceType, label := range topology.ResourceRoots {
		resourceRoots[resourceType] = label
	}
	return map[string]any{
		"kind":           string(topology.Kind),
		"schema_version": json.Number("1"),
		"tenant":         nullableString(topology.Tenant),
		"selectors":      stringsValue(topology.Selectors),
		"directories":    directories,
		"roots":          rootsValue,
		"resource_roots": resourceRoots,
	}
}

// RenderLegacyRootTopology ports renderLegacyRootTopology.
func RenderLegacyRootTopology(topology RootTopology) (string, error) {
	return canonjson.Render(topologyValue(topology))
}

// RenderLegacyRootDiagnostics ports renderLegacyRootDiagnostics: one
// "NOTE: <message>\n" line per diagnostic, no trailing separator.
func RenderLegacyRootDiagnostics(diagnostics []WholeRootDiagnostic) string {
	output := ""
	for _, diagnostic := range diagnostics {
		output += "NOTE: " + diagnostic.Message + "\n"
	}
	return output
}

// RenderLegacyChangedPathScope ports renderLegacyChangedPathScope.
func RenderLegacyChangedPathScope(scope ChangedPathScope) (string, error) {
	matches := make([]any, len(scope.PathMatches))
	for index, match := range scope.PathMatches {
		kinds := make([]any, len(match.Kinds))
		for kindIndex, kind := range match.Kinds {
			kinds[kindIndex] = string(kind)
		}
		matches[index] = map[string]any{
			"path":      match.Path,
			"kinds":     kinds,
			"tenants":   stringsValue(match.Tenants),
			"resources": stringsValue(match.Resources),
			"roots":     stringsValue(match.Roots),
		}
	}
	affected := make([]any, len(scope.AffectedRoots))
	for index, root := range scope.AffectedRoots {
		affected[index] = map[string]any{
			"label":             root.Label,
			"provider":          nullableString(root.Provider),
			"members":           stringsValue(root.Members),
			"matched_resources": stringsValue(root.MatchedResources),
			"paths":             stringsValue(root.Paths),
		}
	}
	return canonjson.Render(map[string]any{
		"kind":               string(scope.Kind),
		"schema_version":     json.Number("1"),
		"paths":              stringsValue(scope.Paths),
		"path_matches":       matches,
		"unmatched_paths":    stringsValue(scope.UnmatchedPaths),
		"affected_resources": stringsValue(scope.AffectedResources),
		"affected_roots":     affected,
	})
}

// RenderLegacyPlanRoots ports renderLegacyPlanRoots.
func RenderLegacyPlanRoots(planRoots PlanRoots) (string, error) {
	rootsValue := make([]any, len(planRoots.Roots))
	for index, root := range planRoots.Roots {
		rootsValue[index] = map[string]any{
			"tenant":         root.Tenant,
			"label":          root.Label,
			"provider":       nullableString(root.Provider),
			"members":        stringsValue(root.Members),
			"env_dir":        root.EnvDir,
			"artifact_state": string(root.ArtifactState),
			"artifacts": map[string]any{
				"tfplan": map[string]any{
					"path":   root.Artifacts.Tfplan.Path,
					"exists": root.Artifacts.Tfplan.Exists,
				},
				"tfplan_sources": map[string]any{
					"path":   root.Artifacts.TfplanSources.Path,
					"exists": root.Artifacts.TfplanSources.Exists,
				},
			},
		}
	}
	return canonjson.Render(map[string]any{
		"kind":           string(planRoots.Kind),
		"schema_version": json.Number("1"),
		"request": map[string]any{
			"tenant":    nullableString(planRoots.Request.Tenant),
			"selectors": stringsValue(planRoots.Request.Selectors),
		},
		"roots": rootsValue,
	})
}

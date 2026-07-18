package adopt

import (
	"fmt"
	"os"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func emptyAdoptionPolicyData() map[string]any {
	return map[string]any{
		"version":        float64(1),
		"resource_types": map[string]any{},
	}
}

func driftPolicyVersionOrOne(record map[string]any) any {
	value, present := record["version"]
	if !present || value == nil {
		return float64(1)
	}
	return value
}

// MergeAdoptionPolicyData ports mergePolicyData from
// node-src/domain/adopt-runner.ts, including PR 247's pack/user version-one
// reconciliation. Input trees are copied before they are merged.
func MergeAdoptionPolicyData(base, override any) any {
	baseRecord, baseOK := cloneAdoptionValue(base).(map[string]any)
	if !baseOK {
		baseRecord = emptyAdoptionPolicyData()
	}
	overrideRecord, overrideOK := override.(map[string]any)
	if !overrideOK {
		return cloneAdoptionValue(override)
	}
	if !metadata.IsSupportedDriftPolicyVersion(driftPolicyVersionOrOne(baseRecord)) ||
		!metadata.IsSupportedDriftPolicyVersion(driftPolicyVersionOrOne(overrideRecord)) {
		return cloneAdoptionValue(override)
	}
	baseRecord["version"] = float64(1)
	resources, ok := baseRecord["resource_types"].(map[string]any)
	if !ok {
		resources = map[string]any{}
		baseRecord["resource_types"] = resources
	}
	incoming, _ := overrideRecord["resource_types"].(map[string]any)
	for _, resourceType := range canonjson.SortedStrings(adoptMapKeys(incoming)) {
		rawConfig := incoming[resourceType]
		config, configOK := rawConfig.(map[string]any)
		if !configOK {
			resources[resourceType] = cloneAdoptionValue(rawConfig)
			continue
		}
		target, targetOK := resources[resourceType].(map[string]any)
		if !targetOK {
			target = map[string]any{}
			resources[resourceType] = target
		}
		for _, mode := range canonjson.SortedStrings(adoptMapKeys(config)) {
			rawEntries := config[mode]
			existing, _ := target[mode].([]any)
			combined := make([]any, 0, len(existing)+1)
			for _, entry := range existing {
				combined = append(combined, cloneAdoptionValue(entry))
			}
			if entries, isList := rawEntries.([]any); isList {
				for _, entry := range entries {
					combined = append(combined, cloneAdoptionValue(entry))
				}
			} else {
				combined = append(combined, cloneAdoptionValue(rawEntries))
			}
			target[mode] = combined
		}
	}
	return baseRecord
}

// PackAdoptionPolicyData ports packPolicyData from
// node-src/domain/adopt-runner.ts. Active manifest declaration order is
// load-bearing and is preserved.
func PackAdoptionPolicyData(root metadata.LoadedPackRoot) any {
	output := any(emptyAdoptionPolicyData())
	active := make(map[string]struct{}, len(root.Active.Packs))
	for _, pack := range root.Active.Packs {
		active[pack] = struct{}{}
	}
	for _, manifest := range root.Packs.Manifests {
		if _, enabled := active[manifest.Name]; !enabled {
			continue
		}
		policy, present := manifest.Data["drift_policy"]
		if !present {
			continue
		}
		output = MergeAdoptionPolicyData(output, policy)
	}
	return output
}

// LoadAdoptionPolicy ports loadAdoptionPolicy from
// node-src/domain/adopt-runner.ts. A non-nil path is validated independently
// before it is merged with active-pack policy.
func LoadAdoptionPolicy(root metadata.LoadedPackRoot, path *string) (*metadata.DriftPolicy, error) {
	base := PackAdoptionPolicyData(root)
	if path == nil {
		return metadata.NewDriftPolicy(base, "pack drift policy")
	}
	text, err := os.ReadFile(*path)
	if err != nil {
		return nil, fmt.Errorf("read adoption policy %s: %w", *path, err)
	}
	user, err := canonjson.ParseDataJSONLosslessly(string(text))
	if err != nil {
		return nil, err
	}
	if _, err := metadata.NewDriftPolicy(user, *path); err != nil {
		return nil, err
	}
	return metadata.NewDriftPolicy(MergeAdoptionPolicyData(base, user), *path+" merged with pack drift policy")
}

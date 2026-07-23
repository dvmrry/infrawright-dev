package adopt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

// AdoptBatchResult ports AdoptBatchResult from
// the original implementation.
type AdoptBatchResult struct {
	Failed    []string
	Processed []string
	Skipped   []string
}

// AdoptionStateRequest is the Go options bag accepted by one injected
// per-resource adoption state loader.
type AdoptionStateRequest struct {
	KeyToImportID map[string]string
	Policy        *metadata.DriftPolicy
	RawItems      map[string]map[string]any
	ResourceType  string
}

// AdoptionStateLoader ports AdoptionStateLoader. Tests inject loaders; the
// runner itself never resolves or launches Terraform.
type AdoptionStateLoader func(AdoptionStateRequest) (map[string]OracleStateObject, error)

// RunAdoptBatchOptions ports RunAdoptBatchOptions from
// the original implementation. State access is explicit and injected.
type RunAdoptBatchOptions struct {
	Deployment     deployment.Deployment
	InputDirectory string
	OnDiagnostic   func(string)
	Policy         *metadata.DriftPolicy
	Root           metadata.LoadedPackRoot
	Selectors      []string
	StateLoader    AdoptionStateLoader
	Tenant         string
}

type adoptionItemCounts struct {
	Eligible      int
	Fetched       int
	SystemSkipped int
	Unsupported   int
}

type preparedAdoptionItems struct {
	Counts        adoptionItemCounts
	IdentityByKey map[string]map[string]any
	KeyToImportID map[string]string
	KeyToRaw      map[string]map[string]any
	Resource      metadata.LoadedResourceMetadata
}

type adoptionPreflight struct {
	Classified bool
	Prepared   preparedAdoptionItems
	Status     string
	Blocked    AdoptionRawClassification
	Counts     adoptionItemCounts
	Resource   metadata.LoadedResourceMetadata
}

func adoptionItemLabel(item map[string]any) string {
	value, present := item["name"]
	if !present || value == nil {
		value, present = item["id"]
	}
	if !present || value == nil {
		value = "<unknown>"
	}
	return adoptionJSONValue(value)
}

func adoptionItemCountsFor(fetched int, classification AdoptionRawClassification) adoptionItemCounts {
	return adoptionItemCounts{
		Eligible:      len(classification.Eligible),
		Fetched:       fetched,
		SystemSkipped: len(classification.Skipped),
		Unsupported:   len(classification.Unsupported),
	}
}

func writeUnsupportedDiagnostics(classification AdoptionRawClassification, resource metadata.LoadedResourceMetadata, write func(string)) {
	if write == nil {
		return
	}
	for _, unsupported := range classification.Unsupported {
		write(fmt.Sprintf("unsupported %s item %s (matched static unsupported rule)", resource.Type, adoptionItemLabel(unsupported.Item)))
	}
	seen := make(map[*AdoptionUnsupportedRule]struct{})
	for _, unsupported := range classification.Unsupported {
		rule := unsupported.Rule
		if _, duplicate := seen[rule]; duplicate {
			continue
		}
		seen[rule] = struct{}{}
		evidence, _ := adoptionJSONMarshal(rule.Evidence)
		write(fmt.Sprintf("unsupported %s rule for %s %s: %s; evidence=%s", resource.Type, rule.ProviderSource, rule.ProviderVersion, rule.Reason, evidence))
	}
}

func writeAdoptionTerminalCounts(counts adoptionItemCounts, published int, resourceType string, write func(string)) {
	failed := counts.Eligible - published
	if failed < 0 {
		failed = 0
	}
	write(fmt.Sprintf(
		"adopt counts %s: fetched=%d system_skipped=%d unsupported=%d eligible=%d published=%d failed=%d",
		resourceType, counts.Fetched, counts.SystemSkipped, counts.Unsupported, counts.Eligible, published, failed,
	))
}

func prepareAdoptionItems(rawItems []any, resource metadata.LoadedResourceMetadata, write func(string)) (adoptionPreflight, error) {
	classification, err := ClassifyAdoptionRawItems(rawItems, resource)
	if err != nil {
		return adoptionPreflight{}, err
	}
	counts := adoptionItemCountsFor(len(rawItems), classification)
	for _, skipped := range classification.Skipped {
		value, present := skipped.Item["name"]
		if !present || value == nil {
			value, present = skipped.Item["id"]
		}
		if write != nil {
			label := "undefined"
			if present {
				label = adoptionJSONValue(value)
			}
			write(fmt.Sprintf("skipped %s item %s (identity %s matched)", resource.Type, label, skipped.Reason))
		}
	}
	writeUnsupportedDiagnostics(classification, resource, write)
	if len(classification.Unsupported) > 0 {
		return adoptionPreflight{Classified: true, Status: "unsupported", Blocked: classification, Counts: counts, Resource: resource}, nil
	}
	eligible := make([]any, len(classification.Eligible))
	for index, item := range classification.Eligible {
		eligible[index] = item
	}
	derived, err := DeriveAdoptionIdentities(eligible, resource)
	if err != nil {
		return adoptionPreflight{Classified: true, Status: "failed", Counts: counts, Resource: resource}, err
	}
	prepared := preparedAdoptionItems{
		Counts:        counts,
		IdentityByKey: make(map[string]map[string]any, len(derived.Identities)),
		KeyToImportID: make(map[string]string, len(derived.Identities)),
		KeyToRaw:      make(map[string]map[string]any, len(derived.Identities)),
		Resource:      resource,
	}
	for _, identity := range derived.Identities {
		prepared.IdentityByKey[identity.Key] = cloneAdoptionRecord(identity.Item)
		prepared.KeyToImportID[identity.Key] = identity.ImportID
		prepared.KeyToRaw[identity.Key] = cloneAdoptionRecord(identity.Raw)
	}
	return adoptionPreflight{Classified: true, Status: "ready", Prepared: prepared, Counts: counts, Resource: resource}, nil
}

// projectAdoptionItems verifies exact Oracle key coverage and projects each
// observed state object in deterministic key order.
func projectAdoptionItems(policy *metadata.DriftPolicy, prepared preparedAdoptionItems, root metadata.LoadedPackRoot, state map[string]OracleStateObject) (tfrender.PullTransformResult, error) {
	originals := make(map[string]map[string]any, len(prepared.IdentityByKey))
	if len(prepared.KeyToImportID) == 0 {
		return tfrender.PullTransformResult{Drops: []string{}, Items: map[string]map[string]any{}, Originals: originals}, nil
	}
	missing := make([]string, 0)
	for key := range prepared.KeyToImportID {
		if _, present := state[key]; !present {
			missing = append(missing, key)
		}
	}
	unexpected := make([]string, 0)
	for key := range state {
		if _, requested := prepared.KeyToImportID[key]; !requested {
			unexpected = append(unexpected, key)
		}
	}
	missing = canonjson.SortedStrings(missing)
	unexpected = canonjson.SortedStrings(unexpected)
	if len(missing) > 0 || len(unexpected) > 0 {
		missingText := "<none>"
		if len(missing) > 0 {
			missingText = strings.Join(missing, ", ")
		}
		unexpectedText := "<none>"
		if len(unexpected) > 0 {
			unexpectedText = strings.Join(unexpected, ", ")
		}
		return tfrender.PullTransformResult{}, fmt.Errorf("%s adoption Oracle keys did not match requested identities (missing=%s unexpected=%s)", prepared.Resource.Type, missingText, unexpectedText)
	}
	items := make(map[string]map[string]any, len(state))
	for _, key := range canonjson.SortedStrings(adoptMapKeys(state)) {
		observed := state[key]
		identity, ok := prepared.IdentityByKey[key]
		if !ok {
			continue
		}
		projected, err := ProjectProviderState(ProjectProviderStateOptions{
			Policy:          policy,
			RawItem:         prepared.KeyToRaw[key],
			ResourceType:    prepared.Resource.Type,
			Root:            &root,
			SensitiveValues: observed.SensitiveValues,
			StateValues:     observed.Values,
		})
		if err != nil {
			return tfrender.PullTransformResult{}, err
		}
		originals[key] = cloneAdoptionRecord(identity)
		items[key] = projected
	}
	return tfrender.PullTransformResult{Drops: []string{}, Items: items, Originals: originals}, nil
}

func adoptPreparedResourceItems(policy *metadata.DriftPolicy, prepared preparedAdoptionItems, root metadata.LoadedPackRoot, loader AdoptionStateLoader) (tfrender.PullTransformResult, error) {
	if len(prepared.KeyToImportID) == 0 {
		return projectAdoptionItems(policy, prepared, root, map[string]OracleStateObject{})
	}
	if loader == nil {
		return tfrender.PullTransformResult{}, fmt.Errorf("%s adoption requires a state loader", prepared.Resource.Type)
	}
	state, err := loader(AdoptionStateRequest{
		KeyToImportID: cloneStringMap(prepared.KeyToImportID),
		Policy:        policy,
		RawItems:      cloneRawItems(prepared.KeyToRaw),
		ResourceType:  prepared.Resource.Type,
	})
	if err != nil {
		return tfrender.PullTransformResult{}, err
	}
	return projectAdoptionItems(policy, prepared, root, state)
}

// AdoptResourceItems derives identity, loads provider state, and projects one
// resource without writing artifacts, porting adoptResourceItems.
func AdoptResourceItems(policy *metadata.DriftPolicy, rawItems []any, resource metadata.LoadedResourceMetadata, root metadata.LoadedPackRoot, loader AdoptionStateLoader, write func(string)) (tfrender.PullTransformResult, error) {
	if policy == nil {
		return tfrender.PullTransformResult{}, fmt.Errorf("adoption requires a drift policy")
	}
	preflight, err := prepareAdoptionItems(rawItems, resource, write)
	if err != nil {
		return tfrender.PullTransformResult{}, err
	}
	if preflight.Status == "unsupported" {
		return tfrender.PullTransformResult{}, fmt.Errorf("%s contains %d unsupported item(s); no Oracle command or artifact publication is permitted", resource.Type, preflight.Counts.Unsupported)
	}
	return adoptPreparedResourceItems(policy, preflight.Prepared, root, loader)
}

func pendingMovesPath(dep deployment.Deployment, resourceType, tenant string) (string, error) {
	paths, err := tfrender.ComputeTransformArtifactPaths(dep, resourceType, tenant)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(paths.Imports, "_imports.tf") {
		return strings.TrimSuffix(paths.Imports, "_imports.tf") + "_moves.pending.json", nil
	}
	return paths.Imports + ".moves.pending.json", nil
}

func assertNoPendingMoves(dep deployment.Deployment, resourceType, tenant string) error {
	pending, err := pendingMovesPath(dep, resourceType, tenant)
	if err != nil {
		return err
	}
	_, err = os.Lstat(pending)
	if err == nil {
		return fmt.Errorf("pending move transition for %s must be applied and acknowledged before transform or adopt can run", resourceType)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readAdoptionInput(inputDirectory string, root metadata.LoadedPackRoot, resourceType, tenant string) ([]any, string, bool, error) {
	sourceType, err := transform.TransformSourceType(root, resourceType)
	if err != nil {
		return nil, "", false, err
	}
	source := filepath.Join(inputDirectory, sourceType+".json")
	text, err := metadata.ReadOptionalUTF8(source, resourceType+" adoption input")
	if err != nil {
		return nil, source, false, err
	}
	if text == nil {
		return nil, source, false, nil
	}
	value, err := canonjson.ParseDataJSONLosslessly(*text)
	if err != nil {
		return nil, source, true, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, source, true, fmt.Errorf("%s must be a JSON LIST of items", source)
	}
	return items, source, true, nil
}

func transformArtifactOptions(
	options RunAdoptBatchOptions,
	resource metadata.LoadedResourceMetadata,
	result tfrender.PullTransformResult,
	resourceRoots map[string]string,
	write func(string),
) (tfrender.TransformArtifactCompileOptions, error) {
	references := transformrun.TransformReferenceSpecsForAdopt(options.Root, resource)
	lookupNameField, err := transformrun.TransformLookupNameFieldForAdopt(options.Root, resource, options.Deployment)
	if err != nil {
		return tfrender.TransformArtifactCompileOptions{}, err
	}
	meta, err := AdoptionMetadataFor(resource)
	if err != nil {
		return tfrender.TransformArtifactCompileOptions{}, err
	}
	return tfrender.TransformArtifactCompileOptions{
		BindingContext: transformrun.TransformBindingContextForAdopt(
			options.Deployment, options.Root, resource, resourceRoots, references,
		),
		Deployment:             options.Deployment,
		LookupNameField:        lookupNameField,
		RemoveLookupWhenAbsent: transformrun.TransformHasInferredLookupLifecycleForAdopt(options.Root, resource),
		OnDiagnostic:           write,
		Override:               map[string]any{"import_id": meta.ImportID},
		References:             references,
		ResourceType:           resource.Type,
		Result:                 result,
		Tenant:                 options.Tenant,
		VariableName:           "items",
	}, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// RunAdoptBatch executes the generic adoption batch target without resolving
// Terraform. It ports runAdoptBatch from the original implementation.
func RunAdoptBatch(options RunAdoptBatchOptions) (AdoptBatchResult, error) {
	result := AdoptBatchResult{Failed: []string{}, Processed: []string{}, Skipped: []string{}}
	if options.Policy == nil {
		return result, fmt.Errorf("adoption requires a drift policy")
	}
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return result, err
	}
	write := options.OnDiagnostic
	if write == nil {
		write = func(string) {}
	}
	selection, err := transform.SelectTransformResources(options.Root, options.Selectors)
	if err != nil {
		return result, err
	}
	for _, note := range selection.Notes {
		write(strings.TrimRight(note, " \t\r\n"))
	}
	bindingTopology, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: options.Root, Deployment: options.Deployment, Tenant: &options.Tenant, Selectors: []string{},
	})
	if err != nil {
		return result, err
	}
	for _, resourceType := range selection.ResourceTypes {
		rawItems, source, exists, readErr := readAdoptionInput(options.InputDirectory, options.Root, resourceType, options.Tenant)
		if readErr != nil {
			result.Failed = appendUnique(result.Failed, resourceType)
			write(fmt.Sprintf("error: %s: %s", resourceType, readErr))
			continue
		}
		if !exists {
			result.Skipped = append(result.Skipped, resourceType)
			write(fmt.Sprintf("skip %s (no %s)", resourceType, source))
			continue
		}
		resource, ok := options.Root.Resources[resourceType]
		if !ok {
			result.Failed = appendUnique(result.Failed, resourceType)
			write(fmt.Sprintf("error: %s: unknown resource %s", resourceType, resourceType))
			continue
		}
		if _, derived := resource.Registry["derive"].(map[string]any); derived {
			if pendingErr := assertNoPendingMoves(options.Deployment, resourceType, options.Tenant); pendingErr != nil {
				result.Failed = appendUnique(result.Failed, resourceType)
				write(fmt.Sprintf("error: %s: %s", resourceType, pendingErr))
				continue
			}
			delegated, delegatedErr := transformrun.RunTransformBatch(transformrun.RunTransformBatchOptions{
				BeforeArtifactWrite: func(selectedResourceType string) error {
					return assertNoPendingMoves(options.Deployment, selectedResourceType, options.Tenant)
				},
				Deployment: options.Deployment, InputDirectory: options.InputDirectory,
				OnDiagnostic: write, Root: options.Root, Selectors: []string{resourceType}, Tenant: options.Tenant,
			})
			if delegatedErr != nil {
				result.Failed = appendUnique(result.Failed, resourceType)
				write(fmt.Sprintf("error: %s: %s", resourceType, delegatedErr))
			} else if len(delegated.Failed) > 0 {
				result.Failed = appendUnique(result.Failed, resourceType)
			} else if len(delegated.Skipped) > 0 {
				result.Skipped = append(result.Skipped, resourceType)
			} else {
				result.Processed = append(result.Processed, resourceType)
			}
			continue
		}
		preflight, prepErr := prepareAdoptionItems(rawItems, resource, write)
		if prepErr != nil {
			result.Failed = appendUnique(result.Failed, resourceType)
			write(fmt.Sprintf("error: %s: %s", resourceType, prepErr))
			if preflight.Classified {
				writeAdoptionTerminalCounts(preflight.Counts, 0, resourceType, write)
			}
			continue
		}
		published := 0
		resourceSucceeded := false
		if preflight.Status == "unsupported" {
			result.Failed = appendUnique(result.Failed, resourceType)
		} else if pendingErr := assertNoPendingMoves(options.Deployment, resourceType, options.Tenant); pendingErr != nil {
			result.Failed = appendUnique(result.Failed, resourceType)
			write(fmt.Sprintf("error: %s: %s", resourceType, pendingErr))
		} else {
			projected, adoptErr := adoptPreparedResourceItems(options.Policy, preflight.Prepared, options.Root, options.StateLoader)
			if adoptErr == nil {
				adoptErr = assertNoPendingMoves(options.Deployment, resourceType, options.Tenant)
			}
			if adoptErr == nil {
				artifact, artifactErr := transformArtifactOptions(options, resource, projected, bindingTopology.Topology.ResourceRoots, write)
				if artifactErr == nil {
					_, artifactErr = tfrender.WriteTransformArtifacts(artifact)
				}
				adoptErr = artifactErr
			}
			if adoptErr != nil {
				result.Failed = appendUnique(result.Failed, resourceType)
				write(fmt.Sprintf("error: %s: %s", resourceType, adoptErr))
			} else {
				published = len(projected.Items)
				resourceSucceeded = true
				result.Processed = append(result.Processed, resourceType)
			}
		}
		if preflight.Status == "ready" || preflight.Status == "unsupported" {
			if !resourceSucceeded {
				published = 0
			}
			writeAdoptionTerminalCounts(preflight.Counts, published, resourceType, write)
		}
	}
	if len(result.Failed) > 0 {
		write("\nadopt FAILED for: " + strings.Join(result.Failed, " "))
	}
	return result, nil
}

package adopt

import (
	"encoding/json"
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
// node-src/domain/adopt-runner.ts.
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

// AdoptionBatchStateLoader ports AdoptionBatchStateLoader.
type AdoptionBatchStateLoader func([]OracleBatchResourceRequest) (OracleBatchState, error)

// OracleBatchMode ports OracleBatchMode from node-src/domain/adopt-runner.ts.
type OracleBatchMode string

const (
	OracleBatchPerResourceType OracleBatchMode = "per-resource-type"
	OracleBatchLogicalRoot     OracleBatchMode = "logical-root"
)

// OracleBatchModeFromEnvironment ports oracleBatchMode.
func OracleBatchModeFromEnvironment(environment map[string]string) (OracleBatchMode, error) {
	raw := strings.TrimSpace(environment["INFRAWRIGHT_ORACLE_BATCH_MODE"])
	switch raw {
	case "", string(OracleBatchPerResourceType):
		return OracleBatchPerResourceType, nil
	case string(OracleBatchLogicalRoot):
		return OracleBatchLogicalRoot, nil
	default:
		return "", fmt.Errorf("INFRAWRIGHT_ORACLE_BATCH_MODE must be per-resource-type or logical-root")
	}
}

// RunAdoptBatchOptions ports RunAdoptBatchOptions from
// node-src/domain/adopt-runner.ts. State access is explicit and injected.
type RunAdoptBatchOptions struct {
	BatchStateLoader AdoptionBatchStateLoader
	Deployment       deployment.Deployment
	Environment      map[string]string
	InputDirectory   string
	OnDiagnostic     func(string)
	Policy           *metadata.DriftPolicy
	Root             metadata.LoadedPackRoot
	Selectors        []string
	StateLoader      AdoptionStateLoader
	Tenant           string
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
		evidence, _ := json.Marshal(rule.Evidence)
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
		value, ok := skipped.Item["name"]
		if !ok || value == nil {
			value = skipped.Item["id"]
		}
		if write != nil {
			write(fmt.Sprintf("skipped %s item %s (identity %s matched)", resource.Type, adoptionJSONValue(value), skipped.Reason))
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

func variableNameForAdoption(resourceType string, resourceRoots map[string]string) string {
	root := resourceRoots[resourceType]
	if root == "" || root == resourceType {
		return "items"
	}
	return resourceType + "_items"
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
	variableRoots map[string]string,
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
		VariableName:           variableNameForAdoption(resource.Type, variableRoots),
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

type logicalPreparedResource struct {
	Adoption     preparedAdoptionItems
	Resource     metadata.LoadedResourceMetadata
	ResourceType string
}

// RunAdoptBatch executes the generic adoption batch target without resolving
// Terraform. It ports runAdoptBatch from node-src/domain/adopt-runner.ts.
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
	selectedTopology, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: options.Root, Deployment: options.Deployment, Tenant: &options.Tenant,
		Selectors: append([]string(nil), selection.ResourceTypes...),
	})
	if err != nil {
		return result, err
	}
	for _, diagnostic := range selectedTopology.Diagnostics {
		write("NOTE: " + diagnostic.Message)
	}
	bindingTopology, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: options.Root, Deployment: options.Deployment, Tenant: &options.Tenant, Selectors: []string{},
	})
	if err != nil {
		return result, err
	}
	environment := options.Environment
	if environment == nil {
		environment = processEnvironment()
	} else {
		environment = cloneStringMap(environment)
	}
	mode, err := OracleBatchModeFromEnvironment(environment)
	if err != nil {
		return result, err
	}
	operationOrder := selection
	if mode == OracleBatchLogicalRoot {
		members := make([]string, 0)
		for _, logicalRoot := range selectedTopology.Topology.Roots {
			members = append(members, logicalRoot.Members...)
		}
		operationOrder, err = transform.ReferenceOrder(options.Root, members)
		if err != nil {
			return result, err
		}
		for _, note := range operationOrder.Notes {
			write(strings.TrimRight(note, " \t\r\n"))
		}
	}
	selected := make(map[string]struct{}, len(operationOrder.ResourceTypes))
	selectionIndex := make(map[string]int, len(operationOrder.ResourceTypes))
	for index, resourceType := range operationOrder.ResourceTypes {
		selected[resourceType] = struct{}{}
		selectionIndex[resourceType] = index
	}
	handled := make(map[string]struct{})
	disabledBatchRoots := make(map[string]struct{})
	topologyRootByMember := make(map[string]roots.RootTopologyRoot)
	for _, logicalRoot := range selectedTopology.Topology.Roots {
		for _, member := range logicalRoot.Members {
			topologyRootByMember[member] = logicalRoot
		}
	}

	tryLogicalRootBatch := func(trigger string) (bool, error) {
		if mode != OracleBatchLogicalRoot {
			return false, nil
		}
		logicalRoot, found := topologyRootByMember[trigger]
		if !found {
			return false, nil
		}
		if _, disabled := disabledBatchRoots[logicalRoot.Label]; disabled {
			return false, nil
		}
		orderedRoot, err := transform.ReferenceOrder(options.Root, logicalRoot.Members)
		if err != nil {
			return false, err
		}
		for _, note := range orderedRoot.Notes {
			write(strings.TrimRight(note, " \t\r\n"))
		}
		candidates := make([]string, 0, len(orderedRoot.ResourceTypes))
		for _, member := range orderedRoot.ResourceTypes {
			resource, ok := options.Root.Resources[member]
			if !ok {
				continue
			}
			if _, derived := resource.Registry["derive"].(map[string]any); !derived {
				candidates = append(candidates, member)
			}
		}
		if len(candidates) < 2 {
			disabledBatchRoots[logicalRoot.Label] = struct{}{}
			return false, nil
		}
		prepared := make([]logicalPreparedResource, 0, len(candidates))
		countsByResource := make(map[string]adoptionItemCounts)
		missingResources := make(map[string]struct{})
		preflightDiagnostics := make([]string, 0)
		skippedBefore := len(result.Skipped)
		failedBefore := len(result.Failed)
		preflightFailed := false
		unsupportedRoot := false
		for _, resourceType := range candidates {
			handled[resourceType] = struct{}{}
			rawItems, source, exists, readErr := readAdoptionInput(options.InputDirectory, options.Root, resourceType, options.Tenant)
			if readErr != nil {
				preflightFailed = true
				result.Failed = appendUnique(result.Failed, resourceType)
				preflightDiagnostics = append(preflightDiagnostics, fmt.Sprintf("error: %s: %s", resourceType, readErr))
				continue
			}
			if !exists {
				missingResources[resourceType] = struct{}{}
				countsByResource[resourceType] = adoptionItemCounts{}
				result.Skipped = append(result.Skipped, resourceType)
				preflightDiagnostics = append(preflightDiagnostics, fmt.Sprintf("skip %s (no %s)", resourceType, source))
				continue
			}
			resource, ok := options.Root.Resources[resourceType]
			if !ok {
				preflightFailed = true
				result.Failed = appendUnique(result.Failed, resourceType)
				preflightDiagnostics = append(preflightDiagnostics, fmt.Sprintf("error: %s: unknown resource %s", resourceType, resourceType))
				continue
			}
			if logicalRoot.Provider == nil || resource.Provider != *logicalRoot.Provider {
				provider := "<nil>"
				if logicalRoot.Provider != nil {
					provider = *logicalRoot.Provider
				}
				preflightFailed = true
				result.Failed = appendUnique(result.Failed, resourceType)
				preflightDiagnostics = append(preflightDiagnostics, fmt.Sprintf("error: %s: logical root %s mixes provider %s with %s", resourceType, logicalRoot.Label, provider, resource.Provider))
				continue
			}
			preflight, prepErr := prepareAdoptionItems(rawItems, resource, func(message string) {
				preflightDiagnostics = append(preflightDiagnostics, message)
			})
			if preflight.Classified {
				countsByResource[resourceType] = preflight.Counts
			}
			if prepErr != nil {
				preflightFailed = true
				result.Failed = appendUnique(result.Failed, resourceType)
				preflightDiagnostics = append(preflightDiagnostics, fmt.Sprintf("error: %s: %s", resourceType, prepErr))
				continue
			}
			if preflight.Status == "unsupported" {
				unsupportedRoot = true
				continue
			}
			prepared = append(prepared, logicalPreparedResource{Adoption: preflight.Prepared, Resource: resource, ResourceType: resourceType})
		}
		finishCounts := func(published map[string]int) {
			for _, resourceType := range candidates {
				counts, present := countsByResource[resourceType]
				if !present {
					continue
				}
				writeAdoptionTerminalCounts(counts, published[resourceType], resourceType, write)
			}
		}
		flush := func() {
			for _, diagnostic := range preflightDiagnostics {
				write(diagnostic)
			}
		}
		failPreparedRoot := func() {
			for _, resourceType := range candidates {
				if _, missing := missingResources[resourceType]; !missing {
					result.Failed = appendUnique(result.Failed, resourceType)
				}
			}
			flush()
			finishCounts(map[string]int{})
		}
		if preflightFailed || unsupportedRoot {
			failPreparedRoot()
			return true, nil
		}
		for _, entry := range prepared {
			if pendingErr := assertNoPendingMoves(options.Deployment, entry.ResourceType, options.Tenant); pendingErr != nil {
				preflightFailed = true
				result.Failed = appendUnique(result.Failed, entry.ResourceType)
				preflightDiagnostics = append(preflightDiagnostics, fmt.Sprintf("error: %s: %s", entry.ResourceType, pendingErr))
			}
		}
		if preflightFailed {
			failPreparedRoot()
			return true, nil
		}
		candidateSet := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			candidateSet[candidate] = struct{}{}
		}
		triggerIndex, hasTriggerIndex := selectionIndex[trigger]
		if !hasTriggerIndex {
			return false, fmt.Errorf("selected resource %s has no reference-order position", trigger)
		}
		hasPendingExternalReferent := false
		for _, resourceType := range candidates {
			resource := options.Root.Resources[resourceType]
			for _, reference := range transformrun.TransformReferenceSpecsForAdopt(options.Root, resource) {
				if _, isSelected := selected[reference.Referent]; !isSelected {
					continue
				}
				if _, inRoot := candidateSet[reference.Referent]; inRoot {
					continue
				}
				_, alreadyHandled := handled[reference.Referent]
				referentIndex, indexed := selectionIndex[reference.Referent]
				if !alreadyHandled && (!indexed || referentIndex >= triggerIndex) {
					hasPendingExternalReferent = true
					break
				}
			}
			if hasPendingExternalReferent {
				break
			}
		}
		if hasPendingExternalReferent {
			result.Skipped = result.Skipped[:skippedBefore]
			result.Failed = result.Failed[:failedBefore]
			for _, candidate := range candidates {
				delete(handled, candidate)
			}
			disabledBatchRoots[logicalRoot.Label] = struct{}{}
			return false, nil
		}
		flush()
		if len(prepared) == 0 {
			finishCounts(map[string]int{})
			return true, nil
		}
		if options.BatchStateLoader == nil {
			for _, entry := range prepared {
				result.Failed = appendUnique(result.Failed, entry.ResourceType)
			}
			write(fmt.Sprintf("error: logical root %s: logical-root Oracle batching was requested but no batch state loader was configured", logicalRoot.Label))
			finishCounts(map[string]int{})
			return true, nil
		}
		requests := make([]OracleBatchResourceRequest, len(prepared))
		for index, entry := range prepared {
			requests[index] = OracleBatchResourceRequest{
				KeyToImportID: cloneStringMap(entry.Adoption.KeyToImportID),
				Policy:        options.Policy,
				RawItems:      cloneRawItems(entry.Adoption.KeyToRaw),
				ResourceType:  entry.ResourceType,
			}
		}
		stateByResource, batchErr := options.BatchStateLoader(requests)
		if batchErr != nil {
			isolatedFailures := 0
			for _, request := range requests {
				if options.StateLoader == nil {
					isolatedFailures++
					write(fmt.Sprintf("error: %s: adoption requires a state loader", request.ResourceType))
					continue
				}
				_, memberErr := options.StateLoader(AdoptionStateRequest{
					KeyToImportID: cloneStringMap(request.KeyToImportID), Policy: request.Policy,
					RawItems: cloneRawItems(request.RawItems), ResourceType: request.ResourceType,
				})
				if memberErr != nil {
					isolatedFailures++
					write(fmt.Sprintf("error: %s: %s", request.ResourceType, memberErr))
				}
			}
			for _, entry := range prepared {
				result.Failed = appendUnique(result.Failed, entry.ResourceType)
			}
			if isolatedFailures == 0 {
				write(fmt.Sprintf("error: logical root %s: batched Oracle failed after every member succeeded independently: %s", logicalRoot.Label, batchErr))
			} else {
				write(fmt.Sprintf("error: logical root %s: batched Oracle failed; %d member failure(s) identified above: %s", logicalRoot.Label, isolatedFailures, batchErr))
			}
			finishCounts(map[string]int{})
			return true, nil
		}
		expectedTypes := make(map[string]struct{}, len(prepared))
		for _, entry := range prepared {
			expectedTypes[entry.ResourceType] = struct{}{}
		}
		unexpectedTypes := make([]string, 0)
		for resourceType := range stateByResource {
			if _, expected := expectedTypes[resourceType]; !expected {
				unexpectedTypes = append(unexpectedTypes, resourceType)
			}
		}
		if len(unexpectedTypes) > 0 {
			for _, entry := range prepared {
				result.Failed = appendUnique(result.Failed, entry.ResourceType)
			}
			write(fmt.Sprintf("error: logical root %s: logical-root Oracle result %s contained unexpected resources %s", logicalRoot.Label, logicalRoot.Label, strings.Join(canonjson.SortedStrings(unexpectedTypes), ", ")))
			finishCounts(map[string]int{})
			return true, nil
		}
		artifactOptions := make([]tfrender.TransformArtifactCompileOptions, 0, len(prepared))
		published := make(map[string]int, len(prepared))
		for _, entry := range prepared {
			state, present := stateByResource[entry.ResourceType]
			if !present {
				for _, failedEntry := range prepared {
					result.Failed = appendUnique(result.Failed, failedEntry.ResourceType)
				}
				write(fmt.Sprintf("error: logical root %s: %s missing from logical-root Oracle result %s", logicalRoot.Label, entry.ResourceType, logicalRoot.Label))
				finishCounts(map[string]int{})
				return true, nil
			}
			projected, projectErr := projectAdoptionItems(options.Policy, entry.Adoption, options.Root, state)
			if projectErr != nil {
				for _, failedEntry := range prepared {
					result.Failed = appendUnique(result.Failed, failedEntry.ResourceType)
				}
				write(fmt.Sprintf("error: logical root %s: %s", logicalRoot.Label, projectErr))
				finishCounts(map[string]int{})
				return true, nil
			}
			if pendingErr := assertNoPendingMoves(options.Deployment, entry.ResourceType, options.Tenant); pendingErr != nil {
				for _, failedEntry := range prepared {
					result.Failed = appendUnique(result.Failed, failedEntry.ResourceType)
				}
				write(fmt.Sprintf("error: logical root %s: %s", logicalRoot.Label, pendingErr))
				finishCounts(map[string]int{})
				return true, nil
			}
			artifact, artifactErr := transformArtifactOptions(options, entry.Resource, projected, bindingTopology.Topology.ResourceRoots, selectedTopology.Topology.ResourceRoots, write)
			if artifactErr != nil {
				for _, failedEntry := range prepared {
					result.Failed = appendUnique(result.Failed, failedEntry.ResourceType)
				}
				write(fmt.Sprintf("error: logical root %s: %s", logicalRoot.Label, artifactErr))
				finishCounts(map[string]int{})
				return true, nil
			}
			artifactOptions = append(artifactOptions, artifact)
			published[entry.ResourceType] = len(projected.Items)
		}
		compiled, compileErr := tfrender.CompileTransformArtifactBatch(artifactOptions)
		if compileErr == nil {
			for _, entry := range prepared {
				if pendingErr := assertNoPendingMoves(options.Deployment, entry.ResourceType, options.Tenant); pendingErr != nil {
					compileErr = pendingErr
					break
				}
			}
		}
		if compileErr == nil {
			_, compileErr = tfrender.PublishCompiledTransformArtifactBatch(compiled)
		}
		if compileErr != nil {
			for _, entry := range prepared {
				result.Failed = appendUnique(result.Failed, entry.ResourceType)
			}
			write(fmt.Sprintf("error: logical root %s: %s", logicalRoot.Label, compileErr))
			finishCounts(map[string]int{})
			return true, nil
		}
		for _, entry := range prepared {
			result.Processed = append(result.Processed, entry.ResourceType)
		}
		finishCounts(published)
		return true, nil
	}

	for _, resourceType := range operationOrder.ResourceTypes {
		if _, done := handled[resourceType]; done {
			continue
		}
		batched, batchErr := tryLogicalRootBatch(resourceType)
		if batchErr != nil {
			return result, batchErr
		}
		if batched {
			continue
		}
		if _, done := handled[resourceType]; done {
			continue
		}
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
				artifact, artifactErr := transformArtifactOptions(options, resource, projected, bindingTopology.Topology.ResourceRoots, selectedTopology.Topology.ResourceRoots, write)
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

package adopt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// ImportProviderStatesOptions is the Go options bag for
// importProviderStates in node-src/domain/import-oracle.ts. A nil Environment
// snapshots the current process environment at the call boundary.
type ImportProviderStatesOptions struct {
	Environment   map[string]string
	KeepWorkdir   bool
	OnDiagnostic  func(string)
	Resources     []OracleBatchResourceRequest
	Root          *metadata.LoadedPackRoot
	Runner        OracleCommandRunner
	TemporaryRoot string
}

func processEnvironment() map[string]string {
	result := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func snapshotOracleResources(input []OracleBatchResourceRequest) []OracleBatchResourceRequest {
	output := make([]OracleBatchResourceRequest, len(input))
	for index, resource := range input {
		output[index] = OracleBatchResourceRequest{
			KeyToImportID: cloneStringMap(resource.KeyToImportID),
			Policy:        resource.Policy,
			RawItems:      cloneRawItems(resource.RawItems),
			ResourceType:  resource.ResourceType,
		}
	}
	sort.Slice(output, func(i, j int) bool { return output[i].ResourceType < output[j].ResourceType })
	return output
}

func cloneRawItems(input map[string]map[string]any) map[string]map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]map[string]any, len(input))
	for key, item := range input {
		copyItem := make(map[string]any, len(item))
		for field, value := range item {
			copyItem[field] = value
		}
		output[key] = copyItem
	}
	return output
}

func normalTerraformFailure(err error) bool {
	var failure *procerr.ProcessFailure
	return errors.As(err, &failure) && failure.Code == "TERRAFORM_COMMAND_FAILED"
}

// ImportProviderStates ports importProviderStates from
// node-src/domain/import-oracle.ts. Terraform Apply, when selected, targets
// only this transaction's ephemeral local scratch state. Tests inject a fake
// runner and cannot reach any Terraform executable.
func ImportProviderStates(options ImportProviderStatesOptions) (state OracleBatchState, err error) {
	resources := snapshotOracleResources(options.Resources)
	seenTypes := make(map[string]struct{}, len(resources))
	active := make([]OracleBatchResourceRequest, 0, len(resources))
	for _, resource := range resources {
		if _, duplicate := seenTypes[resource.ResourceType]; duplicate {
			return nil, oracleErrorf("duplicate oracle batch resource type %s", resource.ResourceType)
		}
		seenTypes[resource.ResourceType] = struct{}{}
		if len(resource.KeyToImportID) > 0 {
			active = append(active, resource)
		}
	}
	if len(active) == 0 {
		output := make(OracleBatchState, len(resources))
		for _, resource := range resources {
			output[resource.ResourceType] = map[string]OracleStateObject{}
		}
		return output, nil
	}
	if options.Root == nil || options.Runner == nil {
		return nil, oracleErrorf("oracle transaction requires pack root and command runner")
	}
	environment := options.Environment
	if environment == nil {
		environment = processEnvironment()
	} else {
		environment = cloneStringMap(environment)
	}
	stateSource, err := OracleStateSourceFromEnvironment(environment)
	if err != nil {
		return nil, err
	}
	for _, resource := range active {
		if resource.Policy != nil && len(resource.Policy.Entries(resource.ResourceType, metadata.PolicyProjectionFill)) > 0 && resource.RawItems == nil {
			return nil, oracleErrorf("%s projection_fill requires raw_items", resource.ResourceType)
		}
		byImportID := make(map[string]string, len(resource.KeyToImportID))
		keys := canonjson.SortedStrings(mapKeys(resource.KeyToImportID))
		for _, key := range keys {
			importID := resource.KeyToImportID[key]
			if prior, duplicate := byImportID[importID]; duplicate {
				return nil, oracleErrorf("%s duplicate import_id for keys %s and %s", resource.ResourceType, jsonString(prior), jsonString(key))
			}
			byImportID[importID] = key
		}
		if err := checkAddressCollisions(resource.ResourceType, keys); err != nil {
			return nil, err
		}
	}

	providers := make(map[string]struct{})
	for _, request := range active {
		resource, ok := options.Root.Resources[request.ResourceType]
		if !ok {
			return nil, oracleErrorf("unknown active resource %s", request.ResourceType)
		}
		providers[resource.Provider] = struct{}{}
	}
	providerNames := canonjson.SortedStrings(mapKeys(providers))
	if len(providerNames) != 1 {
		return nil, oracleErrorf("oracle batch must contain exactly one provider, found %s", strings.Join(providerNames, ", "))
	}
	provider := providerNames[0]
	expectedProviderName := providerName(options.Root.Packs.ProviderSources[provider])
	expected := make(map[string]expectedOracleInstance)
	for _, request := range active {
		for _, key := range canonjson.SortedStrings(mapKeys(request.KeyToImportID)) {
			address := OracleAddress(request.ResourceType, key)
			if _, collision := expected[address]; collision {
				return nil, oracleErrorf("oracle batch address collision at %s", address)
			}
			expected[address] = expectedOracleInstance{
				ImportID:     request.KeyToImportID[key],
				Key:          key,
				ProviderName: expectedProviderName,
				ResourceType: request.ResourceType,
			}
		}
	}

	keep := options.KeepWorkdir || truthy(environment["INFRAWRIGHT_KEEP_ORACLE"])
	temporaryRoot := options.TemporaryRoot
	if temporaryRoot == "" {
		temporaryRoot = os.TempDir()
	}
	temporary, err := os.MkdirTemp(temporaryRoot, "infrawright-oracle-")
	if err != nil {
		return nil, fmt.Errorf("create oracle workdir: %w", err)
	}
	primaryFailed := false
	defer func() {
		if keep {
			if options.OnDiagnostic != nil {
				options.OnDiagnostic("WARNING: kept oracle workdir " + temporary + "; it may contain unencrypted provider state, generated configuration, raw API pull values, credentials, import IDs, and provider diagnostics. Remove it when debugging is complete.")
			}
			return
		}
		// D1 retains the Node oracle's arbitrary-tree scratch cleanup. The
		// descriptor-bound reusable cleanup required by the Block D guardrail is
		// implemented for exact-plan-apply's closed saved-plan snapshot in D4;
		// it cannot safely scrub Terraform's arbitrary plugin/cache tree here.
		cleanupErr := os.RemoveAll(temporary)
		if cleanupErr == nil {
			return
		}
		if primaryFailed {
			if options.OnDiagnostic != nil {
				options.OnDiagnostic("WARNING: failed to remove oracle workdir " + temporary + " after an error")
			}
			return
		}
		err = oracleErrorf("failed to remove oracle workdir %s", temporary)
		state = nil
	}()
	fail := func(cause error) (OracleBatchState, error) {
		primaryFailed = true
		return nil, cause
	}

	rootText, err := RenderOracleRoot(options.Root, provider)
	if err != nil {
		return fail(err)
	}
	importsText, err := renderOracleBatchImports(active)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(temporary, "main.tf"), []byte(rootText), 0o600); err != nil {
		return fail(fmt.Errorf("write oracle main configuration: %w", err))
	}
	if err := os.WriteFile(filepath.Join(temporary, "imports.tf"), []byte(importsText), 0o600); err != nil {
		return fail(fmt.Errorf("write oracle imports configuration: %w", err))
	}
	childEnvironment := cloneStringMap(environment)
	childEnvironment["TF_DATA_DIR"] = filepath.Join(temporary, ".terraform")
	sensitiveTokens := make([]string, 0, len(expected))
	for _, instance := range expected {
		sensitiveTokens = append(sensitiveTokens, instance.ImportID)
	}
	sort.Strings(sensitiveTokens)
	run := func(argv []string, debugName string, capture bool) ([]byte, error) {
		result, runErr := options.Runner.Run(OracleCommandRequest{
			Argv:            append([]string(nil), argv...),
			CWD:             temporary,
			DebugName:       debugName,
			Environment:     cloneStringMap(childEnvironment),
			CaptureOutput:   capture,
			SensitiveTokens: append([]string(nil), sensitiveTokens...),
		})
		return result.Stdout, runErr
	}
	if _, err := run([]string{"init", "-input=false", "-no-color"}, "init", false); err != nil {
		return fail(err)
	}
	planPath := filepath.Join(temporary, "oracle.tfplan")
	generatedPath := filepath.Join(temporary, "generated.tf")
	var generateFailure error
	if _, planErr := run([]string{"plan", "-input=false", "-no-color", "-lock=false", "-generate-config-out=" + generatedPath, "-out=" + planPath}, "plan-generate-config", false); planErr != nil {
		if !normalTerraformFailure(planErr) || !regularOrExistingFile(generatedPath) {
			return fail(planErr)
		}
		generateFailure = planErr
	}
	original, readErr := os.ReadFile(generatedPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fail(fmt.Errorf("read generated import config: %w", readErr))
	}
	policyResources := make([]GeneratedConfigPolicyResource, 0, len(active))
	for _, resource := range active {
		addresses := make(map[string]string)
		for address, instance := range expected {
			if instance.ResourceType == resource.ResourceType {
				addresses[address] = instance.Key
			}
		}
		policyResources = append(policyResources, GeneratedConfigPolicyResource{
			AddressToKey: addresses,
			Policy:       resource.Policy,
			RawItems:     resource.RawItems,
			ResourceType: resource.ResourceType,
		})
	}
	applied, err := ApplyGeneratedConfigPolicies(ApplyGeneratedConfigPoliciesOptions{
		GeneratedConfig: string(original), Resources: policyResources, Root: options.Root,
	})
	if err != nil {
		return fail(err)
	}
	if applied.Edits > 0 {
		if keep {
			if err := os.WriteFile(filepath.Join(temporary, "generated.tf.before-policy"), original, 0o600); err != nil {
				return fail(fmt.Errorf("write oracle generated-config backup: %w", err))
			}
		}
		if err := os.WriteFile(generatedPath, []byte(applied.Text), 0o600); err != nil {
			return fail(fmt.Errorf("write oracle generated config: %w", err))
		}
	}
	if generateFailure != nil && applied.Edits == 0 {
		return fail(generateFailure)
	}
	if generateFailure != nil || applied.Edits > 0 {
		if _, err := run([]string{"plan", "-input=false", "-no-color", "-lock=false", "-out=" + planPath}, "plan-imports", false); err != nil {
			return fail(err)
		}
	}
	planBytes, err := run([]string{"show", "-json", planPath}, "show-plan", true)
	if err != nil {
		return fail(err)
	}
	typedPlan, rawPlan, err := DecodeOraclePlan(planBytes)
	if err != nil {
		return fail(err)
	}
	decoded := decodedPlan{Typed: typedPlan, Raw: rawPlan}
	if stateSource == OracleAcceptedPlan {
		output, extractErr := extractAcceptedBatchPlanState(decoded, expected)
		if extractErr != nil {
			return fail(extractErr)
		}
		return fillEmptyBatchResources(resources, output), nil
	}
	if err := assertImportOnlyBatchPlan(decoded, expected); err != nil {
		return fail(err)
	}
	if _, err := run([]string{"apply", "-input=false", "-no-color", "-lock=false", planPath}, "apply-imports", false); err != nil {
		return fail(err)
	}
	stateBytes, err := run([]string{"show", "-json", "terraform.tfstate"}, "show-state", true)
	if err != nil {
		return fail(err)
	}
	_, rawState, err := DecodeOracleState(stateBytes)
	if err != nil {
		return fail(err)
	}
	output, err := exactBatchStateObjects(rawState, expected)
	if err != nil {
		return fail(err)
	}
	return fillEmptyBatchResources(resources, output), nil
}

func regularOrExistingFile(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fillEmptyBatchResources(resources []OracleBatchResourceRequest, output OracleBatchState) OracleBatchState {
	result := make(OracleBatchState, len(resources))
	for _, resource := range resources {
		items := output[resource.ResourceType]
		if items == nil {
			items = map[string]OracleStateObject{}
		}
		result[resource.ResourceType] = items
	}
	return result
}

// ImportProviderStateOptions is the single-resource options bag from
// node-src/domain/import-oracle.ts.
type ImportProviderStateOptions struct {
	Environment   map[string]string
	KeepWorkdir   bool
	KeyToImportID map[string]string
	OnDiagnostic  func(string)
	Policy        *metadata.DriftPolicy
	RawItems      map[string]map[string]any
	ResourceType  string
	Root          *metadata.LoadedPackRoot
	Runner        OracleCommandRunner
	TemporaryRoot string
}

// ImportProviderState ports importProviderState from
// node-src/domain/import-oracle.ts.
func ImportProviderState(options ImportProviderStateOptions) (map[string]OracleStateObject, error) {
	output, err := ImportProviderStates(ImportProviderStatesOptions{
		Environment: options.Environment, KeepWorkdir: options.KeepWorkdir,
		OnDiagnostic: options.OnDiagnostic,
		Resources:    []OracleBatchResourceRequest{{KeyToImportID: options.KeyToImportID, Policy: options.Policy, RawItems: options.RawItems, ResourceType: options.ResourceType}},
		Root:         options.Root, Runner: options.Runner, TemporaryRoot: options.TemporaryRoot,
	})
	if err != nil {
		return nil, err
	}
	return output[options.ResourceType], nil
}

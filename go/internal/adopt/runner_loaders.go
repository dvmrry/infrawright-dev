package adopt

import (
	"errors"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// DefaultAdoptionLoaderOptions is the explicit construction boundary for the
// D1 oracle-backed D3 state loaders. Constructing a loader launches nothing;
// execution happens only when its returned function is invoked.
type DefaultAdoptionLoaderOptions struct {
	Environment         map[string]string
	KeepOracleWorkdir   bool
	OnDiagnostic        func(string)
	Root                metadata.LoadedPackRoot
	TemporaryRoot       string
	TerraformExecutable string
}

// DefaultAdoptionStateLoader ports defaultAdoptionStateLoader from
// node-src/domain/adopt-runner.ts by layering the single-resource view over
// D1's shared batch oracle transaction. It validates Oracle timeout input
// before returning a callable loader, matching the source constructor.
func DefaultAdoptionStateLoader(options DefaultAdoptionLoaderOptions) (AdoptionStateLoader, error) {
	if options.Environment == nil {
		return nil, errors.New("default adoption state loader requires an explicit environment")
	}
	environment := cloneStringMap(options.Environment)
	if _, err := OracleTimeoutMS(environment); err != nil {
		return nil, err
	}
	runner := CreateOracleCommandRunner(options.TerraformExecutable)
	root := options.Root
	return func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
		state, err := ImportProviderStates(ImportProviderStatesOptions{
			Environment:  cloneStringMap(environment),
			KeepWorkdir:  options.KeepOracleWorkdir,
			OnDiagnostic: options.OnDiagnostic,
			Resources: []OracleBatchResourceRequest{{
				KeyToImportID: cloneStringMap(request.KeyToImportID),
				Policy:        request.Policy,
				RawItems:      cloneRawItems(request.RawItems),
				ResourceType:  request.ResourceType,
			}},
			Root:          &root,
			Runner:        runner,
			TemporaryRoot: options.TemporaryRoot,
		})
		if err != nil {
			return nil, err
		}
		return state[request.ResourceType], nil
	}, nil
}

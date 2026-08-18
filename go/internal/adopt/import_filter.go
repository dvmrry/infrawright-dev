package adopt

import (
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// FilteredGeneratedImports ports the FilteredGeneratedImports interface from
// the original implementation.
type FilteredGeneratedImports struct {
	Text    string
	Kept    int
	Skipped int
}

// stagedImportAddress renders the module-scoped state address a generated
// import pair adopts into. RenderHclQuotedString matches the quoting the
// rendered import block carries in its `to =` line, which is what the
// pre-parse filter compared against terraform state list output.
func stagedImportAddress(resourceType, key string) (string, error) {
	quoted, err := tfrender.RenderHclQuotedString(key)
	if err != nil {
		return "", err
	}
	return "module." + resourceType + "." + resourceType + ".this[" + quoted + "]", nil
}

// FilterGeneratedImports filters a generated import file down to the pairs
// whose addresses are not already managed. The input must be a complete
// canonical Infrawright-generated import file: it is parsed with
// tfrender.ParseGeneratedImports and the survivors are re-rendered with
// tfrender.RenderGeneratedImports, so the filtered artifact round-trips
// through the same parser that later reads it back (stagedImportTargets
// derives the imports-only -target set from the staged file). The previous
// implementation spliced source text and left the separators of skipped
// blocks behind -- output the strict parser then refused. Anything other
// than a canonical import file is refused here, at staging time, instead of
// being passed through to fail at plan time.
func FilterGeneratedImports(resourceType, importsText string, stateAddresses []string) (FilteredGeneratedImports, error) {
	pairs, err := tfrender.ParseGeneratedImports(resourceType, importsText)
	if err != nil {
		return FilteredGeneratedImports{}, err
	}
	managed := make(map[string]struct{}, len(stateAddresses))
	for _, address := range stateAddresses {
		managed[address] = struct{}{}
	}
	kept := make([]tfrender.GeneratedImportPair, 0, len(pairs))
	skipped := 0
	for _, pair := range pairs {
		address, err := stagedImportAddress(resourceType, pair.Key)
		if err != nil {
			return FilteredGeneratedImports{}, err
		}
		if _, alreadyManaged := managed[address]; alreadyManaged {
			skipped++
			continue
		}
		kept = append(kept, pair)
	}
	if len(kept) == 0 {
		return FilteredGeneratedImports{Skipped: skipped}, nil
	}
	text, err := tfrender.RenderGeneratedImports(resourceType, kept)
	if err != nil {
		return FilteredGeneratedImports{}, err
	}
	return FilteredGeneratedImports{Text: text, Kept: len(kept), Skipped: skipped}, nil
}

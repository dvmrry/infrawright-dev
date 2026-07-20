package sourcebind

import (
	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

func captureOpenAPI(root string, binding *contracts.OpenAPIInputBinding, budget *artifacts.ReadBudget, options artifacts.StableReadOptions) OpenAPIStatus {
	if binding == nil {
		return OpenAPIStatus{}
	}
	if root == "" {
		return OpenAPIStatus{Err: failure(ErrorInvalidRoots, "openapi", "an OpenAPI manifest binding requires an explicit local root")}
	}
	files := make([]CapturedFile, 0, len(binding.LocalRefs)+1)
	document, err := captureBoundFile(root, binding.Document, budget, options, "openapi.document")
	if err != nil {
		return OpenAPIStatus{Err: err}
	}
	files = append(files, document)
	for _, reference := range binding.LocalRefs {
		file, err := captureBoundFile(root, reference, budget, options, "openapi.local_refs")
		if err != nil {
			clearFiles(files)
			return OpenAPIStatus{Err: err}
		}
		files = append(files, file)
	}
	return OpenAPIStatus{Available: true, Files: files}
}

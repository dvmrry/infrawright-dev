package contracts

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	sourceProvenanceContract   = "source-provenance-v1"
	inputProvenanceContract    = "input-provenance-v1"
	sourceReportContract       = "source-evidence-report-v1"
	openAPIDiagnosticsContract = "openapi-diagnostics-v1"
	sourceProvenanceSchemaID   = "https://infrawright.local/schemas/source-provenance-v1.schema.json"
	inputProvenanceSchemaID    = sourceProvenanceSchemaID + "#/$defs/inputProvenance"
	sourceReportSchemaID       = "https://infrawright.local/schemas/source-evidence-report-v1.schema.json"
	openAPIDiagnosticsSchemaID = "https://infrawright.local/schemas/openapi-diagnostics-v1.schema.json"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

var (
	sourceProvenanceSchema   = mustCompileSchema("schemas/source-provenance-v1.schema.json", sourceProvenanceSchemaID)
	inputProvenanceSchema    = mustCompileSchema("schemas/source-provenance-v1.schema.json", inputProvenanceSchemaID)
	sourceReportSchema       = mustCompileSchema("schemas/source-evidence-report-v1.schema.json", sourceReportSchemaID)
	openAPIDiagnosticsSchema = mustCompileSchema("schemas/openapi-diagnostics-v1.schema.json", openAPIDiagnosticsSchemaID)
)

func mustCompileSchema(name, id string) *jsonschema.Schema {
	data, err := schemaFiles.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("authoring contracts: read embedded schema %q: %v", name, err))
	}
	value, err := canonjson.ParseControlJSON(string(data))
	if err != nil {
		panic(fmt.Sprintf("authoring contracts: parse embedded schema %q: %v", name, err))
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(id, value); err != nil {
		panic(fmt.Sprintf("authoring contracts: register embedded schema %q: %v", name, err))
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		panic(fmt.Sprintf("authoring contracts: compile embedded schema %q: %v", name, err))
	}
	return schema
}

func decodeDocument[T any](data []byte, contract string, schema *jsonschema.Schema, output *T) error {
	value, err := canonjson.ParseControlJSON(string(data))
	if err != nil {
		return contractError(ErrorInvalidJSON, contract, "$", "input is not strict control JSON")
	}
	if err := schema.Validate(value); err != nil {
		return structuralError(contract, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return contractError(ErrorInvalidStructure, contract, "$", "input cannot be decoded into the typed contract")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return contractError(ErrorInvalidStructure, contract, "$", "input contains trailing content")
	}
	return nil
}

func nonCanonicalDocumentError(contract string) error {
	return contractError(ErrorInvalidStructure, contract, "$", "input must exactly equal its canonical rendering")
}

type validationLeaf struct {
	instance string
	keyword  string
}

func structuralError(contract string, err error) error {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return contractError(ErrorInvalidStructure, contract, "$", "input does not satisfy the embedded schema")
	}
	leaves := make([]validationLeaf, 0)
	collectValidationLeaves(validationErr, &leaves)
	if len(leaves) == 0 {
		return contractError(ErrorInvalidStructure, contract, "$", "input does not satisfy the embedded schema")
	}
	sort.Slice(leaves, func(i, j int) bool {
		if leaves[i].instance != leaves[j].instance {
			return leaves[i].instance < leaves[j].instance
		}
		return leaves[i].keyword < leaves[j].keyword
	})
	leaf := leaves[0]
	keyword := leaf.keyword
	if keyword == "" {
		keyword = "$schema"
	}
	return contractError(
		ErrorInvalidStructure,
		contract,
		jsonPointerPath(leaf.instance),
		"input violates schema keyword "+keyword,
	)
}

func collectValidationLeaves(err *jsonschema.ValidationError, output *[]validationLeaf) {
	if len(err.Causes) != 0 {
		for _, cause := range err.Causes {
			collectValidationLeaves(cause, output)
		}
		return
	}
	keyword := "/" + strings.Join(err.ErrorKind.KeywordPath(), "/")
	*output = append(*output, validationLeaf{
		instance: "/" + strings.Join(err.InstanceLocation, "/"),
		keyword:  strings.TrimSuffix(keyword, "/"),
	})
}

func jsonPointerPath(pointer string) string {
	if pointer == "" || pointer == "/" {
		return "$"
	}
	return "$" + pointer
}

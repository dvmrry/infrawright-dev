package contracts

import "fmt"

// ErrorCode classifies project-owned contract failures under
// the authoring artifact contract without exposing dependency error text.
type ErrorCode string

const (
	// ErrorInvalidJSON means the input was not strict control JSON under the authoring artifact contract.
	ErrorInvalidJSON ErrorCode = "invalid_json"
	// ErrorInvalidStructure means the input failed its closed schema under the authoring artifact contract.
	ErrorInvalidStructure ErrorCode = "invalid_structure"
	// ErrorInvalidSemantics means cross-field invariants from the authoring artifact contract failed.
	ErrorInvalidSemantics ErrorCode = "invalid_semantics"
)

// ContractError is the deterministic public failure shape required by
// the authoring artifact contract for authoring contract boundaries.
type ContractError struct {
	// Code is the stable machine-readable failure class.
	Code ErrorCode
	// Contract identifies source-provenance-v1 or source-evidence-report-v1.
	Contract string
	// Path is a stable JSON-pointer-like location beginning at $.
	Path string
	// Detail is project-owned deterministic diagnostic text.
	Detail string
}

// Error implements error for ContractError as required by
// the authoring artifact contract.
func (e *ContractError) Error() string {
	return fmt.Sprintf("authoring contract %s %s at %s: %s", e.Contract, e.Code, e.Path, e.Detail)
}

func contractError(code ErrorCode, contract, path, detail string) error {
	return &ContractError{Code: code, Contract: contract, Path: path, Detail: detail}
}

func semanticErrorf(contract, path, format string, args ...any) error {
	return contractError(ErrorInvalidSemantics, contract, path, fmt.Sprintf(format, args...))
}

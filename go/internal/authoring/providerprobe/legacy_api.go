package providerprobe

// ValidateLegacyOpenAPI validates one already-decoded document with the
// frozen node-src/authoring/openapi.ts compatibility contract. It exists for
// A6 command composition only; source-first callers must use openapiadapter.
func ValidateLegacyOpenAPI(document map[string]any) error {
	return validateLegacyOpenAPI(document)
}

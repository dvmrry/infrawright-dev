package tfrender

// PythonTransformStringForAdopt exposes the existing byte-compatible scalar
// spelling used by transform identities to the adoption identity port.
func PythonTransformStringForAdopt(value any) (string, error) {
	return pythonTransformString(value)
}

// FormatImportTemplateForAdopt exposes the existing Python-compatible import
// template formatter to adoption. Artifact rendering remains owned here.
func FormatImportTemplateForAdopt(template string, original map[string]any) (string, error) {
	return formatImportTemplate(template, original)
}

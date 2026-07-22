package modulesgen

// formatter.go owns the injected HCL-formatting seam used after tfrender has
// produced Terraform source. Formatting operates on the rendered token stream;
// it does not use hclwrite's AST construction APIs and therefore does not alter
// the byte-producing renderer contract.

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// HclFormatter formats generated Terraform source (.tf / .tftest.hcl content)
// before GenerateModule writes it to disk. It ports the HclFormatter type from
// the original implementation while allowing a subprocess-free production
// implementation.
type HclFormatter interface {
	FormatHCL(source string) (string, error)
}

// FormatterFunc adapts a plain function to the HclFormatter interface.
type FormatterFunc func(source string) (string, error)

// FormatHCL implements HclFormatter.
func (f FormatterFunc) FormatHCL(source string) (string, error) { return f(source) }

// IdentityFormatter returns source unchanged. It mirrors the Node test suite's
// IDENTITY_FORMATTER fake.
var IdentityFormatter HclFormatter = FormatterFunc(func(source string) (string, error) {
	return source, nil
})

type hclFormatter struct{}

// NewHCLFormatter returns the production post-render formatter. The renderer
// remains internal/tfrender; this formatter only normalizes its existing token
// stream in process.
func NewHCLFormatter() HclFormatter {
	return hclFormatter{}
}

// FormatHCL validates the complete generated configuration before formatting
// it. hclwrite.Format deliberately tolerates malformed token streams, so the
// explicit parse is the fail-closed guard that prevents invalid generated HCL
// from being written.
func (hclFormatter) FormatHCL(source string) (string, error) {
	sourceBytes := []byte(source)
	if _, diagnostics := hclwrite.ParseConfig(sourceBytes, "generated.tf", hcl.InitialPos); diagnostics.HasErrors() {
		return "", fmt.Errorf("generated HCL is invalid: %s", diagnostics.Error())
	}
	return string(hclwrite.Format(sourceBytes)), nil
}

package modulesgen

import (
	"strings"
	"testing"
)

func TestFormatterFuncSatisfiesHclFormatterSeam(t *testing.T) {
	calls := 0
	fake := FormatterFunc(func(source string) (string, error) {
		calls++
		return strings.ToUpper(source), nil
	})
	var seam HclFormatter = fake
	got, err := seam.FormatHCL("abc")
	if err != nil {
		t.Fatalf("FormatHCL: %v", err)
	}
	if got != "ABC" {
		t.Errorf("FormatHCL(%q) = %q, want %q", "abc", got, "ABC")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestIdentityFormatterReturnsSourceUnchanged(t *testing.T) {
	const source = "resource \"x\" \"y\" {}\n"
	got, err := IdentityFormatter.FormatHCL(source)
	if err != nil {
		t.Fatalf("FormatHCL: %v", err)
	}
	if got != source {
		t.Errorf("FormatHCL(%q) = %q, want unchanged", source, got)
	}
}

func TestHCLFormatterFormatsValidSourceInProcess(t *testing.T) {
	const source = "resource \"x\" \"y\" {\nvalue=1\n}\n"
	const want = "resource \"x\" \"y\" {\n  value = 1\n}\n"
	got, err := NewHCLFormatter().FormatHCL(source)
	if err != nil {
		t.Fatalf("FormatHCL: %v", err)
	}
	if got != want {
		t.Errorf("FormatHCL() = %q, want %q", got, want)
	}
}

func TestHCLFormatterRejectsInvalidSourceBeforeFormatting(t *testing.T) {
	const source = "resource \"x\" \"y\" {\n  value =\n"
	got, err := NewHCLFormatter().FormatHCL(source)
	if err == nil {
		t.Fatal("FormatHCL invalid source: expected an error, got nil")
	}
	if got != "" {
		t.Errorf("FormatHCL invalid source returned %q, want no output", got)
	}
	if !strings.Contains(err.Error(), "generated HCL is invalid") {
		t.Errorf("FormatHCL invalid source error = %q, want generated-HCL context", err)
	}
}

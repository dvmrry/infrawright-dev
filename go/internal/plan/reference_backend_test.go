package plan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func writeReferenceBackendTestFile(t *testing.T, content []byte) string {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "backend.json")
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", filePath, err)
	}
	return filePath
}

func requireReferenceBackendFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *procerr.ProcessFailure with code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

func TestReferenceBackendEnvironmentFromConfigProjectsExactAllowlist(t *testing.T) {
	backend := writeReferenceBackendTestFile(t, []byte(`{
  "use_oidc": true,
  "tenant_id": "tenant<&>\u2028",
  "storage_account_name": "example",
  "subscription_id": "subscription",
  "use_msi": false,
  "container_name": "tfstate",
  "use_cli": false,
  "resource_group_name": "state-rg",
  "use_azuread_auth": true,
  "lookup_blob_endpoint": false
}`))

	got, err := ReferenceBackendEnvironmentFromConfig(backend)
	if err != nil {
		t.Fatalf("ReferenceBackendEnvironmentFromConfig(%q) error: %v", backend, err)
	}
	want := `{"container_name":"tfstate","lookup_blob_endpoint":false,"resource_group_name":"state-rg","storage_account_name":"example","subscription_id":"subscription","tenant_id":"tenant<&>` + " " + `","use_azuread_auth":true,"use_cli":false,"use_msi":false,"use_oidc":true}`
	if got[ReferenceBackendEnvironment] != want {
		t.Errorf("ReferenceBackendEnvironmentFromConfig(%q)[%q] = %q, want %q", backend, ReferenceBackendEnvironment, got[ReferenceBackendEnvironment], want)
	}
	if len(got) != 1 {
		t.Errorf("len(ReferenceBackendEnvironmentFromConfig(%q)) = %d, want 1", backend, len(got))
	}
}

func TestReferenceBackendEnvironmentFromConfigPreservesJSONStringifyUTF16Semantics(t *testing.T) {
	// Frozen with Node v24.15.0:
	//   JSON.stringify(Object.fromEntries(Object.entries(JSON.parse(input)).sort()))
	// These vectors pin the distinction encoding/json's semantic tree cannot
	// retain on its own: a lone surrogate escape is not U+FFFD.
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "lone_high_surrogate",
			input: `{"tenant_id":"\ud800"}`,
			want:  `{"tenant_id":"\ud800"}`,
		},
		{
			name:  "lone_low_surrogate_normalizes_hex_case",
			input: `{"tenant_id":"\uDFFF"}`,
			want:  `{"tenant_id":"\udfff"}`,
		},
		{
			name:  "replacement_character",
			input: `{"tenant_id":"�"}`,
			want:  `{"tenant_id":"�"}`,
		},
		{
			name:  "escaped_valid_pair",
			input: `{"tenant_id":"\ud83d\ude00"}`,
			want:  `{"tenant_id":"😀"}`,
		},
		{
			name:  "literal_astral",
			input: `{"tenant_id":"😀"}`,
			want:  `{"tenant_id":"😀"}`,
		},
		{
			name:  "ordinary_escapes_controls_and_non_ascii",
			input: `{"tenant_id":"a\\b\/c\u0000\b\f\n\r\t<&>é\u2028"}`,
			want:  `{"tenant_id":"a\\b/c\u0000\b\f\n\r\t<&>é` + " " + `"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := writeReferenceBackendTestFile(t, []byte(test.input))
			environment, err := ReferenceBackendEnvironmentFromConfig(backend)
			if err != nil {
				t.Fatalf("ReferenceBackendEnvironmentFromConfig(%q) error: %v", test.input, err)
			}
			got := environment[ReferenceBackendEnvironment]
			if got != test.want {
				t.Errorf("ReferenceBackendEnvironmentFromConfig(%q)[%q] = %q, want %q", test.input, ReferenceBackendEnvironment, got, test.want)
			}
		})
	}
}

func TestReferenceBackendEnvironmentFromConfigRejectsSecretFieldsWithoutEcho(t *testing.T) {
	secretFields := []string{
		"access_key",
		"client_id",
		"oidc_token_file_path",
		"oidc_token",
		"oidc_request_token",
		"client_secret_file_path",
		"client_certificate_path",
		"key",
		"msi_endpoint",
		"sas_token",
		"unknown_authentication_material",
	}
	for _, key := range secretFields {
		t.Run(key, func(t *testing.T) {
			secret := "must-not-echo-" + key
			backend := writeReferenceBackendTestFile(t, []byte(
				`{"container_name":"tfstate","`+key+`":"`+secret+`","storage_account_name":"example"}`,
			))
			_, err := ReferenceBackendEnvironmentFromConfig(backend)
			failure := requireReferenceBackendFailure(t, err, "UNSAFE_REFERENCE_BACKEND_CONFIG")
			if strings.Contains(failure.Message, secret) {
				t.Errorf("ProcessFailure.Message = %q, must not contain secret %q", failure.Message, secret)
			}
		})
	}
}

func TestReferenceBackendEnvironmentFromConfigRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "empty", content: []byte{}},
		{name: "whitespace", content: []byte(" \n")},
		{name: "array", content: []byte(`[]`)},
		{name: "empty_object", content: []byte(`{}`)},
		{name: "hcl", content: []byte(`storage_account_name = "example"`)},
		{name: "duplicate", content: []byte(`{"storage_account_name":"a","storage_account_name":"b"}`)},
		{name: "invalid_utf8", content: []byte{0xff}},
		{name: "string_field_boolean", content: []byte(`{"container_name":true}`)},
		{name: "string_field_empty", content: []byte(`{"container_name":""}`)},
		{name: "boolean_field_string", content: []byte(`{"use_oidc":"true"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := writeReferenceBackendTestFile(t, test.content)
			_, err := ReferenceBackendEnvironmentFromConfig(backend)
			requireReferenceBackendFailure(t, err, "INVALID_REFERENCE_BACKEND_CONFIG")
		})
	}
}

func TestReferenceBackendEnvironmentFromConfigEnforcesStable64KiBRead(t *testing.T) {
	const prefix = `{"storage_account_name":"`
	const suffix = `"}`
	accepted := prefix + strings.Repeat("x", int(maxReferenceBackendConfigBytes)-len(prefix)-len(suffix)) + suffix
	if len(accepted) != int(maxReferenceBackendConfigBytes) {
		t.Fatalf("len(accepted) = %d, want %d", len(accepted), maxReferenceBackendConfigBytes)
	}
	backend := writeReferenceBackendTestFile(t, []byte(accepted))
	if _, err := ReferenceBackendEnvironmentFromConfig(backend); err != nil {
		t.Fatalf("ReferenceBackendEnvironmentFromConfig(exactly 64 KiB) error: %v", err)
	}
	if err := os.WriteFile(backend, append([]byte(accepted), ' '), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q, oversized) error: %v", backend, err)
	}
	_, err := ReferenceBackendEnvironmentFromConfig(backend)
	requireReferenceBackendFailure(t, err, "INVALID_REFERENCE_BACKEND_CONFIG")
}

func TestReferenceBackendEnvironmentFromConfigClassifiesReadFailures(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	_, err := ReferenceBackendEnvironmentFromConfig(missing)
	failure := requireReferenceBackendFailure(t, err, "REFERENCE_BACKEND_CONFIG_READ_FAILED")
	if failure.Category != procerr.CategoryIO {
		t.Errorf("ProcessFailure.Category = %q, want %q", failure.Category, procerr.CategoryIO)
	}

	directory := t.TempDir()
	_, err = ReferenceBackendEnvironmentFromConfig(directory)
	requireReferenceBackendFailure(t, err, "INVALID_REFERENCE_BACKEND_CONFIG")
}

package adopt

import (
	"crypto/sha1" // #nosec G505 -- SHA-1 is a compatibility name digest, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	tfjson "github.com/hashicorp/terraform-json"
)

const (
	defaultOracleTimeoutMS = 300_000
	oraclePlanDebugHint    = "rerun with INFRAWRIGHT_KEEP_ORACLE=1 to retain the sensitive scratch workdir for local inspection"
)

var (
	backendBlockPattern = regexp.MustCompile(`\bbackend\s+"[^"]+"\s*\{`)
	cloudBlockPattern   = regexp.MustCompile(`\bcloud\s*\{`)
	formatVersionOne    = regexp.MustCompile(`^1\.`)
)

// OracleError ports OracleError from the original implementation.
type OracleError struct{ Message string }

// Error implements error.
func (e *OracleError) Error() string { return e.Message }

func oracleErrorf(format string, args ...any) error {
	return &OracleError{Message: fmt.Sprintf(format, args...)}
}

// OracleStateObject ports OracleStateObject from
// the original implementation.
type OracleStateObject struct {
	Address         string
	SensitiveValues any
	Values          map[string]any
}

// OracleBatchResourceRequest ports OracleBatchResourceRequest from
// the original implementation. Maps are copied before a transaction uses
// them, so caller mutation cannot alter a running oracle transaction.
type OracleBatchResourceRequest struct {
	KeyToImportID map[string]string
	Policy        *metadata.DriftPolicy
	RawItems      map[string]map[string]any
	ResourceType  string
}

// OracleBatchState ports OracleBatchState from
// the original implementation.
type OracleBatchState map[string]map[string]OracleStateObject

// OracleCommandRequest ports OracleCommandRequest from
// the original implementation. Environment and sensitive tokens are
// complete explicit snapshots, never ambient process state.
type OracleCommandRequest struct {
	Argv            []string
	CWD             string
	DebugName       string
	Environment     map[string]string
	CaptureOutput   bool
	SensitiveTokens []string
}

// OracleCommandResult ports OracleCommandResult from
// the original implementation.
type OracleCommandResult struct{ Stdout []byte }

// OracleCommandRunner ports OracleCommandRunner from
// the original implementation. Tests use non-forwarding fakes.
type OracleCommandRunner interface {
	Run(OracleCommandRequest) (OracleCommandResult, error)
}

// OracleStateSource ports OracleStateSource from
// the original implementation.
type OracleStateSource string

const (
	// OracleAppliedState applies the accepted local scratch plan and then
	// reads ephemeral local state.
	OracleAppliedState OracleStateSource = "applied-state"
	// OracleAcceptedPlan extracts provider-observed state from a fully known,
	// exact import-only plan without running Terraform Apply.
	OracleAcceptedPlan OracleStateSource = "accepted-plan"
)

// OracleTimeoutMS ports oracleTimeoutMs from
// the original implementation.
func OracleTimeoutMS(environment map[string]string) (int, error) {
	raw := environment["INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS"]
	if strings.TrimSpace(raw) == "" {
		return defaultOracleTimeoutMS, nil
	}
	seconds, err := parseJavaScriptNumber(raw)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, oracleErrorf("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS must be a positive number")
	}
	milliseconds := math.Ceil(seconds * 1000)
	if milliseconds <= 0 || milliseconds > float64(1<<53-1) || milliseconds > float64(int(^uint(0)>>1)) {
		return 0, oracleErrorf("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS is outside the supported numeric range")
	}
	return int(milliseconds), nil
}

func parseJavaScriptNumber(raw string) (float64, error) {
	text := strings.TrimSpace(raw)
	// Number() accepts unsigned hexadecimal, binary, and octal string
	// literals. A sign before one of these prefixes is not accepted.
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			integer, ok := new(big.Int).SetString(text[2:], base)
			if !ok {
				return 0, strconv.ErrSyntax
			}
			value, _ := new(big.Float).SetInt(integer).Float64()
			return value, nil
		}
	}
	return strconv.ParseFloat(text, 64)
}

// OracleStateSourceFromEnvironment ports oracleStateSource from
// the original implementation.
func OracleStateSourceFromEnvironment(environment map[string]string) (OracleStateSource, error) {
	raw := strings.TrimSpace(environment["INFRAWRIGHT_ORACLE_STATE_SOURCE"])
	switch raw {
	case "", string(OracleAppliedState):
		return OracleAppliedState, nil
	case string(OracleAcceptedPlan):
		return OracleAcceptedPlan, nil
	default:
		return "", oracleErrorf("INFRAWRIGHT_ORACLE_STATE_SOURCE must be applied-state or accepted-plan")
	}
}

// OracleBatchResourceFamily ports oracleBatchResourceFamily from
// the original implementation.
func OracleBatchResourceFamily(resourceTypes []string) string {
	set := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		set[resourceType] = struct{}{}
	}
	sorted := canonjson.SortedStrings(mapKeys(set))
	if len(sorted) == 1 {
		return sorted[0]
	}
	readable := "oracle_batch." + strings.Join(sorted, ".")
	if len(readable) <= 256 {
		return readable
	}
	return fmt.Sprintf("oracle_batch_%d_%s", len(sorted), sha1Prefix(strings.Join(sorted, ",")))
}

func sha1Prefix(value string) string {
	digest := sha1.Sum([]byte(value)) // #nosec G401 -- exact Node compatibility identifier.
	return hex.EncodeToString(digest[:])[:16]
}

func instanceName(key string) string { return "iw_" + sha1Prefix(key) }

// OracleAddress ports oracleAddress from the original implementation.
func OracleAddress(resourceType, key string) string {
	return resourceType + "." + instanceName(key)
}

func checkAddressCollisions(resourceType string, keys []string) error {
	seen := make(map[string]string, len(keys))
	for _, key := range canonjson.SortedStrings(keys) {
		name := instanceName(key)
		if prior, ok := seen[name]; ok {
			return oracleErrorf("%s oracle instance name collision: %s and %s both map to %s", resourceType, jsonString(prior), jsonString(key), name)
		}
		seen[name] = key
	}
	return nil
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// RenderOracleRoot ports renderOracleRoot from
// the original implementation.
func RenderOracleRoot(root *metadata.LoadedPackRoot, provider string) (string, error) {
	source, ok := root.Packs.ProviderSources[provider]
	if !ok {
		return "", oracleErrorf("no provider source declared for %s", provider)
	}
	manifest, err := metadata.ManifestForProvider(root.Packs, provider)
	if err != nil {
		return "", err
	}
	quotedSource, err := tfrender.RenderHclQuotedString(source)
	if err != nil {
		return "", err
	}
	pin := ""
	if value, ok := manifest.Data["pin"].(string); ok {
		quotedPin, quoteErr := tfrender.RenderHclQuotedString(value)
		if quoteErr != nil {
			return "", quoteErr
		}
		pin = "      version = " + quotedPin + "\n"
	}
	providerFile := filepath.Join(manifest.Directory, "oracle", provider+".tf")
	providerBlock, readErr := os.ReadFile(providerFile)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("read %s oracle provider configuration: %w", provider, readErr)
	}
	if errors.Is(readErr, os.ErrNotExist) {
		providerBlock = []byte("provider \"" + provider + "\" {\n  # credentials via provider environment variables\n}\n")
	}
	output := "terraform {\n" +
		"  required_version = \"\u003e= 1.5\"\n" +
		"  required_providers {\n" +
		"    " + provider + " = {\n" +
		"      source = " + quotedSource + "\n" + pin +
		"    }\n  }\n}\n\n" + string(providerBlock)
	if err := assertLocalScratchRoot(output); err != nil {
		return "", err
	}
	return output, nil
}

// RenderOracleImports ports renderOracleImports from
// the original implementation.
func RenderOracleImports(resourceType string, keyToImportID map[string]string) (string, error) {
	var output strings.Builder
	keys := canonjson.SortedStrings(mapKeys(keyToImportID))
	for index, key := range keys {
		quoted, err := tfrender.RenderHclQuotedString(keyToImportID[key])
		if err != nil {
			return "", err
		}
		if index > 0 {
			output.WriteByte('\n')
		}
		fmt.Fprintf(&output, "import {\n  to = %s\n  id = %s\n}\n", OracleAddress(resourceType, key), quoted)
	}
	return output.String(), nil
}

func renderOracleBatchImports(resources []OracleBatchResourceRequest) (string, error) {
	sorted := append([]OracleBatchResourceRequest(nil), resources...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ResourceType < sorted[j].ResourceType })
	parts := make([]string, 0, len(sorted))
	for _, resource := range sorted {
		part, err := RenderOracleImports(resource.ResourceType, resource.KeyToImportID)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n"), nil
}

func assertLocalScratchRoot(text string) error {
	if backendBlockPattern.MatchString(text) {
		return oracleErrorf("oracle scratch root must not declare a Terraform backend; oracle state is intentionally ephemeral and local")
	}
	if cloudBlockPattern.MatchString(text) {
		return oracleErrorf("oracle scratch root must not declare Terraform cloud; oracle state is intentionally ephemeral and local")
	}
	return nil
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func providerName(source string) string {
	if len(strings.Split(source, "/")) == 2 {
		return "registry.terraform.io/" + source
	}
	return source
}

type expectedOracleInstance struct {
	ImportID     string
	Key          string
	ProviderName string
	ResourceType string
}

type decodedPlan struct {
	Typed *tfjson.Plan
	Raw   map[string]any
}

// DecodeOraclePlan decodes Terraform plan JSON with terraform-json typed
// structs and a canonjson lossless raw sidecar. The typed Complete pointer is
// the fail-closed authorization gate; the sidecar retains fields and number
// tokens terraform-json does not model.
func DecodeOraclePlan(data []byte) (*tfjson.Plan, map[string]any, error) {
	var typed tfjson.Plan
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, nil, oracleErrorf("terraform show -json plan returned malformed JSON")
	}
	value, err := canonjson.Decode(data)
	if err != nil {
		return nil, nil, oracleErrorf("terraform show -json plan returned malformed JSON")
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, nil, oracleErrorf("terraform show -json plan returned a non-object")
	}
	return &typed, raw, nil
}

// DecodeOracleState decodes Terraform state JSON with terraform-json typed
// structs plus the lossless raw representation used for exact extraction.
func DecodeOracleState(data []byte) (*tfjson.State, map[string]any, error) {
	var typed tfjson.State
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, nil, oracleErrorf("terraform show -json state returned malformed JSON")
	}
	value, err := canonjson.Decode(data)
	if err != nil {
		return nil, nil, oracleErrorf("terraform show -json state returned malformed JSON")
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, nil, oracleErrorf("terraform show -json state returned a non-object")
	}
	return &typed, raw, nil
}

func mapKeys[V any](value map[string]V) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}

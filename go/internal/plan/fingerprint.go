package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// PlanFingerprintVersion ports PLAN_FINGERPRINT_VERSION from
// node-src/domain/plan-fingerprint.ts.
const PlanFingerprintVersion = 2

// FileFingerprint ports FileFingerprint from
// node-src/domain/plan-fingerprint.ts. Its two array positions are path and
// SHA-256 respectively, matching the source tuple's serialized shape.
type FileFingerprint [2]string

// BackendFingerprint ports BackendFingerprint from
// node-src/domain/plan-fingerprint.ts. Nil Key represents JSON null, and nil
// SHA256 represents the source object's absent sha256 property.
type BackendFingerprint struct {
	Key     *string `json:"key"`
	Present bool    `json:"present"`
	SHA256  *string `json:"sha256,omitempty"`
}

// ModuleFingerprint ports ModuleFingerprint from
// node-src/domain/plan-fingerprint.ts.
type ModuleFingerprint struct {
	Files        []FileFingerprint `json:"files"`
	Local        bool              `json:"local"`
	Present      bool              `json:"present"`
	ResourceType string            `json:"resource_type"`
	Source       string            `json:"source"`
}

// PlanSourcesPayload ports PlanSourcesPayload from
// node-src/domain/plan-fingerprint.ts.
type PlanSourcesPayload struct {
	Backend     *BackendFingerprint `json:"backend"`
	MemberTypes []string            `json:"member_types"`
	Modules     []ModuleFingerprint `json:"modules"`
	RootTF      []FileFingerprint   `json:"root_tf"`
	VarFiles    []FileFingerprint   `json:"var_files"`
}

// InitSourcesPayload ports InitSourcesPayload from
// node-src/domain/plan-fingerprint.ts.
type InitSourcesPayload struct {
	Backend    *BackendFingerprint `json:"backend"`
	Modules    []ModuleFingerprint `json:"modules"`
	RootConfig []FileFingerprint   `json:"root_config"`
}

// PlanFingerprintInput ports PlanFingerprintInput from
// node-src/domain/plan-fingerprint.ts. Nil optional strings represent both an
// omitted source property and its null value; those forms are behaviorally
// identical for fingerprint capture.
type PlanFingerprintInput struct {
	EnvDir        string
	VarFiles      []string
	MemberTypes   []string
	BackendConfig *string
	BackendKey    *string
}

// InitFingerprintInput ports InitFingerprintInput from
// node-src/domain/plan-fingerprint.ts. BackendConfig and BackendKey use the
// same nil representation as PlanFingerprintInput.
type InitFingerprintInput struct {
	EnvDir        string
	MemberTypes   []string
	BackendConfig *string
	BackendKey    *string
}

// PlanFingerprintV2 ports PlanFingerprintV2 from
// node-src/domain/plan-fingerprint.ts.
type PlanFingerprintV2 struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

// ModuleFingerprintIgnoredDirs returns a detached slice in the insertion
// order of MODULE_FINGERPRINT_IGNORED_DIRS from
// node-src/domain/plan-fingerprint.ts.
func ModuleFingerprintIgnoredDirs() []string {
	return []string{
		".git",
		".mypy_cache",
		".pytest_cache",
		".ruff_cache",
		".terraform",
		"__pycache__",
	}
}

// CanonicalPlanSourcesJSON matches
// json.dumps(payload, sort_keys=True, separators=(",", ":")) from
// node-src/domain/plan-fingerprint.ts. It deliberately accepts only the
// fingerprint value model instead of growing a second general JSON encoder.
func CanonicalPlanSourcesJSON(payload PlanSourcesPayload) string {
	var out strings.Builder
	encodePlanSources(&out, payload)
	return out.String()
}

// PlanSourcesSHA256 ports planSourcesSha256 from
// node-src/domain/plan-fingerprint.ts.
func PlanSourcesSHA256(payload PlanSourcesPayload) string {
	digest := sha256.Sum256([]byte(CanonicalPlanSourcesJSON(payload)))
	return hex.EncodeToString(digest[:])
}

// InitSourcesSHA256 ports initSourcesSha256 from
// node-src/domain/plan-fingerprint.ts.
func InitSourcesSHA256(payload InitSourcesPayload) string {
	var encoded strings.Builder
	encodeInitSources(&encoded, payload)
	digest := sha256.Sum256([]byte(encoded.String()))
	return hex.EncodeToString(digest[:])
}

// CapturePlanSourcesPayload ports capturePlanSourcesPayload from
// node-src/domain/plan-fingerprint.ts. A nil budget selects the source default;
// a supplied budget is shared serially by every nested directory and file read.
func CapturePlanSourcesPayload(
	input PlanFingerprintInput,
	budget *artifacts.ReadBudget,
) (PlanSourcesPayload, error) {
	budget = fingerprintBudget(budget)
	backend, err := BackendConfigFingerprint(input.BackendConfig, input.BackendKey, budget)
	if err != nil {
		return PlanSourcesPayload{}, err
	}
	modules, err := ModuleFingerprints(input.EnvDir, input.MemberTypes, budget)
	if err != nil {
		return PlanSourcesPayload{}, err
	}
	rootTF, err := RootTFFingerprints(input.EnvDir, budget)
	if err != nil {
		return PlanSourcesPayload{}, err
	}
	varFiles, err := VarFileFingerprints(input.VarFiles, budget)
	if err != nil {
		return PlanSourcesPayload{}, err
	}
	return PlanSourcesPayload{
		Backend:     backend,
		MemberTypes: canonjson.SortedStrings(input.MemberTypes),
		Modules:     modules,
		RootTF:      rootTF,
		VarFiles:    varFiles,
	}, nil
}

// CaptureInitSourcesPayload ports captureInitSourcesPayload from
// node-src/domain/plan-fingerprint.ts. Nil and supplied budgets have the same
// contract as CapturePlanSourcesPayload.
func CaptureInitSourcesPayload(
	input InitFingerprintInput,
	budget *artifacts.ReadBudget,
) (InitSourcesPayload, error) {
	budget = fingerprintBudget(budget)
	backend, err := BackendConfigFingerprint(input.BackendConfig, input.BackendKey, budget)
	if err != nil {
		return InitSourcesPayload{}, err
	}
	modules, err := ModuleFingerprints(input.EnvDir, input.MemberTypes, budget)
	if err != nil {
		return InitSourcesPayload{}, err
	}
	rootConfig, err := RootConfigFingerprints(input.EnvDir, budget)
	if err != nil {
		return InitSourcesPayload{}, err
	}
	return InitSourcesPayload{
		Backend:    backend,
		Modules:    modules,
		RootConfig: rootConfig,
	}, nil
}

// FingerprintPlanV2 ports planFingerprintV2 from
// node-src/domain/plan-fingerprint.ts. A nil budget selects the source default.
func FingerprintPlanV2(
	input PlanFingerprintInput,
	budget *artifacts.ReadBudget,
) (PlanFingerprintV2, error) {
	payload, err := CapturePlanSourcesPayload(input, budget)
	if err != nil {
		return PlanFingerprintV2{}, err
	}
	return PlanFingerprintV2{
		Version: PlanFingerprintVersion,
		SHA256:  PlanSourcesSHA256(payload),
	}, nil
}

func encodePlanSources(out *strings.Builder, payload PlanSourcesPayload) {
	out.WriteString(`{"backend":`)
	encodeBackend(out, payload.Backend)
	out.WriteString(`,"member_types":`)
	encodeStrings(out, payload.MemberTypes)
	out.WriteString(`,"modules":`)
	encodeModules(out, payload.Modules)
	out.WriteString(`,"root_tf":`)
	encodeFiles(out, payload.RootTF)
	out.WriteString(`,"var_files":`)
	encodeFiles(out, payload.VarFiles)
	out.WriteByte('}')
}

func encodeInitSources(out *strings.Builder, payload InitSourcesPayload) {
	out.WriteString(`{"backend":`)
	encodeBackend(out, payload.Backend)
	out.WriteString(`,"modules":`)
	encodeModules(out, payload.Modules)
	out.WriteString(`,"root_config":`)
	encodeFiles(out, payload.RootConfig)
	out.WriteByte('}')
}

func encodeBackend(out *strings.Builder, backend *BackendFingerprint) {
	if backend == nil {
		out.WriteString("null")
		return
	}
	out.WriteString(`{"key":`)
	if backend.Key == nil {
		out.WriteString("null")
	} else {
		encodeFingerprintString(out, *backend.Key)
	}
	out.WriteString(`,"present":`)
	encodeBool(out, backend.Present)
	if backend.SHA256 != nil {
		out.WriteString(`,"sha256":`)
		encodeFingerprintString(out, *backend.SHA256)
	}
	out.WriteByte('}')
}

func encodeModules(out *strings.Builder, modules []ModuleFingerprint) {
	out.WriteByte('[')
	for index, module := range modules {
		if index > 0 {
			out.WriteByte(',')
		}
		out.WriteString(`{"files":`)
		encodeFiles(out, module.Files)
		out.WriteString(`,"local":`)
		encodeBool(out, module.Local)
		out.WriteString(`,"present":`)
		encodeBool(out, module.Present)
		out.WriteString(`,"resource_type":`)
		encodeFingerprintString(out, module.ResourceType)
		out.WriteString(`,"source":`)
		encodeFingerprintString(out, module.Source)
		out.WriteByte('}')
	}
	out.WriteByte(']')
}

func encodeFiles(out *strings.Builder, files []FileFingerprint) {
	out.WriteByte('[')
	for index, file := range files {
		if index > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('[')
		encodeFingerprintString(out, file[0])
		out.WriteByte(',')
		encodeFingerprintString(out, file[1])
		out.WriteByte(']')
	}
	out.WriteByte(']')
}

func encodeStrings(out *strings.Builder, values []string) {
	out.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		encodeFingerprintString(out, value)
	}
	out.WriteByte(']')
}

func encodeBool(out *strings.Builder, value bool) {
	if value {
		out.WriteString("true")
		return
	}
	out.WriteString("false")
}

// encodeFingerprintString ports encodePythonJsonString from
// node-src/domain/plan-fingerprint.ts. U+007F and above are escaped, unlike
// canonjson.Render's deliberately Node-specific DEL behavior.
func encodeFingerprintString(out *strings.Builder, value string) {
	out.WriteByte('"')
	for _, unit := range utf16.Encode([]rune(value)) {
		switch unit {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if unit < 0x20 || unit >= 0x7f {
				fmt.Fprintf(out, `\u%04x`, unit)
			} else {
				out.WriteByte(byte(unit))
			}
		}
	}
	out.WriteByte('"')
}

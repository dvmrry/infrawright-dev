// Package deployment retains node-src/domain/deployment.ts's deployment.json
// loading, Python-truthy defaults, path resolution, and path accessors while
// applying the Go-authoritative singleton-state v2 roots contract. The v2
// parser rejects retired root configuration fields.
//
// Deployment-path resolution is explicit path > INFRAWRIGHT_DEPLOYMENT env var
// > ./deployment.json, and the overlay/config-dir/imports-dir/envs-dir/
// module-dir/tenant-root/tfvars-format accessors other domain packages
// (chief among them go/internal/roots) build on.
package deployment

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// providerKeys are the only supported keys on a roots.<provider> entry.
var providerKeys = map[string]struct{}{
	"cross_state_references": {},
}

// ReferenceBindingMode describes how declared references resolve.
type ReferenceBindingMode string

// The supported ReferenceBindingMode literals.
const (
	ReferenceBindingDisabled   ReferenceBindingMode = "disabled"
	ReferenceBindingCrossState ReferenceBindingMode = "cross_state"
)

// RootProviderConfig preserves only the explicit cross-state setting.
// HasCrossStateReferences distinguishes an omitted setting from explicit false.
type RootProviderConfig struct {
	HasCrossStateReferences bool
	CrossStateReferences    bool
}

// Deployment ports the Deployment interface from node-src/domain/types.ts.
// Overlay, ModuleDir, and TfvarsFormat are `unknown` in the TypeScript
// source (validateDeployment stores whatever raw JSON value the input
// document carried, deferring type/shape validation to the
// DeploymentOverlay/DeploymentModuleDir/DeploymentTfvarsFormat accessors);
// they are canonjson.Value here for the same reason. ModuleDir and
// TfvarsFormat are additionally optional in the TypeScript interface
// (`module_dir?`, `tfvars_format?`); HasModuleDir/HasTfvarsFormat carry that
// optionality, mirroring RootProviderConfig's HasXxx convention above.
type Deployment struct {
	// Overlay is always present: validateDeployment stores
	// pythonTruthy(overlay) ? overlay : "." unconditionally, never omitting
	// the key.
	Overlay canonjson.Value
	// HasModuleDir is true only when the input document's module_dir key
	// was present AND pythonTruthy (validateDeployment's
	// `...(pythonTruthy(moduleDir) ? { module_dir: moduleDir } : {})`
	// spread): an explicit falsy module_dir (e.g. "", 0, null) is stored
	// exactly as if the key had been omitted, not as a falsy ModuleDir
	// with HasModuleDir true.
	HasModuleDir bool
	ModuleDir    canonjson.Value
	// HasTfvarsFormat is true whenever the input document's tfvars_format
	// key was present at all (validateDeployment's `tfvarsFormat ===
	// undefined ? {} : { tfvars_format: tfvarsFormat }` spread does not
	// consult pythonTruthy), including when its value was JSON null --
	// unlike HasModuleDir above, HasTfvarsFormat is not a truthiness gate.
	HasTfvarsFormat bool
	TfvarsFormat    canonjson.Value
	// Roots is always present (defaults to an empty, non-nil map when the
	// input document has no "roots" key), keyed by provider name.
	Roots map[string]RootProviderConfig
}

// malformed panics with a *procerr.ProcessFailure carrying code
// "INVALID_DEPLOYMENT", category "domain", and message -- the Go analogue
// of node-src/domain/deployment.ts's malformed() helper, typed there as
// returning `never` because it always throws. See recoverProcessFailure
// for how every exported entry point in this package converts the panic
// back into a normal error return.
func malformed(message string) {
	panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "INVALID_DEPLOYMENT",
		Category: procerr.CategoryDomain,
		Message:  message,
	}))
}

func malformedf(format string, args ...any) {
	malformed(fmt.Sprintf(format, args...))
}

// recoverProcessFailure is deferred by every exported entry point in this
// package (as `defer recoverProcessFailure(&err)`) to convert a recovered
// *procerr.ProcessFailure panic (see malformed) into a normal error return.
// Any other recovered value is re-panicked, since it indicates a genuine
// bug rather than an expected validation failure -- the same convention
// go/internal/metadata's recoverMetadataError follows for its own
// MetadataError panics.
func recoverProcessFailure(err *error) {
	if r := recover(); r != nil {
		if pf, ok := r.(*procerr.ProcessFailure); ok {
			*err = pf
			return
		}
		panic(r)
	}
}

// pythonTruthy ports pythonTruthy from node-src/domain/deployment.ts:
// Python's bool() coercion (not JavaScript's, which additionally treats
// NaN as falsy and has no dict/list special case) applied to a decoded
// JSON value -- false for JSON null/false/0/""/an empty array/an empty
// object, true for everything else, including a non-zero-but-falsy-looking
// JS value with no Python equivalent (this package's Go value tree cannot
// represent JS's -0 or NaN as anything other than the float64 zero value or
// NaN, so those distinctions do not arise here the way a literal
// `value === 0` JS check might suggest).
func pythonTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return !isZeroNumber(value)
	}
}

// isZeroNumber reports whether value is a JSON number decoded to exactly
// zero: a json.Number holding the source lexeme (how
// canonjson.ParseControlJSON represents every JSON number validateDeployment
// ever sees) or a plain float64 (for a Deployment value built
// programmatically rather than decoded from JSON).
func isZeroNumber(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == 0
	case json.Number:
		f, err := v.Float64()
		return err == nil && f == 0
	default:
		return false
	}
}

// validateRootConfig validates the singleton-state v2 provider contract.
func validateRootConfig(value any, provider string) RootProviderConfig {
	object, ok := value.(map[string]any)
	if !ok {
		malformedf("roots.%s must be an object", provider)
	}
	var retiredKeys, unknownKeys []string
	for key := range object {
		if key == "strategy" || key == "groups" || key == "bind_references" {
			retiredKeys = append(retiredKeys, key)
			continue
		}
		if _, known := providerKeys[key]; !known {
			unknownKeys = append(unknownKeys, key)
		}
	}
	if retired := canonjson.SortedStrings(retiredKeys); len(retired) > 0 {
		malformedf("roots.%s.%s has been removed; see docs/singleton-state-topology-v2.md", provider, retired[0])
	}
	if len(unknownKeys) > 0 {
		malformedf("roots.%s has unknown key(s): %s", provider, strings.Join(canonjson.SortedStrings(unknownKeys), ", "))
	}

	crossValue, hasCrossKey := object["cross_state_references"]
	if hasCrossKey {
		if _, ok := crossValue.(bool); !ok {
			malformedf("roots.%s.cross_state_references must be a bool", provider)
		}
	}
	var config RootProviderConfig
	if b, ok := crossValue.(bool); ok {
		config.HasCrossStateReferences = true
		config.CrossStateReferences = b
	}
	return config
}

// deploymentReferenceBindingMode ports deploymentReferenceBindingMode from
// node-src/domain/deployment.ts.
func deploymentReferenceBindingMode(deployment Deployment, provider string) ReferenceBindingMode {
	config, ok := deployment.Roots[provider]
	if ok && config.HasCrossStateReferences && !config.CrossStateReferences {
		return ReferenceBindingDisabled
	}
	return ReferenceBindingCrossState
}

// DeploymentReferenceBindingMode ports deploymentReferenceBindingMode from
// node-src/domain/deployment.ts. Unlike this package's other exported
// accessors, it never fails: an absent or malformed roots.<provider> entry
// (impossible to construct through LoadDeployment, but not impossible for
// a hand-built Deployment) defaults to cross-state. Only an explicit false
// cross_state_references setting disables generated bindings.
func DeploymentReferenceBindingMode(deployment Deployment, provider string) ReferenceBindingMode {
	return deploymentReferenceBindingMode(deployment, provider)
}

// validateDeployment ports validateDeployment from
// node-src/domain/deployment.ts.
func validateDeployment(value any) Deployment {
	object, ok := value.(map[string]any)
	if !ok {
		malformed("deployment must contain a JSON object")
	}

	roots := map[string]RootProviderConfig{}
	if rootsValue, hasRoots := object["roots"]; hasRoots {
		rootsObject, ok := rootsValue.(map[string]any)
		if !ok {
			malformed("deployment roots must be an object")
		}
		keys := make([]string, 0, len(rootsObject))
		for key := range rootsObject {
			keys = append(keys, key)
		}
		for _, provider := range canonjson.SortedStrings(keys) {
			if provider == "" {
				malformed("deployment roots keys must be non-empty strings")
			}
			roots[provider] = validateRootConfig(rootsObject[provider], provider)
		}
	}

	overlayValue, hasOverlay := object["overlay"]
	var overlay canonjson.Value = "."
	if hasOverlay && pythonTruthy(overlayValue) {
		overlay = overlayValue
	}

	deployment := Deployment{Overlay: overlay, Roots: roots}
	if moduleDirValue, hasModuleDir := object["module_dir"]; hasModuleDir && pythonTruthy(moduleDirValue) {
		deployment.HasModuleDir = true
		deployment.ModuleDir = moduleDirValue
	}
	if tfvarsFormatValue, hasTfvarsFormat := object["tfvars_format"]; hasTfvarsFormat {
		deployment.HasTfvarsFormat = true
		deployment.TfvarsFormat = tfvarsFormatValue
	}
	return deployment
}

// readOptionalUtf8 ports readOptionalUtf8 from node-src/io/files.ts (via
// its decodeUtf8 helper), kept package-private per this port's per-package
// convention for this small helper (see go/internal/metadata/files.go's
// own copy and its doc comment for the rationale): loadDeployment is the
// only caller in this package's scope, and it only ever needs the optional
// variant (a missing deployment.json is not an error; see
// deploymentFromText).
func readOptionalUtf8(path, label string) *string {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "READ_FAILED",
			Category: procerr.CategoryIO,
			Message:  fmt.Sprintf("unable to read %s", label),
		}))
	}
	if !utf8.Valid(content) {
		panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "INVALID_UTF8",
			Category: procerr.CategoryDomain,
			Message:  fmt.Sprintf("%s is not valid UTF-8", label),
		}))
	}
	text := string(content)
	return &text
}

// deploymentFromText ports deploymentFromText from
// node-src/domain/deployment.ts.
func deploymentFromText(text *string) Deployment {
	if text == nil || strings.TrimSpace(*text) == "" {
		return Deployment{Overlay: ".", Roots: map[string]RootProviderConfig{}}
	}
	value, err := canonjson.ParseControlJSON(*text)
	if err != nil {
		malformed("deployment is not valid JSON")
	}
	return validateDeployment(value)
}

// LoadDeployment ports loadDeployment from node-src/domain/deployment.ts.
func LoadDeployment(deploymentPath string) (deployment Deployment, err error) {
	defer recoverProcessFailure(&err)
	return deploymentFromText(readOptionalUtf8(deploymentPath, "deployment")), nil
}

// DeploymentPathOptions ports the options bag deploymentPath accepts in
// node-src/domain/deployment.ts. Each field's nil-ness mirrors the
// TypeScript optional property's undefined-ness (`options?.explicit`,
// `options?.environment`, `options?.cwd`) exactly, which matters here
// because Explicit/Environment participate in `||` (falsy) fallbacks while
// Cwd participates in a `??` (nullish) fallback -- see DeploymentPath's doc
// comment for why that distinction is load-bearing and must not be
// collapsed to a single "zero value means unset" convention.
type DeploymentPathOptions struct {
	// Explicit, if non-nil, is options.explicit. A non-nil empty string is
	// deliberately equivalent to nil here (see DeploymentPath).
	Explicit *string
	// Environment, if non-nil, replaces the process environment
	// DeploymentPath reads INFRAWRIGHT_DEPLOYMENT from.
	Environment map[string]string
	// Cwd, if non-nil, is used as-is -- including a non-nil empty string --
	// as the directory deployment.json is joined onto when neither
	// Explicit nor the environment variable resolves. If nil, the process's
	// actual working directory (os.Getwd()) is used.
	Cwd *string
}

// DeploymentPath ports deploymentPath from node-src/domain/deployment.ts:
//
//	const environment = options?.environment ?? process.env;
//	const selected = options?.explicit || environment.INFRAWRIGHT_DEPLOYMENT;
//	return selected || path.join(options?.cwd ?? process.cwd(), "deployment.json");
//
// Two different fallback operators are load-bearing here and both are
// preserved exactly: `options.explicit || environment.INFRAWRIGHT_DEPLOYMENT`
// and `selected || path.join(...)` both use `||` (falsy fallback), so an
// explicit-but-empty-string Explicit is treated exactly like an omitted
// one and falls through to the environment variable, and an
// empty-string INFRAWRIGHT_DEPLOYMENT falls through to the cwd-based
// default the same way a missing one would. `options?.cwd ?? process.cwd()`
// uses `??` (nullish fallback) instead: an explicit-but-empty-string Cwd is
// used as-is (path.Join("", "deployment.json") == "deployment.json"), not
// replaced by the working directory the way an omitted Cwd would be. This
// asymmetry (`||` for Explicit/the env var, `??` for Cwd) is a genuine
// parity subtlety in the Node source, not an inconsistency this port
// smooths over.
func DeploymentPath(options DeploymentPathOptions) (string, error) {
	lookupEnv := os.Getenv
	if options.Environment != nil {
		lookupEnv = func(key string) string { return options.Environment[key] }
	}
	var explicit string
	if options.Explicit != nil {
		explicit = *options.Explicit
	}
	selected := explicit
	if selected == "" {
		selected = lookupEnv("INFRAWRIGHT_DEPLOYMENT")
	}
	if selected != "" {
		return selected, nil
	}
	var cwd string
	if options.Cwd != nil {
		cwd = *options.Cwd
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		cwd = wd
	}
	return path.Join(cwd, "deployment.json"), nil
}

// deploymentOverlay ports deploymentOverlay from
// node-src/domain/deployment.ts.
func deploymentOverlay(deployment Deployment) string {
	overlay, ok := deployment.Overlay.(string)
	if !ok {
		malformed("deployment overlay must be a string")
	}
	if overlay == "" {
		return "."
	}
	return overlay
}

// DeploymentOverlay ports deploymentOverlay from
// node-src/domain/deployment.ts.
func DeploymentOverlay(deployment Deployment) (overlay string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentOverlay(deployment), nil
}

// deploymentTfvarsFormat ports deploymentTfvarsFormat from
// node-src/domain/deployment.ts.
func deploymentTfvarsFormat(deployment Deployment) string {
	var value canonjson.Value = "json"
	if deployment.HasTfvarsFormat && deployment.TfvarsFormat != nil {
		value = deployment.TfvarsFormat
	}
	format, ok := value.(string)
	if !ok || (format != "json" && format != "hcl") {
		malformed("deployment tfvars_format must be 'json' or 'hcl'")
	}
	return format
}

// DeploymentTfvarsFormat ports deploymentTfvarsFormat from
// node-src/domain/deployment.ts.
func DeploymentTfvarsFormat(deployment Deployment) (format string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentTfvarsFormat(deployment), nil
}

// deploymentModuleDir ports deploymentModuleDir from
// node-src/domain/deployment.ts.
func deploymentModuleDir(deployment Deployment) string {
	if deployment.HasModuleDir {
		moduleDir, ok := deployment.ModuleDir.(string)
		if !ok {
			malformed("deployment module_dir must be a string")
		}
		if len(moduleDir) > 0 {
			return moduleDir
		}
	}
	overlay := deploymentOverlay(deployment)
	if overlay == "." {
		return "modules"
	}
	return path.Join(overlay, "modules", "default")
}

// DeploymentModuleDir ports deploymentModuleDir from
// node-src/domain/deployment.ts.
func DeploymentModuleDir(deployment Deployment) (moduleDir string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentModuleDir(deployment), nil
}

// deploymentTenantRoot ports deploymentTenantRoot from
// node-src/domain/deployment.ts. tenant is accepted (and unused) only to
// mirror that source's signature, which reserves the parameter for a
// future per-tenant overlay override; the Node implementation is
// `deploymentOverlay(deployment)` verbatim, ignoring its own `_tenant`
// parameter the same way.
func deploymentTenantRoot(deployment Deployment, tenant string) string {
	_ = tenant
	return deploymentOverlay(deployment)
}

// DeploymentTenantRoot ports deploymentTenantRoot from
// node-src/domain/deployment.ts.
func DeploymentTenantRoot(deployment Deployment, tenant string) (root string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentTenantRoot(deployment, tenant), nil
}

// deploymentTenantKind identifies which of deploymentTenantPath's three
// sibling directories (config/imports/envs) a call resolves, ported from
// the "config" | "imports" | "envs" literal union
// node-src/domain/deployment.ts's deploymentTenantPath accepts as `kind`.
type deploymentTenantKind string

const (
	tenantKindConfig  deploymentTenantKind = "config"
	tenantKindImports deploymentTenantKind = "imports"
	tenantKindEnvs    deploymentTenantKind = "envs"
)

// deploymentTenantPath ports deploymentTenantPath from
// node-src/domain/deployment.ts.
func deploymentTenantPath(deployment Deployment, tenant string, kind deploymentTenantKind) string {
	relative := path.Join(string(kind), tenant)
	root := deploymentTenantRoot(deployment, tenant)
	if root == "." {
		return relative
	}
	return path.Join(root, relative)
}

// deploymentConfigDir ports deploymentConfigDir from
// node-src/domain/deployment.ts.
func deploymentConfigDir(deployment Deployment, tenant string) string {
	return deploymentTenantPath(deployment, tenant, tenantKindConfig)
}

// DeploymentConfigDir ports deploymentConfigDir from
// node-src/domain/deployment.ts.
func DeploymentConfigDir(deployment Deployment, tenant string) (dir string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentConfigDir(deployment, tenant), nil
}

// deploymentImportsDir ports deploymentImportsDir from
// node-src/domain/deployment.ts.
func deploymentImportsDir(deployment Deployment, tenant string) string {
	return deploymentTenantPath(deployment, tenant, tenantKindImports)
}

// DeploymentImportsDir ports deploymentImportsDir from
// node-src/domain/deployment.ts.
func DeploymentImportsDir(deployment Deployment, tenant string) (dir string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentImportsDir(deployment, tenant), nil
}

// deploymentEnvsDir ports deploymentEnvsDir from
// node-src/domain/deployment.ts.
func deploymentEnvsDir(deployment Deployment, tenant string) string {
	return deploymentTenantPath(deployment, tenant, tenantKindEnvs)
}

// DeploymentEnvsDir ports deploymentEnvsDir from
// node-src/domain/deployment.ts.
func DeploymentEnvsDir(deployment Deployment, tenant string) (dir string, err error) {
	defer recoverProcessFailure(&err)
	return deploymentEnvsDir(deployment, tenant), nil
}

// DeploymentPullsDir ports deploymentPullsDir from
// node-src/domain/deployment.ts. Unlike this package's other DeploymentXxxDir
// accessors, it takes no Deployment (the Node source doesn't either -- the
// pulls tree is not overlay-scoped) and never fails.
func DeploymentPullsDir(tenant string) string {
	return path.Join("pulls", tenant)
}

// Package configcheck holds content validators that range over a deployment's
// own committed tree rather than over pack metadata alone.
//
// The invariant here is a contract: if we manage it, we must be able to see
// it. Committed config means a consumer owns a resource; a fetch block means
// the engine can pull its live state. When the first holds and the second does
// not, the resource is nominally managed and invisible -- and every existing
// check reports healthy, because they all sit above the fetch layer.
//
// Nothing catches that today. check-pack validates each file's shape, and a
// missing fetch block is valid JSON and a valid registry entry, so there is
// nothing malformed to find. The defect exists only in the relationship
// between two files -- committed config on one side, the registry on the other
// -- and no check looks across that boundary. DROPS_CHECK covers the opposite
// direction, where the API grew a field we do not acknowledge; nothing covered
// losing the ability to fetch something we manage.
package configcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// CheckFetchableOptions selects the committed config to range over. An empty
// Tenants enumerates every tenant with a config directory, so coverage grows
// as consumers adopt: a gate that must be extended by hand is a gate that
// silently narrows.
type CheckFetchableOptions struct {
	Workspace  string
	Deployment deployment.Deployment
	Root       metadata.LoadedPackRoot
	Tenants    []string
}

// FetchableViolation is one committed resource type the engine cannot see.
type FetchableViolation struct {
	Tenant string
	Type   string
	Detail string
}

// CheckFetchableResult reports what the check ranged over. Skipped counts
// types that declared themselves unfetchable on purpose.
type CheckFetchableResult struct {
	Tenants []string
	Checked int
	Skipped int
	// OutOfProfile names committed types the active pack profile does not
	// claim. They are reported rather than counted silently: under the full
	// profile a type here is a genuinely orphaned config file worth looking
	// at, and under a reduced profile it is expected.
	OutOfProfile []string
	Violations   []FetchableViolation
}

func checkFailure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

// configExtensions are the committed tfvars spellings. Both are accepted
// regardless of the deployment's configured format, because a tree mid-format
// change still commits config that this invariant applies to.
var configExtensions = []string{".auto.tfvars.json", ".auto.tfvars"}

func resourceTypeFromConfigName(name string) (string, bool) {
	for _, extension := range configExtensions {
		if strings.HasSuffix(name, extension) {
			resourceType := strings.TrimSuffix(name, extension)
			if resourceType == "" {
				return "", false
			}
			return resourceType, true
		}
	}
	return "", false
}

func workspacePath(workspace, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(workspace, candidate)
}

// configuredTenants enumerates the tenants that have a config directory.
func configuredTenants(options CheckFetchableOptions) ([]string, error) {
	if len(options.Tenants) > 0 {
		return canonjson.SortedStrings(append([]string(nil), options.Tenants...)), nil
	}
	// The tenant segment is the last path element and the root above it does
	// not vary by tenant, so a probe resolves the parent directory.
	probe, err := deployment.DeploymentConfigDir(options.Deployment, "__tenant_probe__")
	if err != nil {
		return nil, err
	}
	parent := workspacePath(options.Workspace, filepath.Dir(probe))
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, checkFailure(
			"CONFIG_ROOT_UNREADABLE",
			"unable to enumerate committed config tenants",
			procerr.CategoryIO,
		)
	}
	tenants := make([]string, 0, len(entries))
	for _, entry := range entries {
		// DirEntry.IsDir is false for a symlink to a directory, so testing it
		// alone drops a symlinked tenant without saying so. Silently checking
		// fewer tenants than the consumer has is the failure this check is
		// meant to be immune to, so resolve the link rather than skip it.
		if entry.IsDir() {
			tenants = append(tenants, entry.Name())
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		linkPath := filepath.Join(parent, entry.Name())
		resolved, err := filepath.EvalSymlinks(linkPath)
		if err != nil {
			return nil, checkFailure(
				"CONFIG_ROOT_UNREADABLE",
				"unable to resolve committed config tenant "+entry.Name(),
				procerr.CategoryIO,
			)
		}
		// A link pointing outside the config root would make the result depend
		// on the machine: the same commit passes where the target exists and
		// fails, or reads different config, elsewhere. This check's value is
		// that it reads only the consumer's own tree, so an escaping link is
		// refused rather than followed -- and refused rather than skipped,
		// because skipping is the silent narrowing the link handling exists to
		// prevent.
		if !configRootContains(parent, resolved) {
			return nil, checkFailure(
				"CONFIG_TENANT_OUTSIDE_ROOT",
				"committed config tenant "+entry.Name()+
					" is a symlink resolving outside the config root, so what this"+
					" check reads would depend on the machine it runs on",
				procerr.CategoryDomain,
			)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, checkFailure(
				"CONFIG_ROOT_UNREADABLE",
				"unable to resolve committed config tenant "+entry.Name(),
				procerr.CategoryIO,
			)
		}
		if info.IsDir() {
			tenants = append(tenants, entry.Name())
		}
	}
	return canonjson.SortedStrings(tenants), nil
}

// configRootContains reports whether resolved lies within root. Both sides are
// resolved first so a symlinked config root itself stays supported: it is the
// escape that matters, not the presence of links on the path.
func configRootContains(root, resolved string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// fetchDeclaration reports whether a registry entry says the engine can fetch
// the type, and the reason when it deliberately cannot.
//
// A derived type needs no written reason. Its registry entry already says its
// config is produced from another resource's data, so it has no API object of
// its own to fetch -- the structural fact is already stated, and demanding a
// redundant sentence for it is the ceremony that makes gates get disabled. The
// written reason is required exactly where the registry is otherwise
// ambiguous: a generate-only type with no derive block looks identical to one
// whose fetch block was dropped.
// The derive exemption has to resolve, not merely be present. Runtime fetch
// selection maps a derived type to derive.from and requires that source to
// carry a fetch entry (collectors.SelectFetchResources); a derive block with no
// from, or one naming a type that is absent or itself unfetchable, sends the
// selector to unknown. Accepting the block unresolved would let the check pass
// while make fetch fails -- recreating the blindness it exists to prevent, and
// with a declaration standing where the gap used to be.
func fetchDeclaration(
	resource metadata.LoadedResourceMetadata,
	resources map[string]metadata.LoadedResourceMetadata,
) (fetchable bool, skipReason string, declared bool, detail string) {
	// Direct fetch outranks derive, because runtime selection resolves it
	// first: SelectFetchResources tests fetchableSet before it looks at a
	// derive block. Metadata permits an entry carrying both, so checking
	// derive first would refuse a type whose own fetch block works -- a
	// refusal with no operational counterpart, which is the kind that gets a
	// gate switched off.
	if canonjson.IsJSONRecord(resource.Registry["fetch"]) {
		return true, "", true, ""
	}
	if derive, isRecord := resource.Registry["derive"].(map[string]any); isRecord {
		from, ok := derive["from"].(string)
		if !ok || from == "" {
			return false, "", true,
				"derive block has no from; it cannot resolve to a fetchable source"
		}
		source, known := resources[from]
		if !known {
			return false, "", true,
				"derives from " + from + ", which has no registry entry"
		}
		if !canonjson.IsJSONRecord(source.Registry["fetch"]) {
			return false, "", true,
				"derives from " + from + ", which has no fetch block of its own"
		}
		return false, "derived from " + from + "; no API object of its own", true, ""
	}
	value, present := resource.Registry["fetch"]
	if !present {
		return false, "", false, ""
	}
	if canonjson.IsJSONRecord(value) {
		return true, "", true, ""
	}
	if allowed, isBool := value.(bool); isBool && !allowed {
		reason, _ := resource.Registry["fetch_skip_reason"].(string)
		return false, reason, true, ""
	}
	return false, "", false, ""
}

// CheckFetchable asserts that every committed resource type can be fetched.
//
// It reads only files already in the consumer's tree: no network, no upstream
// clone, no pinned ref. A gate that needs a fetch to pass can block a consumer
// for reasons unrelated to correctness; this one cannot.
func CheckFetchable(options CheckFetchableOptions) (CheckFetchableResult, error) {
	tenants, err := configuredTenants(options)
	if err != nil {
		return CheckFetchableResult{}, err
	}
	result := CheckFetchableResult{
		Tenants:      tenants,
		OutOfProfile: make([]string, 0),
		Violations:   make([]FetchableViolation, 0),
	}
	for _, tenant := range tenants {
		configDir, err := deployment.DeploymentConfigDir(options.Deployment, tenant)
		if err != nil {
			return CheckFetchableResult{}, err
		}
		entries, err := os.ReadDir(workspacePath(options.Workspace, configDir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return CheckFetchableResult{}, checkFailure(
				"CONFIG_DIR_UNREADABLE",
				"unable to read committed config for tenant "+tenant,
				procerr.CategoryIO,
			)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		// The data-referent lane publishes its config one level down, in
		// <configDir>/data/; a committed nested config is held to the same
		// fetchability contract as a flat one, so enumerate that directory
		// too. Anything else nested (lookups/, unknown dirs) stays ignored.
		dataEntries, err := os.ReadDir(workspacePath(options.Workspace, filepath.Join(configDir, "data")))
		if err != nil && !os.IsNotExist(err) {
			return CheckFetchableResult{}, checkFailure(
				"CONFIG_DIR_UNREADABLE",
				"unable to read committed data-referent config for tenant "+tenant,
				procerr.CategoryIO,
			)
		}
		for _, entry := range dataEntries {
			if !entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		// One committed resource type is one check, wherever its config
		// sits: during the flat-to-data/ migration window a stale flat file
		// and the nested one coexist (in either tfvars format), and both
		// resolve to the same type. Checked/Skipped count types and a
		// FetchableViolation names a type, so a second sighting is skipped.
		checkedTypes := map[string]bool{}
		for _, name := range names {
			resourceType, ok := resourceTypeFromConfigName(name)
			if !ok {
				continue
			}
			if checkedTypes[resourceType] {
				continue
			}
			checkedTypes[resourceType] = true
			resource, known := options.Root.Resources[resourceType]
			if !known {
				// The active pack profile does not claim this type, so there
				// is no fetch declaration to hold it to. Consumers run reduced
				// profiles deliberately, and CI runs every profile against the
				// same committed tree; treating out-of-profile as blindness
				// made this check fail wherever the profile was narrower than
				// the config, which is most of them.
				//
				// The incident this exists for is unaffected: a dropped fetch
				// block leaves the entry in place, so the type is still in
				// profile and still checked.
				result.OutOfProfile = append(result.OutOfProfile, resourceType)
				continue
			}
			result.Checked++
			fetchable, skipReason, declared, detail := fetchDeclaration(
				resource, options.Root.Resources,
			)
			if fetchable {
				continue
			}
			if declared && skipReason != "" {
				result.Skipped++
				continue
			}
			if detail == "" {
				detail = "registry entry has no fetch block; " +
					"add one, or declare \"fetch\": false with a fetch_skip_reason"
			}
			result.Violations = append(result.Violations, FetchableViolation{
				Tenant: tenant, Type: resourceType, Detail: detail,
			})
		}
	}
	return result, nil
}

// FetchableFailure renders the violations as one refusal naming every type and
// tenant, so a consumer sees the whole gap rather than the first instance.
func FetchableFailure(result CheckFetchableResult) error {
	if len(result.Violations) == 0 {
		return nil
	}
	lines := make([]string, 0, len(result.Violations))
	for _, violation := range result.Violations {
		lines = append(lines, fmt.Sprintf(
			"%s/%s: %s", violation.Tenant, violation.Type, violation.Detail,
		))
	}
	return checkFailure(
		"COMMITTED_CONFIG_NOT_FETCHABLE",
		fmt.Sprintf(
			"%d committed resource type(s) cannot be fetched, so drift cannot see them: %s",
			len(result.Violations),
			strings.Join(lines, "; "),
		),
		procerr.CategoryDomain,
	)
}

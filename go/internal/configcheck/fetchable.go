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
	Tenants    []string
	Checked    int
	Skipped    int
	Violations []FetchableViolation
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
	// Generated sidecars sit in the same directory and are not committed
	// config for a resource type.
	if strings.HasSuffix(name, ".generated.expressions.json") {
		return "", false
	}
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
		if entry.IsDir() {
			tenants = append(tenants, entry.Name())
		}
	}
	return canonjson.SortedStrings(tenants), nil
}

// fetchDeclaration reports whether a registry entry says the engine can fetch
// the type, and the reason when it deliberately cannot.
func fetchDeclaration(resource metadata.LoadedResourceMetadata) (fetchable bool, skipReason string, declared bool) {
	value, present := resource.Registry["fetch"]
	if !present {
		return false, "", false
	}
	if canonjson.IsJSONRecord(value) {
		return true, "", true
	}
	if allowed, isBool := value.(bool); isBool && !allowed {
		reason, _ := resource.Registry["fetch_skip_reason"].(string)
		return false, reason, true
	}
	return false, "", false
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
		Tenants:    tenants,
		Violations: make([]FetchableViolation, 0),
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
		sort.Strings(names)
		for _, name := range names {
			resourceType, ok := resourceTypeFromConfigName(name)
			if !ok {
				continue
			}
			result.Checked++
			resource, known := options.Root.Resources[resourceType]
			if !known {
				result.Violations = append(result.Violations, FetchableViolation{
					Tenant: tenant, Type: resourceType,
					Detail: "committed config has no registry entry",
				})
				continue
			}
			fetchable, skipReason, declared := fetchDeclaration(resource)
			if fetchable {
				continue
			}
			if declared && skipReason != "" {
				result.Skipped++
				continue
			}
			result.Violations = append(result.Violations, FetchableViolation{
				Tenant: tenant, Type: resourceType,
				Detail: "registry entry has no fetch block; " +
					"add one, or declare \"fetch\": false with a fetch_skip_reason",
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

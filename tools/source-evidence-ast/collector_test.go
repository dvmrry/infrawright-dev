package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectMinedProviderPatterns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", `
module github.com/example/terraform-provider-example

require (
	github.com/zscaler/zscaler-sdk-go/v3 v3.1.0
	github.com/shurcooL/githubv4 v0.0.0-20240501000000-deadbeef // indirect
)
`)
	writeFile(t, root, "provider.go", `
package provider

import teams "github.com/example/terraform-provider-example/internal/teams"

var resources = map[string]any{
	"github_repository": resourceGithubRepository(),
	"github_team": teams.ResourceTeam(),
}
`)
	writeFile(t, root, "resource_github_repository.go", `
package provider

import (
	"fmt"
	"net/http"

	locationmanagement "github.com/zscaler/zscaler-sdk-go/v3/zscaler/zia/services/location/locationmanagement"
)

type Resource struct{}

var repoResource = &schema.Resource{
	ReadContext: resourceRepositoryRead,
}

func resourceGithubRepository() any {
	return repoResource
}

func resourceRepositoryRead() {
	repo, _, err := client.Repositories.Get(ctx, owner, repoName)
	req, err := client.NewRequest(
		http.MethodGet,
		fmt.Sprintf("orgs/%s/actions/hosted-runners/%s", orgName, runnerID),
		nil,
	)
	location, _, err := locationmanagement.Get(ctx, service, id)
	_, _, err = githubv4Client.Query(ctx, query, nil)
	_, _, _, _ = repo, req, location, err
}

func (r *Resource) Read() {}
`)
	writeFile(t, root, "vendor/ignored/ignored.go", `
package ignored

func Ignored() {
	client.Should.NotAppear()
}
`)

	report, err := Collect(root)
	if err != nil {
		t.Fatalf("Collect(%q) error = %v, want nil", root, err)
	}

	if report.GoMod == nil || report.GoMod.Module != "github.com/example/terraform-provider-example" {
		t.Fatalf("Collect(%q).GoMod.Module = %#v, want module metadata", root, report.GoMod)
	}
	if len(report.GoMod.Requires) != 2 {
		t.Fatalf("Collect(%q).GoMod.Requires len = %d, want 2", root, len(report.GoMod.Requires))
	}

	assertRegistration(t, report, ResourceRegistration{
		Resource:    "github_repository",
		File:        "provider.go",
		Constructor: "resourceGithubRepository",
	})
	assertRegistration(t, report, ResourceRegistration{
		Resource:    "github_team",
		File:        "provider.go",
		Constructor: "ResourceTeam",
		Package:     "teams",
	})
	assertResourceReference(t, report, ResourceReference{
		Resource: "github_repository",
		File:     "provider.go",
	})
	assertIdentifierReference(t, report, IdentifierReference{
		Name: "resourceGithubRepository",
		File: "provider.go",
	})
	assertReadCallback(t, report, ReadCallback{
		Field:    "ReadContext",
		File:     "resource_github_repository.go",
		Function: "resourceRepositoryRead",
	})
	assertSelectorCall(t, report, "resource_github_repository.go", "resourceRepositoryRead", "client.Repositories.Get")
	assertSelectorCall(t, report, "resource_github_repository.go", "resourceRepositoryRead", "client.NewRequest")
	assertPackageCall(t, report, PackageCall{
		File:       "resource_github_repository.go",
		Function:   "resourceRepositoryRead",
		Symbol:     "locationmanagement.Get",
		Package:    "locationmanagement",
		ImportPath: "github.com/zscaler/zscaler-sdk-go/v3/zscaler/zia/services/location/locationmanagement",
		Method:     "Get",
	})
	assertRawRESTCall(t, report, RawRESTCall{
		File:     "resource_github_repository.go",
		Function: "resourceRepositoryRead",
		Symbol:   "client.NewRequest",
		Method:   "GET",
		Path:     "orgs/%s/actions/hosted-runners/%s",
	})
	assertFunction(t, report, FunctionFact{
		Name:     "Read",
		File:     "resource_github_repository.go",
		Receiver: "Resource",
	})

	for _, call := range report.SelectorCalls {
		if call.Symbol == "client.Should.NotAppear" {
			t.Fatalf("Collect(%q) selector calls include vendor call, want vendor skipped", root)
		}
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("writeFile(%q) mkdir error = %v, want nil", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile(%q) write error = %v, want nil", name, err)
	}
}

func assertRegistration(t *testing.T, report *Report, want ResourceRegistration) {
	t.Helper()
	for _, got := range report.ResourceRegistrations {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).ResourceRegistrations missing %#v; got %#v", report.SourceRoot, want, report.ResourceRegistrations)
}

func assertResourceReference(t *testing.T, report *Report, want ResourceReference) {
	t.Helper()
	for _, got := range report.ResourceReferences {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).ResourceReferences missing %#v; got %#v", report.SourceRoot, want, report.ResourceReferences)
}

func assertIdentifierReference(t *testing.T, report *Report, want IdentifierReference) {
	t.Helper()
	for _, got := range report.IdentifierReferences {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).IdentifierReferences missing %#v; got %#v", report.SourceRoot, want, report.IdentifierReferences)
}

func assertReadCallback(t *testing.T, report *Report, want ReadCallback) {
	t.Helper()
	for _, got := range report.ReadCallbacks {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).ReadCallbacks missing %#v; got %#v", report.SourceRoot, want, report.ReadCallbacks)
}

func assertSelectorCall(t *testing.T, report *Report, file, function, symbol string) {
	t.Helper()
	for _, got := range report.SelectorCalls {
		if got.File == file && got.Function == function && got.Symbol == symbol {
			return
		}
	}
	t.Fatalf("Collect(%q).SelectorCalls missing %s in %s/%s; got %#v", report.SourceRoot, symbol, file, function, report.SelectorCalls)
}

func assertPackageCall(t *testing.T, report *Report, want PackageCall) {
	t.Helper()
	for _, got := range report.PackageCalls {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).PackageCalls missing %#v; got %#v", report.SourceRoot, want, report.PackageCalls)
}

func assertRawRESTCall(t *testing.T, report *Report, want RawRESTCall) {
	t.Helper()
	for _, got := range report.RawRESTCalls {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).RawRESTCalls missing %#v; got %#v", report.SourceRoot, want, report.RawRESTCalls)
}

func assertFunction(t *testing.T, report *Report, want FunctionFact) {
	t.Helper()
	for _, got := range report.Functions {
		if reflect.DeepEqual(got, want) {
			return
		}
	}
	t.Fatalf("Collect(%q).Functions missing %#v; got %#v", report.SourceRoot, want, report.Functions)
}

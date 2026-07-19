package sourceanalysis

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestNewIndexRejectsMalformedCapturedProviderBytes(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{
		ModulePath: "example.invalid/provider",
		Files:      []sourcebind.CapturedFile{{Path: "broken.go", Bytes: []byte("package provider\nfunc broken(")}},
	}}
	if _, err := newIndex(context.Background(), snapshot); err == nil {
		t.Error("newIndex(malformed captured bytes) error = nil, want fail-closed rejection")
	}
}

func TestRegistrationRejectsNestedReadDecoyAndUnsupportedLocalConstructorReturn(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nvar unsupported = createOnly\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"nested\": resourceNested(), \"unsupported\": resourceUnsupported(), \"missing\": resourceMissing(), \"good\": resourceGood()}} }\nfunc resourceNested() *Resource { _ = Resource{ReadContext: decoy}; return &Resource{CreateContext: create} }\nfunc resourceUnsupported() *Resource { return unsupported }\nfunc resourceMissing() *Resource { return absent }\nfunc resourceGood() *Resource { return &Resource{ReadContext: goodRead} }\nfunc goodRead() {}\nfunc decoy() {}\nfunc create() {}\nfunc createOnly() {}")}}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(actual nested/unsupported constructors) error = %v, want nil", err)
	}
	for _, resource := range []string{"nested", "unsupported", "missing"} {
		row := index.resourceRow(context.Background(), resource)
		if row.Classification != contracts.SourceUnresolved || row.ReasonCode == nil || *row.ReasonCode != contracts.ReasonReadCallbackUnresolved {
			t.Errorf("resourceRow(%q) = %#v, want read callback unresolved", resource, row)
		}
	}
	if callback := index.registrations["good"].callback; callback == nil || callback.symbol != "goodRead" {
		t.Errorf("good registration callback = %#v, want goodRead; unsupported row must not abort analysis", callback)
	}
}

func TestRegistrationIndexesUnsupportedAuthoritativeMapValueAsRowUnresolved(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nvar nonConstructor = unsupportedValue\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"unsupported\": nonConstructor}} }\nfunc unsupportedValue() {}")}}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v, want nil", err)
	}
	registration := index.registrations["unsupported"]
	if registration == nil || registration.symbol.Symbol != "ResourcesMap[unsupported]" {
		t.Fatalf("unsupported map registration = %#v, want indexed authoritative key provenance", registration)
	}
	row := index.resourceRow(context.Background(), "unsupported")
	if row.Classification != contracts.SourceUnresolved || row.ReasonCode == nil || *row.ReasonCode != contracts.ReasonReadCallbackUnresolved || row.ProviderRegistration == nil {
		t.Errorf("resourceRow(unsupported map value) = %#v, want row-level read callback unresolved rather than provider source missing", row)
	}
}

func TestIndexRegistrationsAcceptsProviderResourcesMapOnly(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nvar unrelated = map[string]*Resource{\"ignored\": resourceIgnored()}\nfunc NotProvider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"decoy\": resourceIgnored()}} }\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"wanted\": resourceWanted()}} }\nfunc resourceWanted() *Resource { return &Resource{ReadContext: wantedRead} }\nfunc resourceIgnored() *Resource { return &Resource{ReadContext: ignoredRead} }\nfunc wantedRead(){}\nfunc ignoredRead(){}")}}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(ResourcesMap source) error = %v, want nil", err)
	}
	if _, ok := index.registrations["wanted"]; !ok {
		t.Error("ResourcesMap registration wanted missing")
	}
	if _, ok := index.registrations["ignored"]; ok {
		t.Error("unrelated same-string map was treated as a resource registration")
	}
	if _, ok := index.registrations["decoy"]; ok {
		t.Error("non-Provider ResourcesMap was treated as a resource registration")
	}
}

func TestIndexRegistrationsUsesPluginServeProviderFuncAuthority(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "main.go", Bytes: []byte("package main\nimport ( plugin \"github.com/hashicorp/terraform-plugin-sdk/v2/plugin\"; zpa \"example.invalid/provider/zpa\" )\nfunc main() { plugin.Serve(&plugin.ServeOpts{ProviderFunc: zpa.ZPAProvider}) }")},
		{Path: "zpa/provider.go", Bytes: []byte("package zpa\nimport schema \"example.invalid/framework/schema\"\nvar resources = map[string]*Resource{\"zpa_rule\": resourceRule()}\nfunc ZPAProvider() *schema.Provider { p := &schema.Provider{ResourcesMap: resources}; return p }\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"decoy\": resourceDecoy()}} }")},
		{Path: "zpa/resource.go", Bytes: []byte("package zpa\nvar ruleResource = &Resource{ReadContext: readRule}\nfunc resourceRule() *Resource { return ruleResource }\nfunc resourceDecoy() *Resource { return &Resource{ReadContext: readDecoy} }\nfunc readRule() {}\nfunc readDecoy() {}")},
	}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(plugin Serve provider authority) error = %v, want nil", err)
	}
	registration := index.registrations["zpa_rule"]
	if registration == nil {
		t.Fatal("zpa Provider ResourcesMap registration missing")
	}
	if registration.symbol.PackagePath != "example.invalid/provider/zpa" || registration.callback == nil || registration.callback.packagePath != "example.invalid/provider/zpa" || registration.callback.symbol != "readRule" {
		t.Errorf("zpa registration = %#v callback=%#v, want zpa-local callback", registration, registration.callback)
	}
	if _, ok := index.registrations["decoy"]; ok {
		t.Error("unbound zpa.Provider() was selected despite plugin Serve ZPAProvider authority")
	}
}

func TestIndexRegistrationsRejectsUnsafeFactoryLocalProviderBindings(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "reassigned", body: "p := &ProviderType{ResourcesMap: map[string]*Resource{\"wrong\": resourceWrong()}}; p = &ProviderType{ResourcesMap: map[string]*Resource{\"also_wrong\": resourceWrong()}}; return p"},
		{name: "conditional", body: "if true { p := &ProviderType{ResourcesMap: map[string]*Resource{\"wrong\": resourceWrong()}}; _ = p }; return p"},
		{name: "nonliteral", body: "p := buildProvider(); return p"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package provider\nfunc Provider() *ProviderType { " + test.body + " }\nfunc resourceWrong() *Resource { return &Resource{ReadContext: readWrong} }\nfunc readWrong() {}\nfunc buildProvider() *ProviderType { return &ProviderType{} }"
			snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(source)}}}}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v", err)
			}
			if len(index.registrations) != 0 {
				t.Errorf("registrations = %#v, want no guessed registrations from %s local binding", index.registrations, test.name)
			}
		})
	}
}

func TestIndexRegistrationsRejectsResourcesWithoutFactoryAuthority(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "provider.go", Bytes: []byte("package provider\nvar resources = map[string]*Resource{\"fallback\": resourceFallback()}\nfunc resourceFallback() *Resource { return &Resource{ReadContext: readFallback} }\nfunc readFallback() {}")},
		{Path: "zpa/provider.go", Bytes: []byte("package zpa\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"decoy\": resourceDecoy()}} }\nfunc resourceDecoy() *Resource { return &Resource{ReadContext: readDecoy} }\nfunc readDecoy() {}")},
	}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(subpackage Provider decoy) error = %v, want nil", err)
	}
	if len(index.registrations) != 0 {
		t.Errorf("registrations = %#v, want no registrations without plugin Serve or root Provider authority", index.registrations)
	}
}

func TestIndexRegistrationsIgnoresDeadPluginServeAuthority(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "main.go", Bytes: []byte("package main\nimport ( plugin \"github.com/hashicorp/terraform-plugin-sdk/v2/plugin\"; zpa \"example.invalid/provider/zpa\" )\nfunc main() { if false { plugin.Serve(&plugin.ServeOpts{ProviderFunc: zpa.ZPAProvider}) } }\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"root\": resourceRoot()}} }\nfunc resourceRoot() *Resource { return &Resource{ReadContext: readRoot} }\nfunc readRoot() {}")},
		{Path: "zpa/provider.go", Bytes: []byte("package zpa\nfunc ZPAProvider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"dead\": resourceDead()}} }\nfunc resourceDead() *Resource { return &Resource{ReadContext: readDead} }\nfunc readDead() {}")},
	}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(dead plugin Serve) error = %v, want nil", err)
	}
	if _, ok := index.registrations["root"]; !ok {
		t.Error("root Provider authority missing after dead plugin.Serve was ignored")
	}
	if _, ok := index.registrations["dead"]; ok {
		t.Error("constant-false plugin.Serve established an authority")
	}
}

func TestIndexRegistrationsRejectsMultiplePluginServeAuthorities(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "main.go", Bytes: []byte("package main\nimport ( plugin \"github.com/hashicorp/terraform-plugin-sdk/v2/plugin\"; zpa \"example.invalid/provider/zpa\"; zia \"example.invalid/provider/zia\" )\nfunc main() { plugin.Serve(&plugin.ServeOpts{ProviderFunc: zpa.ZPAProvider}); plugin.Serve(&plugin.ServeOpts{ProviderFunc: zia.ZIAProvider}) }")},
		{Path: "zpa/provider.go", Bytes: []byte("package zpa\nfunc ZPAProvider() *ProviderType { return &ProviderType{} }")},
		{Path: "zia/provider.go", Bytes: []byte("package zia\nfunc ZIAProvider() *ProviderType { return &ProviderType{} }")},
	}}}
	if _, err := newIndex(context.Background(), snapshot); err == nil {
		t.Error("newIndex(multiple plugin Serve authorities) error = nil, want fail-closed rejection")
	}
}

func TestTraceIgnoresTypedTerraformFrameworkSelectors(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nimport ( schema \"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema\"; sdk \"example.invalid/sdk/client\" )\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing(d *schema.ResourceData, client *sdk.Client) { d.Get(\"id\"); d.Set(\"id\", \"x\"); client.Get() }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\ntype Client struct{}\nfunc (c *Client) Get() {}")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(framework selectors) error = %v, want nil", err)
	}
	callback := index.registrations["thing"].callback
	chains := (traceState{ctx: context.Background(), index: index, current: callback, bools: map[string]bool{}}).trace()
	if len(chains) != 1 || chains[0].SDKCall == nil || len(chains[0].Steps) != 1 || chains[0].Steps[0].Kind != contracts.CallSDKReceiverMethod {
		t.Errorf("trace(framework selectors around SDK call) = %#v, want only the captured SDK chain", chains)
	}
}

func TestUnboundExternalCallsRemainAmbiguousAlongsideValidEndpoint(t *testing.T) {
	for _, test := range []struct {
		name string
		read string
	}{
		{name: "package function", read: "external.Lookup(); sdk.Get(client)"},
		{name: "typed receiver", read: "externalClient.Lookup(); sdk.Get(client)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := "package provider\nimport ( external \"example.invalid/external/client\"; sdk \"example.invalid/sdk/client\" )\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing("
			if test.name == "typed receiver" {
				provider += "externalClient *external.Client, "
			}
			provider += "client *sdk.Client) { " + test.read + " }"
			snapshot := sourcebind.VerifiedSnapshot{
				Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(provider)}}},
				SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nimport \"net/http\"\ntype Client struct{}\nfunc Get(client *Client) { client.NewRequest(\"GET\", \"/v1/things\", nil) }\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")}}}},
			}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v", err)
			}
			row := index.resourceRow(context.Background(), "thing")
			if row.Classification != contracts.SourceAmbiguous || row.ReasonCode == nil || *row.ReasonCode != contracts.ReasonMultipleCandidates || len(row.Chains) != 2 {
				t.Fatalf("resourceRow() = %#v, want ambiguous unresolved external plus valid endpoint", row)
			}
			var unresolved, endpoint bool
			for _, chain := range row.Chains {
				unresolved = unresolved || chainHasKind(chain, contracts.CallUnresolvedDispatch)
				endpoint = endpoint || chain.Endpoint != nil
			}
			if !unresolved || !endpoint {
				t.Errorf("resourceRow() = %#v, want both unresolved external and endpoint chains retained", row)
			}
		})
	}
}

func TestNestedArgumentCallsRemainIndependentCandidates(t *testing.T) {
	for _, test := range []struct {
		name                 string
		read                 string
		externalReceiverType string
	}{
		{name: "bound outer", read: "sdk.Get(external.Lookup(), client)"},
		{name: "stdlib outer", read: "fmt.Sprint(external.Lookup()); sdk.Get(client)"},
		{name: "typed receiver", read: "sdk.Get(externalClient.Lookup(), client)", externalReceiverType: "*external.Client"},
		{name: "external interface receiver", read: "sdk.Get(externalClient.Lookup(), client)", externalReceiverType: "external.Client"},
		{name: "bound SDK closure gap", read: "sdk.Get(sdk.Missing(client), client)"},
		{name: "unbound imported factory chain", read: "sdk.Get(external.NewClient().Lookup(), client)"},
		{name: "unbound imported receiver chain", read: "sdk.Get(external.Lookup().ID(), client)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := "package provider\nimport ( external \"example.invalid/external/client\"; sdk \"example.invalid/sdk/client\"; \"fmt\" )\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing("
			if test.externalReceiverType != "" {
				provider += "externalClient " + test.externalReceiverType + ", "
			}
			provider += "client *sdk.Client) { " + test.read + " }"
			snapshot := sourcebind.VerifiedSnapshot{
				Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(provider)}}},
				SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nimport \"net/http\"\ntype Client struct{}\nfunc Get(_ any, client *Client) { client.NewRequest(\"GET\", \"/v1/things\", nil) }\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")}}}},
			}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v", err)
			}
			row := index.resourceRow(context.Background(), "thing")
			if row.Classification != contracts.SourceAmbiguous || row.ReasonCode == nil || *row.ReasonCode != contracts.ReasonMultipleCandidates || len(row.Chains) != 2 {
				t.Fatalf("resourceRow() = %#v, want nested unbound plus endpoint ambiguity", row)
			}
			var unresolved, endpoint bool
			for _, chain := range row.Chains {
				unresolved = unresolved || chainHasKind(chain, contracts.CallUnresolvedDispatch)
				endpoint = endpoint || chain.Endpoint != nil
			}
			if !unresolved || !endpoint {
				t.Errorf("resourceRow() = %#v, want retained nested unresolved and endpoint chains", row)
			}
		})
	}
}

func TestNestedArgumentResolvedCallsDoNotInflateCandidates(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider string
		want     contracts.SourceClassification
	}{
		{
			name:     "SDK constructor",
			provider: "package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing(client *sdk.Client) { sdk.Get(sdk.NewClient(), client) }",
			want:     contracts.SourceObservedHTTP,
		},
		{
			name:     "SDK factory receiver",
			provider: "package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing() { sdk.NewClient().Read() }",
			want:     contracts.SourceObservedHTTP,
		},
		{
			name:     "local dynamic path helper",
			provider: "package provider\nimport sdk \"example.invalid/sdk/client\"\nvar dynamicPath = func(id string) string { return \"/runtime/\" + id }\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing(client *sdk.Client, id string) { client.NewRequest(\"GET\", dynamicPath(id), nil) }",
			want:     contracts.SourceDynamic,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := sourcebind.VerifiedSnapshot{
				Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(test.provider)}}},
				SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nimport \"net/http\"\ntype Client struct{}\nfunc NewClient() *Client { return &Client{} }\nfunc Get(_ any, client *Client) { client.NewRequest(\"GET\", \"/v1/things\", nil) }\nfunc (client *Client) Read() { client.NewRequest(\"GET\", \"/v1/things\", nil) }\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")}}}},
			}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v", err)
			}
			row := index.resourceRow(context.Background(), "thing")
			if row.Classification != test.want || len(row.Chains) != 1 {
				t.Errorf("resourceRow() = %#v, want one %s candidate", row, test.want)
			}
		})
	}
}

func TestSDKSamePackageHelperCarriesSDKImportPath(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing() { sdk.Top(&sdk.Client{}) }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nimport \"net/http\"\ntype Client struct{}\nfunc Top(client *Client) { helper(client) }\nfunc helper(client *Client) { client.NewRequest(\"GET\", \"/v1/helper\", nil) }\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v", err)
	}
	row := index.resourceRow(context.Background(), "thing")
	if len(row.Chains) != 1 || len(row.Chains[0].Steps) != 3 || row.Chains[0].Endpoint == nil {
		t.Fatalf("resourceRow() = %#v, want three-step SDK helper endpoint chain", row)
	}
	step := row.Chains[0].Steps[1]
	if step.Kind != contracts.CallSDKPackageFunction || step.ImportPath == nil || *step.ImportPath != "example.invalid/sdk/client" || step.Symbol != "helper" {
		t.Errorf("same-package SDK helper step = %#v, want sdk_package_function helper with example.invalid/sdk/client import path", step)
	}
}

func TestSDKInternalUtilitiesDoNotInflateRequestCandidates(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing() { sdk.Get(&sdk.Client{}) }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nimport \"net/http\"\ntype Client struct{}\nfunc Get(client *Client) { client.NewRequestDo(\"GET\", \"/v1/things\", nil); GetCustomerID(); unmarshalRules() }\nfunc GetCustomerID() {}\nfunc unmarshalRules() {}\nfunc (client *Client) NewRequestDo(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v", err)
	}
	row := index.resourceRow(context.Background(), "thing")
	if row.Classification != contracts.SourceObservedHTTP || len(row.Chains) != 1 || row.Chains[0].Endpoint == nil || row.Chains[0].Endpoint.PathTemplate != "/v1/things" {
		t.Errorf("resourceRow(Get with utility helpers) = %#v, want one observed HTTP request candidate", row)
	}
}

func TestProviderToSDKUtilityWithoutRequestRemainsObservedSDKCall(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing() { sdk.Utility() }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nfunc Utility() {}")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v", err)
	}
	row := index.resourceRow(context.Background(), "thing")
	if row.Classification != contracts.SourceObservedSDKCall || len(row.Chains) != 1 || row.Chains[0].SDKCall == nil || row.Chains[0].SDKCall.Symbol != "Utility" {
		t.Errorf("resourceRow(provider SDK utility) = %#v, want terminal observed SDK Utility", row)
	}
}

func TestSDKMultipleRequestBuildersRemainMultipleCandidates(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing() { sdk.Get(&sdk.Client{}) }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\nimport \"net/http\"\ntype Client struct{}\nfunc Get(client *Client) { client.NewRequest(\"GET\", \"/v1/one\", nil); client.NewRequest(\"GET\", \"/v1/two\", nil) }\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v", err)
	}
	row := index.resourceRow(context.Background(), "thing")
	if row.Classification != contracts.SourceAmbiguous || len(row.Chains) != 2 {
		t.Errorf("resourceRow(two SDK request builders) = %#v, want two actual candidates / ambiguous", row)
	}
}

func TestCallbackResolutionSkipsUnselectedUnsupportedConstructors(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Manifest: contracts.SourceProvenance{Selection: contracts.SelectionBinding{ResourceTypes: []string{"chosen"}}},
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"chosen\": resourceChosen(), \"unselected\": resourceBroken()}} }\nfunc resourceChosen() *Resource { return &Resource{ReadContext: readChosen} }\nfunc resourceBroken() *Resource { return &Resource{ReadContext: readOne}; return &Resource{ReadContext: readTwo} }\nfunc readChosen() {}\nfunc readOne() {}\nfunc readTwo() {}")}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(selected callback resolution) error = %v, want unselected unsupported constructor ignored", err)
	}
	if index.registrations["unselected"] == nil {
		t.Fatal("unselected authoritative map entry was not indexed")
	}
	if callback := index.registrations["chosen"].callback; callback == nil || callback.symbol != "readChosen" {
		t.Errorf("chosen callback = %#v, want readChosen", callback)
	}
}

func TestNewIndexRejectsConflictingPackageNamesAndDuplicateConstants(t *testing.T) {
	conflictingNames := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "one.go", Bytes: []byte("package provider")}, {Path: "two.go", Bytes: []byte("package other")}}}}
	if _, err := newIndex(context.Background(), conflictingNames); err == nil {
		t.Error("newIndex(conflicting package names) error = nil, want rejection")
	}
	duplicateConstants := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "one.go", Bytes: []byte("package provider\nconst prefix = \"/v1\"")}, {Path: "two.go", Bytes: []byte("package provider\nconst prefix = \"/v1\"")}}}}
	if _, err := newIndex(context.Background(), duplicateConstants); err == nil {
		t.Error("newIndex(duplicate same-value constants) error = nil, want rejection")
	}
}

func TestRawPathTemplatesRequireStaticShellAndSupportMultiplePercentS(t *testing.T) {
	request := certifiedRequest(t)
	read := parseFunction(t, "package provider\nimport \"fmt\"\nfunc read(client *Client, first, second string) { client.NewRequest(\"GET\", fmt.Sprintf(\"%s/%s\", first, second), nil) }")
	read.file.imports["fmt"] = "fmt"
	index := &analysisIndex{functions: map[string]*function{functionKey("example.invalid/provider", "(*Client).NewRequest"): request}, constants: map[string]map[string]string{}}
	if raw, _, _, why := (traceState{current: read, index: index}).raw(firstCall(t, read.decl.Body)); !raw || why != contracts.ReasonDynamicPath {
		t.Errorf("raw(placeholder-only fmt.Sprintf) = raw %t reason %q, want dynamic path", raw, why)
	}
	read = parseFunction(t, "package provider\nimport \"fmt\"\nfunc read(client *Client, first, second string) { client.NewRequest(\"GET\", fmt.Sprintf(\"/v1/%s/%s\", first, second), nil) }")
	read.file.imports["fmt"] = "fmt"
	raw, method, template, why := (traceState{current: read, index: index}).raw(firstCall(t, read.decl.Body))
	if !raw || why != "" || method != "GET" || template != "/v1/{first}/{second}" {
		t.Errorf("raw(static multi-placeholder fmt.Sprintf) = raw %t method %q template %q reason %q, want GET /v1/{first}/{second}", raw, method, template, why)
	}
	read = parseFunction(t, "package provider\nimport \"fmt\"\nfunc read(client *Client, first, second string) { client.NewRequest(\"GET\", fmt.Sprintf(\"/v1/%v/%s\", first, second), nil) }")
	read.file.imports["fmt"] = "fmt"
	raw, method, template, why = (traceState{current: read, index: index}).raw(firstCall(t, read.decl.Body))
	if !raw || why != "" || method != "GET" || template != "/v1/{first}/{second}" {
		t.Errorf("raw(static %%v/%%s fmt.Sprintf) = raw %t method %q template %q reason %q, want GET /v1/{first}/{second}", raw, method, template, why)
	}
	for _, format := range []string{"/v1/%02s", "/v1/%[1]s", "/v1/%q"} {
		read = parseFunction(t, "package provider\nimport \"fmt\"\nfunc read(client *Client, first string) { client.NewRequest(\"GET\", fmt.Sprintf(\""+format+"\", first), nil) }")
		read.file.imports["fmt"] = "fmt"
		if raw, _, _, why = (traceState{current: read, index: index}).raw(firstCall(t, read.decl.Body)); !raw || why != contracts.ReasonDynamicPath {
			t.Errorf("raw(rejected fmt format %q) = raw %t reason %q, want dynamic path", format, raw, why)
		}
	}
	read = parseFunction(t, "package provider\nimport \"fmt\"\nfunc read(client *Client, first string) { client.NewRequest(\"GET\", fmt.Sprintf(\"/v1/%s\", first.String()), nil) }")
	read.file.imports["fmt"] = "fmt"
	if raw, _, _, why = (traceState{current: read, index: index}).raw(firstCall(t, read.decl.Body)); !raw || why != contracts.ReasonDynamicPath {
		t.Errorf("raw(fmt getter argument) = raw %t reason %q, want dynamic path", raw, why)
	}
}

func TestExpressionUsesConstOrParameterNotArbitraryIdentifier(t *testing.T) {
	fn := parseFunction(t, "package provider\nconst prefix = \"/v1\"\nfunc read(id string) {}")
	state := traceState{current: fn, index: &analysisIndex{constants: map[string]map[string]string{"example.invalid/provider": {"prefix": "/v1"}}}}
	if got, ok := state.expression(&ast.Ident{Name: "prefix"}, nil); !ok || got != "/v1" {
		t.Errorf("expression(const) = %q, %t; want /v1, true", got, ok)
	}
	if got, ok := state.expression(&ast.Ident{Name: "id"}, nil); !ok || got != "{id}" {
		t.Errorf("expression(parameter) = %q, %t; want {id}, true", got, ok)
	}
	if _, ok := state.expression(&ast.Ident{Name: "unknown"}, nil); ok {
		t.Error("expression(arbitrary identifier) succeeded, want unresolved")
	}
}

func TestLocalPathBindingIsSingleAssignmentOnly(t *testing.T) {
	fn := parseFunction(t, "package provider\nfunc read(client *Client, id string) { path := \"/v1/\" + id; client.NewRequest(\"GET\", path, nil) }")
	state := traceState{current: fn, index: &analysisIndex{constants: map[string]map[string]string{}}}
	bindings := state.localPathBindings(firstCall(t, fn.decl.Body).Pos())
	if got := bindings["path"]; got != "/v1/{id}" {
		t.Errorf("localPathBindings() path = %q, want /v1/{id}", got)
	}
	reassigned := parseFunction(t, "package provider\nfunc read(client *Client, id string) { path := \"/v1/\" + id; path = \"/v2/\" + id; client.NewRequest(\"GET\", path, nil) }")
	if _, ok := (traceState{current: reassigned, index: &analysisIndex{constants: map[string]map[string]string{}}}).localPathBindings(firstCall(t, reassigned.decl.Body).Pos())["path"]; ok {
		t.Error("localPathBindings(reassigned) retained a dynamic binding")
	}
}

func TestSummaryZeroDenominatorIsNotApplicable(t *testing.T) {
	got := summary(map[string]contracts.SourceEvidenceRow{"reviewed": {Classification: contracts.SourceNotApplicable}})
	if got.EndpointCoverage.State != contracts.CoverageNotApplicable || got.EndpointCoverage.Denominator != 0 {
		t.Errorf("summary(all not applicable).EndpointCoverage = %#v, want not_applicable / 0", got.EndpointCoverage)
	}
}

func TestExactHTTPMethodRejectsPrefixLookalike(t *testing.T) {
	if exactHTTPMethod("MethodGet") == false || exactHTTPMethod("MethodGetSomething") {
		t.Error("exactHTTPMethod did not enforce the closed net/http constant set")
	}
}

func TestMissingSDKRequiresExplicitUnavailableManifestAuthorization(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "read.go", Bytes: []byte("package provider\nimport missing \"example.invalid/missing/client\"\nfunc Read() { missing.Get() }\n")},
	}}, ProviderModule: []sourcebind.CapturedFile{{Path: "go.mod", Bytes: []byte("module example.invalid/provider\nrequire example.invalid/missing v1.2.3\n")}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(unlisted missing SDK) error = %v, want nil", err)
	}
	fn := index.providerFunctions[functionKey("example.invalid/provider", "Read")]
	call := firstCall(t, fn.decl.Body)
	_, kind, imported, _, _, unresolved := traceState{current: fn, index: index, bools: map[string]bool{}}.resolveCall(call)
	if !unresolved || kind != "" || imported != "" {
		t.Errorf("resolveCall(unlisted go.mod dependency) = kind %q import %q unresolved %t, want visible unresolved dispatch", kind, imported, unresolved)
	}
	snapshot.Manifest.UnavailableSDKs = []contracts.UnavailableSDKBinding{{ModulePath: "example.invalid/missing", ModuleVersion: "v1.2.3"}}
	index, err = newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(listed unavailable SDK) error = %v, want nil", err)
	}
	fn = index.providerFunctions[functionKey("example.invalid/provider", "Read")]
	call = firstCall(t, fn.decl.Body)
	_, kind, imported, _, _, unresolved = traceState{current: fn, index: index, bools: map[string]bool{}}.resolveCall(call)
	if unresolved || kind != contracts.CallSDKSourceMissing || imported != "example.invalid/missing/client" {
		t.Errorf("resolveCall(explicit unavailable SDK) = kind %q import %q unresolved %t, want sdk_source_missing example.invalid/missing/client false", kind, imported, unresolved)
	}
}

func TestMissingSDKChainsRemainCanonicalWithoutSuppressingOtherCandidates(t *testing.T) {
	missing := []contracts.UnavailableSDKBinding{{ModulePath: "example.invalid/missing", ModuleVersion: "v1.2.3"}}
	for _, test := range []struct {
		name               string
		readBody           string
		wantClassification contracts.SourceClassification
		wantReason         contracts.SourceReasonCode
		wantChains         int
	}{
		{name: "multiple missing", readBody: "missing.Second(); missing.First()", wantClassification: contracts.SourceNoSource, wantReason: contracts.ReasonSDKSourceMissing, wantChains: 2},
		{name: "mixed missing and raw HTTP", readBody: "missing.First(); client.NewRequest(\"GET\", \"/v1/things\", nil)", wantClassification: contracts.SourceAmbiguous, wantReason: contracts.ReasonMultipleCandidates, wantChains: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package provider\nimport ( missing \"example.invalid/missing/client\"; \"net/http\" )\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\ntype Client struct{}\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }\nfunc readThing(client *Client) { " + test.readBody + " }"
			snapshot := sourcebind.VerifiedSnapshot{Manifest: contracts.SourceProvenance{UnavailableSDKs: missing}, Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(source)}}}}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v, want nil", err)
			}
			row := index.resourceRow(context.Background(), "thing")
			if row.Classification != test.wantClassification || row.ReasonCode == nil || *row.ReasonCode != test.wantReason || len(row.Chains) != test.wantChains {
				t.Fatalf("resourceRow() = %#v, want %q/%q with %d canonical chains", row, test.wantClassification, test.wantReason, test.wantChains)
			}
			if test.wantClassification == contracts.SourceNoSource {
				for _, chain := range row.Chains {
					if chain.Endpoint != nil || chain.SDKCall != nil || chain.ReasonCode == nil || *chain.ReasonCode != contracts.ReasonSDKSourceMissing {
						t.Errorf("missing result chain = %#v, want terminal missing-only chain", chain)
					}
				}
			} else {
				var missingChain, endpointChain bool
				for _, chain := range row.Chains {
					missingChain = missingChain || (chain.ReasonCode != nil && *chain.ReasonCode == contracts.ReasonSDKSourceMissing && chainHasKind(chain, contracts.CallSDKSourceMissing))
					endpointChain = endpointChain || chain.Endpoint != nil
				}
				if !missingChain || !endpointChain {
					t.Errorf("mixed row = %#v, want both authorized missing and endpoint chains", row)
				}
			}
			if test.wantChains == 2 {
				first, _ := chainKey(row.Chains[0])
				second, _ := chainKey(row.Chains[1])
				if first >= second {
					t.Errorf("missing chains are not canonical: %q >= %q", first, second)
				}
			}
		})
	}
}

func TestRawSymbolUsesResolvedDeclarationPackage(t *testing.T) {
	read := parseFunction(t, "package provider\nfunc Read(client *transport.Client) { client.NewRequest(\"GET\", \"/v1\", nil) }")
	read.file.imports["transport"] = "example.invalid/provider/internal/transport"
	request := parseFunction(t, "package transport\nfunc (client *Client) NewRequest(method, path string, body any) {}")
	request.packagePath = "example.invalid/provider/internal/transport"
	request.file.packagePath = request.packagePath
	request.receiver = "Client"
	state := traceState{current: read, index: &analysisIndex{functions: map[string]*function{functionKey(request.packagePath, "(*Client).NewRequest"): request}}}
	if got := state.rawSymbol(firstCall(t, read.decl.Body)); got != "(*transport.Client).NewRequest" {
		t.Errorf("rawSymbol(non-fixture declaration) = %q, want (*transport.Client).NewRequest", got)
	}
}

func TestRawRequestResolvesCapturedStructFieldSelections(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "read.go", Bytes: []byte("package provider\nimport ( \"context\"; zscaler \"example.invalid/sdk/zscaler\" )\nfunc Read(service *zscaler.Service) { service.Client.NewRequestDo(context.Background(), \"GET\", \"/v1/things\", nil) }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "zscaler/client.go", Bytes: []byte("package zscaler\nimport ( \"context\"; \"net/http\" )\ntype Service struct { Client *Client }\ntype Client struct{}\nfunc (client *Client) NewRequestDo(ctx context.Context, method, path string, body any) { _, _ = http.NewRequestWithContext(ctx, method, path, nil) }")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(captured struct fields) error = %v, want nil", err)
	}
	read := index.providerFunctions[functionKey("example.invalid/provider", "Read")]
	call := firstCall(t, read.decl.Body)
	raw, method, template, why := (traceState{current: read, index: index, bools: map[string]bool{}}).raw(call)
	if !raw || why != "" || method != "GET" || template != "/v1/things" {
		t.Fatalf("raw(service.Client.NewRequestDo) = raw %t method %q path %q reason %q, want exact SDK raw request", raw, method, template, why)
	}
	if got := (traceState{current: read, index: index}).rawSymbol(call); got != "(*zscaler.Client).NewRequestDo" {
		t.Errorf("rawSymbol(service.Client.NewRequestDo) = %q, want exact SDK declaration package", got)
	}
}

func TestNewRequestInterfaceDispatchIsDynamic(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\ntype Requester interface { NewRequest(string, string, any) }\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing(client Requester) { client.NewRequest(\"GET\", \"/v1/things\", nil) }")}}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex(interface request) error = %v, want nil", err)
	}
	row := index.resourceRow(context.Background(), "thing")
	if row.Classification != contracts.SourceDynamic || len(row.Chains) != 1 || row.Chains[0].ReasonCode == nil || *row.Chains[0].ReasonCode != contracts.ReasonDynamicDispatch || row.Chains[0].Steps[len(row.Chains[0].Steps)-1].Kind != contracts.CallUnresolvedDispatch {
		t.Errorf("resourceRow(NewRequest interface dispatch) = %#v, want dynamic dispatch", row)
	}
}

func TestRawRequestRequiresResolvedNewRequestDeclaration(t *testing.T) {
	fn := parseFunction(t, "package provider\nfunc read(client *Thing) { client.NewRequest(\"GET\", \"/forged\", nil) }")
	call := firstCall(t, fn.decl.Body)
	state := traceState{current: fn, index: &analysisIndex{functions: map[string]*function{}}, bools: map[string]bool{}}
	if raw, _, _, _ := state.raw(call); raw {
		t.Error("raw(unrelated Thing.NewRequest) = true, want false without a resolved declaration")
	}
}

func TestRawRequestRejectsNonClientReceiverEvenWithMatchingMethod(t *testing.T) {
	read := parseFunction(t, "package provider\nfunc read(widget *Widget) { widget.NewRequest(\"GET\", \"/forged\", nil) }")
	request := parseFunction(t, "package provider\nfunc (widget *Widget) NewRequest(method, path string, body any) {}")
	index := &analysisIndex{functions: map[string]*function{functionKey("example.invalid/provider", "(*Widget).NewRequest"): request}}
	state := traceState{current: read, index: index, bools: map[string]bool{}}
	if raw, _, _, _ := state.raw(firstCall(t, read.decl.Body)); raw {
		t.Error("raw(Widget.NewRequest) = true, want Client-only allowlist rejection")
	}
}

func TestRawRequestRequiresDirectNetHTTPSink(t *testing.T) {
	read := parseFunction(t, "package provider\nfunc read(client *Client) { client.NewRequest(\"GET\", \"/forged\", nil) }")
	empty := parseFunction(t, "package provider\nfunc (client *Client) NewRequest(method, path string, body any) {}")
	empty.receiver, empty.symbol = "Client", "(*Client).NewRequest"
	custom := parseFunction(t, "package provider\nfunc (client *Client) NewRequest(method, path string, body any) { _ = method; _ = path }")
	custom.receiver, custom.symbol = "Client", "(*Client).NewRequest"
	for name, request := range map[string]*function{"empty": empty, "custom": custom} {
		t.Run(name, func(t *testing.T) {
			index := &analysisIndex{functions: map[string]*function{functionKey("example.invalid/provider", "(*Client).NewRequest"): request}, constants: map[string]map[string]string{}}
			if raw, _, _, _ := (traceState{current: read, index: index, bools: map[string]bool{}}).raw(firstCall(t, read.decl.Body)); raw {
				t.Fatalf("raw(%s NewRequest) = true, want direct net/http sink rejection", name)
			}
			chains := (traceState{ctx: context.Background(), current: read, index: index, bools: map[string]bool{}}).trace()
			if len(chains) != 1 || chains[0].Endpoint != nil || chains[0].ReasonCode == nil || *chains[0].ReasonCode != contracts.ReasonCallChainUnresolved {
				t.Fatalf("trace(%s provider builder) = %#v, want one unresolved non-endpoint chain", name, chains)
			}
		})
	}
}

func TestRawRequestAcceptsDirectNetHTTPContextSink(t *testing.T) {
	read := parseFunction(t, "package provider\nfunc read(client *Client) { client.NewRequestDo(nil, \"GET\", \"/v1/things\", nil) }")
	request := parseFunction(t, "package provider\nimport (\"context\"; \"net/http\")\nfunc (client *Client) NewRequestDo(ctx context.Context, method, path string, body any) { _, _ = http.NewRequestWithContext(ctx, method, path, nil) }")
	request.file.imports["http"] = "net/http"
	request.receiver, request.symbol = "Client", "(*Client).NewRequestDo"
	index := &analysisIndex{functions: map[string]*function{functionKey("example.invalid/provider", "(*Client).NewRequestDo"): request}, constants: map[string]map[string]string{}}
	raw, method, template, why := (traceState{current: read, index: index, bools: map[string]bool{}}).raw(firstCall(t, read.decl.Body))
	if !raw || why != "" || method != "GET" || template != "/v1/things" {
		t.Errorf("raw(direct NewRequestWithContext) = raw %t method %q path %q reason %q, want GET /v1/things", raw, method, template, why)
	}
}

func TestDirectNetHTTPSinkRejectsDeadBranchAndNestedFunction(t *testing.T) {
	for name, source := range map[string]string{
		"dead branch":     "package provider\nimport \"net/http\"\nfunc (client *Client) NewRequest(method, path string, body any) { if false { _, _ = http.NewRequest(method, path, nil) } }",
		"nested function": "package provider\nimport \"net/http\"\nfunc (client *Client) NewRequest(method, path string, body any) { _ = func() { _, _ = http.NewRequest(method, path, nil) } }",
	} {
		t.Run(name, func(t *testing.T) {
			request := parseFunction(t, source)
			request.file.imports["http"] = "net/http"
			if directNetHTTPSink(request) {
				t.Error("directNetHTTPSink() = true, want rejection outside the reachable direct declaration body")
			}
		})
	}
}

func TestUncertifiedCapturedSDKRequestBuilderTerminatesAtSDK(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{
		Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte("package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing(client *sdk.Client) { client.NewRequestDo(\"GET\", \"/v1/things\", nil) }")}}},
		SDKs:     map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\ntype Client struct{}\nfunc (client *Client) NewRequestDo(method, path string, body any) { client.ExecuteRequest(method, path) }")}}}},
	}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v", err)
	}
	row := index.resourceRow(context.Background(), "thing")
	if row.Classification != contracts.SourceObservedSDKCall || len(row.Chains) != 1 || row.Chains[0].SDKCall == nil || row.Chains[0].SDKCall.Symbol != "(*Client).NewRequestDo" || row.Chains[0].ReasonCode == nil || *row.Chains[0].ReasonCode != contracts.ReasonEndpointNotRecovered {
		t.Errorf("resourceRow(uncertified SDK builder) = %#v, want terminal observed SDK NewRequestDo endpoint_not_recovered", row)
	}
}

func TestUnknownIdentifierCallsFailClosedWithoutErasingCandidates(t *testing.T) {
	for _, test := range []struct {
		name           string
		body           string
		classification contracts.SourceClassification
		chains         int
	}{
		{name: "omitted plus SDK is ambiguous", body: "fetchPolicySetIDByType(); client.Get()", classification: contracts.SourceAmbiguous, chains: 2},
		{name: "omitted only is unresolved", body: "fetchPolicySetIDByType()", classification: contracts.SourceUnresolved, chains: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package provider\nimport sdk \"example.invalid/sdk/client\"\nfunc Provider() *ProviderType { return &ProviderType{ResourcesMap: map[string]*Resource{\"thing\": resourceThing()}} }\nfunc resourceThing() *Resource { return &Resource{ReadContext: readThing} }\nfunc readThing(client *sdk.Client) { " + test.body + " }"
			snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(source)}}}, SDKs: map[string]sourcebind.CapturedTree{"example.invalid/sdk": {ModulePath: "example.invalid/sdk", Files: []sourcebind.CapturedFile{{Path: "client/client.go", Bytes: []byte("package client\ntype Client struct{}\nfunc (client *Client) Get() {}")}}}}}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v", err)
			}
			row := index.resourceRow(context.Background(), "thing")
			if row.Classification != test.classification || len(row.Chains) != test.chains {
				t.Fatalf("resourceRow() = %#v, want %q with %d chains", row, test.classification, test.chains)
			}
			found := false
			for _, chain := range row.Chains {
				for _, step := range chain.Steps {
					if step.Kind == contracts.CallUnresolvedDispatch && step.Symbol == "fetchPolicySetIDByType" {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("resourceRow() = %#v, omitted fetchPolicySetIDByType was erased", row)
			}
		})
	}
}

func TestIdentifierBuiltinsAndTypeConversionsAreIgnoredButShadowingIsNot(t *testing.T) {
	for _, test := range []struct {
		name       string
		source     string
		wantChains int
	}{
		{name: "predeclared builtin and type", source: "package provider\nfunc read() { _ = len([]int{}); _ = make([]int, 0); _ = string([]byte{}) }", wantChains: 0},
		{name: "captured package type", source: "package provider\ntype identifier string\nfunc read() { _ = identifier(\"x\") }", wantChains: 0},
		{name: "local type", source: "package provider\nfunc read() { type identifier string; _ = identifier(\"x\") }", wantChains: 0},
		{name: "shadowed builtin", source: "package provider\nfunc read() { len := func() {}; len() }", wantChains: 1},
		{name: "function variable", source: "package provider\nfunc read() { call := func() {}; call() }", wantChains: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{{Path: "provider.go", Bytes: []byte(test.source)}}}}
			index, err := newIndex(context.Background(), snapshot)
			if err != nil {
				t.Fatalf("newIndex() error = %v", err)
			}
			callback := index.providerFunctions[functionKey("example.invalid/provider", "read")]
			chains := (traceState{ctx: context.Background(), index: index, current: callback, bools: map[string]bool{}}).trace()
			if len(chains) != test.wantChains {
				t.Fatalf("trace() = %#v, want %d chains", chains, test.wantChains)
			}
			if test.wantChains == 1 && (chains[0].ReasonCode == nil || *chains[0].ReasonCode != contracts.ReasonCallChainUnresolved || chains[0].Steps[0].Kind != contracts.CallUnresolvedDispatch) {
				t.Errorf("trace() = %#v, want terminal unresolved dispatch", chains)
			}
		})
	}
}

func TestCrossFilePackageBuiltinShadowingIsUnresolved(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "values.go", Bytes: []byte("package provider\nvar len = func() {}")},
		{Path: "read.go", Bytes: []byte("package provider\nfunc read() { len() }")},
	}}}
	index, err := newIndex(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("newIndex() error = %v", err)
	}
	callback := index.providerFunctions[functionKey("example.invalid/provider", "read")]
	chains := (traceState{ctx: context.Background(), index: index, current: callback, bools: map[string]bool{}}).trace()
	if len(chains) != 1 || chains[0].ReasonCode == nil || *chains[0].ReasonCode != contracts.ReasonCallChainUnresolved || len(chains[0].Steps) != 1 || chains[0].Steps[0].Kind != contracts.CallUnresolvedDispatch || chains[0].Steps[0].Symbol != "len" {
		t.Errorf("trace(cross-file var len) = %#v, want terminal unresolved len dispatch", chains)
	}
}

func TestDepthExhaustionCannotProduceEndpoint(t *testing.T) {
	fn := parseFunction(t, "package provider\nfunc read(client *Client) { client.NewRequest(\"GET\", \"/ignored\", nil) }")
	state := traceState{ctx: context.Background(), current: fn, index: &analysisIndex{functions: map[string]*function{}}, bools: map[string]bool{}, depth: maxCallDepth}
	chains := state.trace()
	if len(chains) != 1 || chains[0].Endpoint != nil || chains[0].ReasonCode == nil || *chains[0].ReasonCode != contracts.ReasonCallChainUnresolved {
		t.Errorf("trace(depth exhausted) = %#v, want one unresolved non-endpoint chain", chains)
	}
}

func TestCandidateLimitNormalizesToSingleUnresolvedChain(t *testing.T) {
	read := parseFunction(t, "package provider\nfunc read(client *Client) { client.NewRequest(\"GET\", \"/one\", nil) }")
	request := certifiedRequest(t)
	call := firstCall(t, read.decl.Body)
	read.decl.Body.List = nil
	for range maxCandidates + 1 {
		read.decl.Body.List = append(read.decl.Body.List, &ast.ExprStmt{X: call})
	}
	state := traceState{ctx: context.Background(), current: read, index: &analysisIndex{functions: map[string]*function{functionKey("example.invalid/provider", "(*Client).NewRequest"): request}}, bools: map[string]bool{}}
	chains := state.trace()
	if len(chains) != 1 || chains[0].Endpoint != nil || chains[0].ReasonCode == nil || *chains[0].ReasonCode != contracts.ReasonCallChainUnresolved {
		t.Errorf("trace(candidate limit) = %#v, want one unresolved non-endpoint chain", chains)
	}
}

func TestNestedHelperFanoutNormalizesToSingleUnresolvedChain(t *testing.T) {
	read := parseFunction(t, "package provider\nfunc read(client *Client) { fanOut(client) }")
	helper := parseFunction(t, "package provider\nfunc fanOut(client *Client) { client.NewRequest(\"GET\", \"/one\", nil) }")
	request := certifiedRequest(t)
	call := firstCall(t, helper.decl.Body)
	helper.decl.Body.List = nil
	for range maxCandidates + 1 {
		helper.decl.Body.List = append(helper.decl.Body.List, &ast.ExprStmt{X: call})
	}
	index := &analysisIndex{
		functions:         map[string]*function{functionKey("example.invalid/provider", "(*Client).NewRequest"): request},
		providerFunctions: map[string]*function{functionKey("example.invalid/provider", "fanOut"): helper},
		constants:         map[string]map[string]string{},
	}
	chains := (traceState{ctx: context.Background(), current: read, index: index, bools: map[string]bool{}}).trace()
	if len(chains) != 1 || chains[0].Endpoint != nil || chains[0].ReasonCode == nil || *chains[0].ReasonCode != contracts.ReasonCallChainUnresolved {
		t.Errorf("trace(nested helper fanout) = %#v, want one unresolved non-endpoint chain", chains)
	}
}

func TestAnalysisCapsFailClosedBeforeUnboundedWork(t *testing.T) {
	snapshot := sourcebind.VerifiedSnapshot{Provider: sourcebind.CapturedTree{ModulePath: "example.invalid/provider", Files: []sourcebind.CapturedFile{
		{Path: "one.go", Bytes: []byte("package provider\nfunc one() { helper() }")},
		{Path: "two.go", Bytes: []byte("package provider\nfunc two() {}")},
	}}}
	for _, test := range []struct {
		name string
		caps analysisCaps
	}{
		{name: "parsed files", caps: analysisCaps{parsedFiles: 1, declarations: 10, functions: 10, callExpressions: 10}},
		{name: "declarations", caps: analysisCaps{parsedFiles: 10, declarations: 1, functions: 10, callExpressions: 10}},
		{name: "functions", caps: analysisCaps{parsedFiles: 10, declarations: 10, functions: 1, callExpressions: 10}},
		{name: "call expressions", caps: analysisCaps{parsedFiles: 10, declarations: 10, functions: 10, callExpressions: 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newIndexWithCaps(context.Background(), snapshot, test.caps); err == nil {
				t.Errorf("newIndexWithCaps(%s) error = nil, want deterministic cap rejection", test.name)
			}
		})
	}
}

func TestFixturePreservesAmbiguityCycleAndCreateExclusion(t *testing.T) {
	// The independent byte-equality test is the broad authority. These focused
	// assertions make its hostile properties explicit and diagnosable.
	checked := fixtureRoot(t)
	input := mustRead(t, checked+"/expected/source-evidence-report-v1.json")
	report, err := contracts.DecodeSourceEvidenceReport(input)
	if err != nil {
		t.Fatalf("contracts.DecodeSourceEvidenceReport(authority) error = %v, want nil", err)
	}
	ambiguous := report.Resources["sourcefirst_ambiguous"]
	if ambiguous.Classification != contracts.SourceAmbiguous || len(ambiguous.Chains) != 2 || ambiguous.Chains[0].Endpoint.PathTemplate != "/v1/alpha/{id}" || ambiguous.Chains[1].Endpoint.PathTemplate != "/v2/beta/{id}" {
		t.Errorf("reviewed ambiguous authority = %#v, want canonical alpha/beta candidates", ambiguous)
	}
	catalog := report.Resources["sourcefirst_sdk_http"]
	if len(catalog.Chains) != 1 || len(catalog.Chains[0].Steps) != 7 || catalog.Chains[0].Endpoint == nil {
		t.Errorf("reviewed catalog cycle authority = %#v, want bounded seven-step endpoint chain", catalog.Chains)
	}
	unresolved := report.Resources["sourcefirst_unresolved"]
	if unresolved.Classification != contracts.SourceUnresolved || len(unresolved.Chains) != 1 || unresolved.Chains[0].Endpoint != nil {
		t.Errorf("reviewed unresolved/Create-decoy authority = %#v, want no endpoint", unresolved)
	}
}

func parseFunction(t *testing.T, source string) *function {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "test.go", source, 0)
	if err != nil {
		t.Fatalf("parser.ParseFile(test source) error = %v, want nil", err)
	}
	for _, declaration := range parsed.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok {
			return &function{file: &sourceFile{origin: contracts.SourceLocationProvider, path: "test.go", packagePath: "example.invalid/provider", parsed: parsed, fset: fset, imports: map[string]string{}}, decl: fn, symbol: fn.Name.Name, packagePath: "example.invalid/provider"}
		}
	}
	t.Fatal("parseFunction() found no function")
	return nil
}

func certifiedRequest(t *testing.T) *function {
	t.Helper()
	request := parseFunction(t, "package provider\nimport \"net/http\"\nfunc (client *Client) NewRequest(method, path string, body any) { _, _ = http.NewRequest(method, path, nil) }")
	request.file.imports["http"] = "net/http"
	request.receiver = "Client"
	request.symbol = "(*Client).NewRequest"
	return request
}

func firstCall(t *testing.T, body *ast.BlockStmt) *ast.CallExpr {
	t.Helper()
	for _, call := range callsInBlock(body, map[string]bool{}) {
		return call
	}
	t.Fatal("firstCall() found no call")
	return nil
}

package provider

import "example.invalid/terraform-provider-sourcefirst/internal/fixture"

var resources = map[string]*fixture.Resource{
	"sourcefirst_direct_http": resourceDirectHTTP(),
	"sourcefirst_sdk_http":    resourceSDKHTTP(),
	"sourcefirst_sdk_symbol":  resourceSDKSymbol(),
	"sourcefirst_ambiguous":   resourceAmbiguous(),
	"sourcefirst_dynamic":     resourceDynamic(),
	"sourcefirst_unresolved":  resourceUnresolved(),
}

func Provider() *fixture.Provider {
	return &fixture.Provider{ResourcesMap: resources}
}

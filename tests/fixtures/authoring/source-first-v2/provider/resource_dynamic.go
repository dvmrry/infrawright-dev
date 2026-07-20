package provider

import (
	"context"
	"net/http"

	"example.invalid/terraform-provider-sourcefirst/internal/fixture"
)

var dynamicPath = func(id string) string { return "/runtime/" + id }

func resourceDynamic() *fixture.Resource {
	return &fixture.Resource{ReadContext: resourceDynamicRead}
}

func resourceDynamicRead(_ context.Context, client *fixture.Client, id string) error {
	_, err := client.NewRequest(http.MethodGet, dynamicPath(id), nil)
	return err
}

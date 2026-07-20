package provider

import (
	"context"

	"example.invalid/sourcefirst-sdk/catalog"
	"example.invalid/terraform-provider-sourcefirst/internal/fixture"
)

func resourceSDKHTTP() *fixture.Resource {
	return &fixture.Resource{ReadContext: resourceSDKHTTPRead}
}

func resourceSDKHTTPRead(ctx context.Context, _ *fixture.Client, id string) error {
	_, err := catalog.Get(ctx, catalog.NewClient(), id)
	return err
}

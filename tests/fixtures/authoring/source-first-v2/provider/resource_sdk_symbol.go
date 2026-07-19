package provider

import (
	"context"

	"example.invalid/sourcefirst-sdk/opaque"
	"example.invalid/terraform-provider-sourcefirst/internal/fixture"
)

type opaqueBackend struct{}

func (opaqueBackend) Resolve(context.Context, string) (string, error) {
	return "opaque-result", nil
}

func resourceSDKSymbol() *fixture.Resource {
	return &fixture.Resource{ReadContext: resourceSDKSymbolRead}
}

func resourceSDKSymbolRead(ctx context.Context, _ *fixture.Client, id string) error {
	_, err := opaque.Lookup(ctx, opaque.NewClient(opaqueBackend{}), id)
	return err
}

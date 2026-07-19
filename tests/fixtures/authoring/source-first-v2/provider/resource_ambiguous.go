package provider

import (
	"context"

	"example.invalid/sourcefirst-sdk/alpha"
	"example.invalid/sourcefirst-sdk/beta"
	"example.invalid/terraform-provider-sourcefirst/internal/fixture"
)

func resourceAmbiguous() *fixture.Resource {
	return &fixture.Resource{ReadContext: resourceAmbiguousRead}
}

func resourceAmbiguousRead(ctx context.Context, _ *fixture.Client, id string) error {
	if _, err := alpha.NewClient().Get(ctx, id); err != nil {
		return err
	}
	_, err := beta.NewClient().Get(ctx, id)
	return err
}

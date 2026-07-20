package provider

import (
	"context"
	"net/http"

	"example.invalid/terraform-provider-sourcefirst/internal/fixture"
)

type unresolvedReader interface {
	Read(context.Context, string) error
}

type unresolvedBackend struct{}

func (unresolvedBackend) Read(context.Context, string) error { return nil }

func resourceUnresolved() *fixture.Resource {
	return &fixture.Resource{
		ReadContext:   resourceUnresolvedRead,
		CreateContext: resourceUnresolvedCreate,
	}
}

func resourceUnresolvedRead(ctx context.Context, _ *fixture.Client, id string) error {
	var reader unresolvedReader = unresolvedBackend{}
	return reader.Read(ctx, id)
}

func resourceUnresolvedCreate(_ context.Context, client *fixture.Client, id string) error {
	_, err := client.NewRequest(http.MethodPost, "/v1/create-only/"+id, nil)
	return err
}

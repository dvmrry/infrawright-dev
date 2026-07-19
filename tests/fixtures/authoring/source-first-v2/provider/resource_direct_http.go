package provider

import (
	"context"
	"fmt"
	"net/http"

	"example.invalid/terraform-provider-sourcefirst/internal/fixture"
)

func resourceDirectHTTP() *fixture.Resource {
	return &fixture.Resource{ReadContext: resourceDirectHTTPRead}
}

func resourceDirectHTTPRead(ctx context.Context, client *fixture.Client, id string) error {
	return readDirectHTTP(ctx, client, id)
}

func readDirectHTTP(_ context.Context, client *fixture.Client, id string) error {
	_, err := client.NewRequest(http.MethodGet, fmt.Sprintf("/v1/direct/%s", id), nil)
	return err
}

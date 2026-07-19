package opaque

import "context"

type Backend interface {
	Resolve(context.Context, string) (string, error)
}

type Client struct {
	backend Backend
}

func NewClient(backend Backend) *Client { return &Client{backend: backend} }

func Lookup(ctx context.Context, client *Client, id string) (string, error) {
	return client.Lookup(ctx, id)
}

func (client *Client) Lookup(ctx context.Context, id string) (string, error) {
	return client.backend.Resolve(ctx, id)
}

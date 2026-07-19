package catalog

import (
	"context"
	"fmt"
	"net/http"
)

type Request struct {
	Method string
	Path   string
}

type Client struct{}

func NewClient() *Client { return &Client{} }

func Get(ctx context.Context, client *Client, id string) (*Request, error) {
	return client.Get(ctx, id)
}

func (client *Client) Get(ctx context.Context, id string) (*Request, error) {
	return client.lookupA(ctx, id, false)
}

func (client *Client) lookupA(ctx context.Context, id string, visited bool) (*Request, error) {
	if visited {
		return client.newGetRequest(id)
	}
	return client.lookupB(ctx, id, true)
}

func (client *Client) lookupB(ctx context.Context, id string, visited bool) (*Request, error) {
	return client.lookupA(ctx, id, visited)
}

func (client *Client) newGetRequest(id string) (*Request, error) {
	return client.NewRequest(http.MethodGet, fmt.Sprintf("/v1/catalog/%s", id), nil)
}

func (*Client) NewRequest(method, path string, _ any) (*Request, error) {
	return &Request{Method: method, Path: path}, nil
}

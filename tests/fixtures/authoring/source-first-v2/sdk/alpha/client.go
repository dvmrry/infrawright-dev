package alpha

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

func (client *Client) Get(_ context.Context, id string) (*Request, error) {
	return client.NewRequest(http.MethodGet, fmt.Sprintf("/v1/alpha/%s", id), nil)
}

func (*Client) NewRequest(method, path string, _ any) (*Request, error) {
	_, _ = http.NewRequest(method, path, nil)
	return &Request{Method: method, Path: path}, nil
}

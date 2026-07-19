package fixture

import (
	"context"
	"net/http"
)

type Request struct {
	Method string
	Path   string
}

type Client struct{}

func (*Client) NewRequest(method, path string, _ any) (*Request, error) {
	_, _ = http.NewRequest(method, path, nil)
	return &Request{Method: method, Path: path}, nil
}

type Resource struct {
	ReadContext   func(context.Context, *Client, string) error
	CreateContext func(context.Context, *Client, string) error
}

type Provider struct {
	ResourcesMap map[string]*Resource
}

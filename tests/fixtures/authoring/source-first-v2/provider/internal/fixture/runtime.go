package fixture

import "context"

type Request struct {
	Method string
	Path   string
}

type Client struct{}

func (*Client) NewRequest(method, path string, _ any) (*Request, error) {
	return &Request{Method: method, Path: path}, nil
}

type Resource struct {
	ReadContext   func(context.Context, *Client, string) error
	CreateContext func(context.Context, *Client, string) error
}

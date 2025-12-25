package p2c

import (
	"context"
	"fmt"
	"net/http"

	"github.com/valyala/fasthttp"
)

type Client struct {
	baseURL     string
	accessToken string
	httpClient  *fasthttp.Client
}

func NewClient(baseURL, accessToken string) *Client {
	return &Client{
		baseURL:     baseURL,
		accessToken: accessToken,
		httpClient:  &fasthttp.Client{},
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) newRequest(method, path string, body []byte) (*fasthttp.Request, *fasthttp.Response) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	req.SetRequestURI(c.baseURL + path)
	req.Header.SetMethod(method)
	req.Header.Set("Content-Type", "application/json")
	if c.accessToken != "" {
		req.Header.Set("Cookie", fmt.Sprintf("access_token=%s", c.accessToken))
	}
	if body != nil {
		req.SetBody(body)
	}
	return req, resp
}

func (c *Client) do(ctx context.Context, req *fasthttp.Request, resp *fasthttp.Response) error {
	return c.httpClient.DoRedirects(req, resp, 3)
}

func (c *Client) statusOK(resp *fasthttp.Response) bool {
	return resp.StatusCode() >= http.StatusOK && resp.StatusCode() < http.StatusMultipleChoices
}

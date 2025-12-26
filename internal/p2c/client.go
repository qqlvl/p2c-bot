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

// TakeLivePayment tries to accept a payment by its hex/id from websocket list:update.
// Endpoint: POST /p2c/payments/take/{id}
func (c *Client) TakeLivePayment(ctx context.Context, id string) error {
	req, resp := c.newRequest(http.MethodPost, fmt.Sprintf("/p2c/payments/take/%s", id), nil)
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err := c.do(ctx, req, resp); err != nil {
		return err
	}
	if !c.statusOK(resp) {
		return fmt.Errorf("take payment status %d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	return nil
}

// CompletePayment confirms payment.
func (c *Client) CompletePayment(ctx context.Context, id string, method string) error {
	body := []byte(fmt.Sprintf(`{"method":"%s"}`, method))
	req, resp := c.newRequest(http.MethodPost, fmt.Sprintf("/p2c/payments/%s/complete", id), body)
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err := c.do(ctx, req, resp); err != nil {
		return err
	}
	if !c.statusOK(resp) {
		return fmt.Errorf("complete payment status %d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	return nil
}

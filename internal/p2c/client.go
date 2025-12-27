package p2c

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/valyala/fasthttp"
)

type Client struct {
	baseURL     string
	accessToken string
	httpClient  *fasthttp.Client
	h2Client    *http.Client
}

// TraceTimings captures key timings for HTTP request.
type TraceTimings struct {
	DNSLookup     time.Duration
	TCPConnection time.Duration
	TLSHandshake  time.Duration
	ServerTime    time.Duration // from write to first byte
}

// TakeResult carries take response details.
type TakeResult struct {
	Body   []byte
	CFRay  string
	Timing TraceTimings
}

func NewClient(baseURL, accessToken string) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          512,
		MaxIdleConnsPerHost:   256,
		MaxConnsPerHost:       256,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   2 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
	}
	return &Client{
		baseURL:     baseURL,
		accessToken: accessToken,
		httpClient: &fasthttp.Client{
			NoDefaultUserAgentHeader: true,
			MaxConnsPerHost:          1024,
			ReadTimeout:              2 * time.Second,
			WriteTimeout:             2 * time.Second,
			MaxIdleConnDuration:      30 * time.Second,
		},
		h2Client: &http.Client{
			Transport: transport,
			Timeout:   3 * time.Second,
		},
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

// Warmup opens a cheap request to prime TLS/keepalive.
func (c *Client) Warmup(ctx context.Context) {
	req, resp := c.newRequest(http.MethodGet, "/health", nil)
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	_ = c.do(ctx, req, resp)
	// пробуем также HTTP/2 клиент
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if c.accessToken != "" {
		hreq.Header.Set("Cookie", fmt.Sprintf("access_token=%s", c.accessToken))
	}
	_, _ = c.h2Client.Do(hreq)
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
func (c *Client) TakeLivePayment(ctx context.Context, id string) (*TakeResult, error) {
	if id == "" {
		return nil, fmt.Errorf("empty id")
	}
	url := fmt.Sprintf("%s/p2c/payments/take/%s", c.baseURL, id)
	var t TraceTimings
	var dnsStart, connStart, tlsStart, writeDone time.Time
	trace := &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:  func(_ httptrace.DNSDoneInfo) { t.DNSLookup = time.Since(dnsStart) },
		ConnectStart: func(_, _ string) { connStart = time.Now() },
		ConnectDone: func(_, _ string, _ error) { t.TCPConnection = time.Since(connStart) },
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { t.TLSHandshake = time.Since(tlsStart) },
		WroteRequest:      func(_ httptrace.WroteRequestInfo) { writeDone = time.Now() },
		GotFirstResponseByte: func() {
			if !writeDone.IsZero() {
				t.ServerTime = time.Since(writeDone)
			}
		},
	}
	ctx = httptrace.WithClientTrace(ctx, trace)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if c.accessToken != "" {
		req.Header.Set("Cookie", fmt.Sprintf("access_token=%s", c.accessToken))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.h2Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("take payment status %d body=%s", resp.StatusCode, string(body))
	}
	return &TakeResult{
		Body:   body,
		CFRay:  resp.Header.Get("CF-RAY"),
		Timing: t,
	}, nil
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

// CancelPayment cancels a payment.
func (c *Client) CancelPayment(ctx context.Context, id string, reason string) error {
	body := []byte(fmt.Sprintf(`{"reason":"%s"}`, reason))
	req, resp := c.newRequest(http.MethodPost, fmt.Sprintf("/p2c/payments/%s/cancel", id), body)
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err := c.do(ctx, req, resp); err != nil {
		return err
	}
	if !c.statusOK(resp) {
		return fmt.Errorf("cancel payment status %d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	return nil
}

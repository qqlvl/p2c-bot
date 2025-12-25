package p2c

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/valyala/fasthttp"
)

type PaymentStatus string

const (
	StatusProcessing PaymentStatus = "processing"
	StatusCompleted  PaymentStatus = "completed"
	StatusDisputed   PaymentStatus = "disputed"
	StatusCanceled   PaymentStatus = "canceled"
	StatusRefunded   PaymentStatus = "refunded"
)

type Payment struct {
	ID         string        `json:"id"`
	Asset      string        `json:"asset"`
	Amount     string        `json:"amount"`
	AmountFiat string        `json:"amount_in_fiat"`
	Fiat       string        `json:"fiat"`
	Status     PaymentStatus `json:"status"`
	Processing string        `json:"processing_at"`
}

type ListPaymentsParams struct {
	Size   int
	Status PaymentStatus
	Cursor string
}

type ListPaymentsResponse struct {
	Data   []Payment `json:"data"`
	Cursor string    `json:"cursor"`
}

func (c *Client) ListPayments(ctx context.Context, params ListPaymentsParams) (*ListPaymentsResponse, error) {
	req, resp := c.newRequest("GET", "/p2c/payments", nil)
	query := req.URI().QueryArgs()
	if params.Size > 0 {
		query.SetUint("size", params.Size)
	}
	if params.Status != "" {
		query.Set("status", string(params.Status))
	}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}

	if err := c.do(ctx, req, resp); err != nil {
		return nil, err
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if !c.statusOK(resp) {
		return nil, fmt.Errorf("list payments status %d", resp.StatusCode())
	}

	var out ListPaymentsResponse
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) TakePayment(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("empty payment id")
	}
	path := fmt.Sprintf("/p2c/payments/take/%s", id)
	req, resp := c.newRequest("POST", path, nil)
	if err := c.do(ctx, req, resp); err != nil {
		return err
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if !c.statusOK(resp) {
		return fmt.Errorf("take payment status %d", resp.StatusCode())
	}
	return nil
}

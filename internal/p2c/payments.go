package p2c

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/valyala/fasthttp"
	"strconv"
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
	ID           json.Number   `json:"id"`
	Asset        string        `json:"out_asset"`
	Amount       string        `json:"out_amount"`
	AmountFiat   string        `json:"in_amount"`
	Fiat         string        `json:"in_asset"`
	ExchangeRate string        `json:"exchange_rate"`
	RewardAmount string        `json:"reward_amount"`
	RewardPercent float64      `json:"reward_percent,omitempty"`
	URL          string        `json:"url"`
	BrandName    string        `json:"brand_name"`
	Status       PaymentStatus `json:"status"`
	Processing   string        `json:"processing_at"`
	CompletedAt  string        `json:"completed_at,omitempty"`
	IsUnlocked   bool          `json:"is_unlocked,omitempty"`
}

func (p Payment) AmountFiatValue() float64 {
	val, err := strconv.ParseFloat(p.AmountFiat, 64)
	if err != nil {
		return 0
	}
	return val
}

func (p Payment) IDString() string {
	return p.ID.String()
}

func (p Payment) NumericID() int64 {
	v, _ := strconv.ParseInt(p.ID.String(), 10, 64)
	return v
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

// TakeResponse mirrors data from /take to extract numeric id.
type TakeResponse struct {
	Data *struct {
		ID json.Number `json:"id"`
	} `json:"data,omitempty"`
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

package p2c

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// LivePayment carries data from list:update op=add.
type LivePayment struct {
	ID          string  `json:"id"`
	Payload     string  `json:"payload"`
	URL         string  `json:"url"`
	BrandName   string  `json:"brand_name"`
	InAsset     string  `json:"in_asset"`
	OutAsset    string  `json:"out_asset"`
	Boost       float64 `json:"boost"`
	Provider    string  `json:"provider"`
	InAmount    string  `json:"in_amount"`
	OutAmount   string  `json:"out_amount"`
	ExchangeRate string `json:"exchange_rate"`
	FeeAmount   string  `json:"fee_amount"`
	ExpiresAt   string  `json:"expires_at"`
}

type listUpdate struct {
	Op   string       `json:"op"`
	Data *LivePayment `json:"data,omitempty"`
	Pos  *int         `json:"pos,omitempty"`
}

// SubscribeSocket connects to p2c-socket and feeds incoming "op=add" updates via handler.
func SubscribeSocket(ctx context.Context, baseURL, accessToken string, handler func(LivePayment)) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parse baseURL: %w", err)
	}
	u.Scheme = "wss"
	u.Path = "/internal/v1/p2c-socket/"
	q := u.Query()
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		EnableCompression: true,
	}

	header := http.Header{}
	header.Set("Origin", fmt.Sprintf("%s://%s", "https", u.Host))
	if accessToken != "" {
		header.Set("Cookie", fmt.Sprintf("access_token=%s", accessToken))
	}
	header.Set("Pragma", "no-cache")
	header.Set("Cache-Control", "no-cache")

	conn, _, err := dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Engine.IO requires sending "2" periodically to keep alive
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
			return nil
		case <-pingTicker.C:
			// send ping "2"
			_ = conn.WriteMessage(websocket.TextMessage, []byte("2"))
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			// Engine.IO messages start with a numeric prefix. We care about "42" -> socket.io event
			if len(msg) < 2 || string(msg[:2]) != "42" {
				continue
			}
			payload := msg[2:]
			var arr []json.RawMessage
			if err := json.Unmarshal(payload, &arr); err != nil || len(arr) < 2 {
				continue
			}
			var event string
			if err := json.Unmarshal(arr[0], &event); err != nil || event != "list:update" {
				continue
			}
			var updates []listUpdate
			if err := json.Unmarshal(arr[1], &updates); err != nil {
				continue
			}
			for _, u := range updates {
				if u.Op == "add" && u.Data != nil {
					handler(*u.Data)
				}
			}
		}
	}
}

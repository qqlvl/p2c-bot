package p2c

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	wsURL, sid, pingInterval, err := eioHandshake(baseURL, accessToken)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	conn, err := eioWebsocket(ctx, wsURL, accessToken, sid)
	if err != nil {
		return fmt.Errorf("dial ws: %w", err)
	}
	defer conn.Close()

	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
			return nil
		case <-pingTicker.C:
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

func eioHandshake(baseURL, accessToken string) (wsURL string, pingInterval time.Duration, err error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", 0, err
	}
	u.Scheme = "https"
	u.Path = "/internal/v1/p2c-socket/"
	q := u.Query()
	q.Set("EIO", "4")
	q.Set("transport", "polling")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	if accessToken != "" {
		req.Header.Set("Cookie", fmt.Sprintf("access_token=%s", accessToken))
	}
	req.Header.Set("Origin", fmt.Sprintf("%s://%s", "https", u.Host))
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Cache-Control", "no-cache")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 || body[0] != '0' {
		return "", 0, fmt.Errorf("unexpected handshake body: %s", string(body))
	}

	var open struct {
		SID          string `json:"sid"`
		PingInterval int64  `json:"pingInterval"`
		PingTimeout  int64  `json:"pingTimeout"`
	}
	if err := json.Unmarshal(body[1:], &open); err != nil {
		return "", 0, fmt.Errorf("parse open: %w", err)
	}
	if open.SID == "" {
		return "", 0, fmt.Errorf("empty sid")
	}

	// prepare websocket URL with sid
	u.Scheme = "wss"
	q.Set("transport", "websocket")
	q.Set("sid", open.SID)
	u.RawQuery = q.Encode()

	pi := time.Duration(open.PingInterval) * time.Millisecond
	if pi <= 0 {
		pi = 20 * time.Second
	}
	return u.String(), pi, nil
}

func eioWebsocket(ctx context.Context, wsURL, accessToken, sid string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		EnableCompression: true,
	}
	header := http.Header{}
	header.Set("Origin", fmt.Sprintf("%s://%s", "https", mustHost(wsURL)))
	if accessToken != "" {
		header.Set("Cookie", fmt.Sprintf("access_token=%s", accessToken))
	}
	header.Set("Pragma", "no-cache")
	header.Set("Cache-Control", "no-cache")

	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, err
	}

	// Engine.IO v4: send probe, expect "3probe", then upgrade "5"
	if err := conn.WriteMessage(websocket.TextMessage, []byte("2probe")); err != nil {
		conn.Close()
		return nil, err
	}
	_, resp, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if string(resp) != "3probe" {
		conn.Close()
		return nil, fmt.Errorf("probe failed: %s", string(resp))
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("5")); err != nil {
		conn.Close()
		return nil, err
	}
	// optional: read next message (may be "40" connect)
	_, _ = conn.ReadMessage()
	return conn, nil
}

func mustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

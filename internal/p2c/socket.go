package p2c

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
	wsURL, pingInterval, err := eioHandshake(baseURL, accessToken)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	conn, err := eioWebsocket(ctx, wsURL, accessToken)
	if err != nil {
		return fmt.Errorf("dial ws: %w", err)
	}
	defer conn.Close()
	log.Printf("ws connected: %s (pingInterval=%s)", wsURL, pingInterval)

	msgCount := 0
	addTimes := make(map[string]time.Time)
	listIDs := make([]string, 0, 32)

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
			return nil
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			s := string(msg)
			msgCount++
			if msgCount <= 20 {
				log.Printf("ws raw: %q", s)
			}
			// server ping -> answer pong
			if s == "2" {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("3"))
				continue
			}
			// connect ack from server -> отправляем list:initialize
			if strings.HasPrefix(s, "40") {
				// новый коннект — сбрасываем локальное состояние списка
				addTimes = make(map[string]time.Time)
				listIDs = listIDs[:0]
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`42["list:initialize"]`)); err != nil {
					return err
				}
				log.Printf("ws send init on 40")
				continue
			}
			// Engine.IO messages start with numeric prefix. We care about "42" -> socket.io event
			if len(s) < 2 || s[0:2] != "42" {
				log.Printf("ws ctrl: %s", s)
				continue
			}
			payload := []byte(s[2:])
			var arr []json.RawMessage
			if err := json.Unmarshal(payload, &arr); err != nil || len(arr) < 2 {
				continue
			}
			var event string
			if err := json.Unmarshal(arr[0], &event); err != nil {
				continue
			}
			if event == "list:snapshot" {
				var snapshot []LivePayment
				if err := json.Unmarshal(arr[1], &snapshot); err == nil {
					addTimes = make(map[string]time.Time)
					listIDs = listIDs[:0]
					now := time.Now()
					for _, p := range snapshot {
						listIDs = append(listIDs, p.ID)
						addTimes[p.ID] = now
					}
					log.Printf("ws snapshot loaded %d items", len(listIDs))
				}
				continue
			}
			if event != "list:update" {
				continue
			}
			var updates []listUpdate
			if err := json.Unmarshal(arr[1], &updates); err != nil {
				continue
			}
			for _, u := range updates {
				log.Printf("ws list:update op=%s id=%s", u.Op, idFrom(u.Data))
				if u.Op == "add" && u.Data != nil {
					// фиксируем время появления в стриме
					if _, ok := addTimes[u.Data.ID]; !ok {
						addTimes[u.Data.ID] = time.Now()
					}
					// убираем дубликат, если внезапно пришёл повтор
					for i, id := range listIDs {
						if id == u.Data.ID {
							listIDs = append(listIDs[:i], listIDs[i+1:]...)
							break
						}
					}
					pos := 0
					if u.Pos != nil && *u.Pos >= 0 && *u.Pos <= len(listIDs) {
						pos = *u.Pos
					}
					if pos < 0 {
						pos = 0
					}
					if pos > len(listIDs) {
						pos = len(listIDs)
					}
					listIDs = append(listIDs[:pos], append([]string{u.Data.ID}, listIDs[pos:]...)...)
					handler(*u.Data)
				}
				if u.Op == "remove" {
					// если пришел pos, пытаемся вытащить id и посчитать ttl
					if u.Pos == nil || *u.Pos < 0 || *u.Pos >= len(listIDs) {
						log.Printf("ws list:remove desync pos=%v len=%d", u.Pos, len(listIDs))
						continue
					}
					id := listIDs[*u.Pos]
					tAdd, ok := addTimes[id]
					ttl := int64(-1)
					if ok {
						ttl = time.Since(tAdd).Milliseconds()
					}
					log.Printf("ws list:remove id=%s pos=%d ttl=%dms hasAdd=%v", id, *u.Pos, ttl, ok)
					// убираем из списка
					listIDs = append(listIDs[:*u.Pos], listIDs[*u.Pos+1:]...)
					delete(addTimes, id)
				}
			}
		}
	}
}

func idFrom(p *LivePayment) string {
	if p == nil {
		return ""
	}
	return p.ID
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

func eioWebsocket(ctx context.Context, wsURL, accessToken string) (*websocket.Conn, error) {
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

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("bad handshake: %v body=%s", err, string(b))
		}
		return nil, err
	}

	// Engine.IO v4: send probe, expect "3probe", then upgrade "5"
	if err := conn.WriteMessage(websocket.TextMessage, []byte("2probe")); err != nil {
		conn.Close()
		return nil, err
	}
	_, respMsg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if string(respMsg) != "3probe" {
		conn.Close()
		return nil, fmt.Errorf("probe failed: %s", string(respMsg))
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("5")); err != nil {
		conn.Close()
		return nil, err
	}
	// Send connect to default namespace
	if err := conn.WriteMessage(websocket.TextMessage, []byte("40")); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func mustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

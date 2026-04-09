package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// TunnelMsg mirrors the relay's message format exactly.
type TunnelMsg struct {
	Type    string              `json:"type"`
	ID      string              `json:"id"`
	Method  string              `json:"method,omitempty"`
	Path    string              `json:"path,omitempty"`
	Query   string              `json:"query,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
	Status  int                 `json:"status,omitempty"`
	Data    string              `json:"data,omitempty"`
	Binary  bool                `json:"binary,omitempty"`
	Code    int                 `json:"code,omitempty"`
	Reason  string              `json:"reason,omitempty"`
}

// Connector maintains a persistent tunnel WebSocket to the relay and proxies
// requests to the local mclaude-server.
type Connector struct {
	relayURL      string
	tunnelToken   string
	mclaudeURL    string
	serviceToken  string
	tlsSkipVerify bool

	sendMu  sync.Mutex
	conn    *websocket.Conn
	connMu  sync.RWMutex

	wsMu    sync.Mutex
	wsConns map[string]*websocket.Conn

	httpClient *http.Client
	wsDialer   *websocket.Dialer

	sendSeq atomic.Uint64
}

func NewConnector(relayURL, tunnelToken, mclaudeURL, serviceToken string, tlsSkipVerify bool) *Connector {
	tlsCfg := &tls.Config{InsecureSkipVerify: tlsSkipVerify} //nolint:gosec
	return &Connector{
		relayURL:      relayURL,
		tunnelToken:   tunnelToken,
		mclaudeURL:    mclaudeURL,
		serviceToken:  serviceToken,
		tlsSkipVerify: tlsSkipVerify,
		wsConns:       make(map[string]*websocket.Conn),
		httpClient: &http.Client{
			Timeout:   25 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
		wsDialer: &websocket.Dialer{
			TLSClientConfig:  tlsCfg,
			HandshakeTimeout: 10 * time.Second,
		},
	}
}

// Run connects to the relay with automatic reconnect.
// Uses a short fixed delay — the relay is expected to be always available.
func (c *Connector) Run() {
	const retryDelay = 2 * time.Second
	for {
		log.Printf("connecting to relay %s ...", c.relayURL)
		if err := c.connect(); err != nil {
			log.Printf("tunnel error: %v — retrying in %v", err, retryDelay)
		}
		time.Sleep(retryDelay)
	}
}

func (c *Connector) connect() error {
	u, err := url.Parse(c.relayURL)
	if err != nil {
		return fmt.Errorf("parse relay url: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/tunnel"

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+c.tunnelToken)

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	defer func() {
		c.connMu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.connMu.Unlock()

		// close all upstream WS connections
		c.wsMu.Lock()
		for id, ws := range c.wsConns {
			ws.Close()
			delete(c.wsConns, id)
		}
		c.wsMu.Unlock()
	}()

	log.Println("tunnel connected")

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var msg TunnelMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("unmarshal: %v", err)
			continue
		}
		go c.handle(&msg)
	}
}

func (c *Connector) send(msg *TunnelMsg) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	return conn.WriteJSON(msg)
}

func (c *Connector) handle(msg *TunnelMsg) {
	switch msg.Type {
	case "http_request":
		c.handleHTTP(msg)
	case "ws_connect":
		c.handleWSConnect(msg)
	case "ws_message":
		c.handleWSMessage(msg)
	case "ws_close":
		c.handleWSClose(msg)
	}
}

// ── HTTP proxy ─────────────────────────────────────────────────────────────

func (c *Connector) handleHTTP(msg *TunnelMsg) {
	rawURL := strings.TrimSuffix(c.mclaudeURL, "/") + msg.Path
	if msg.Query != "" {
		rawURL += "?" + msg.Query
	}

	var bodyReader io.Reader = bytes.NewReader(nil)
	if msg.Body != "" {
		if b, err := base64.StdEncoding.DecodeString(msg.Body); err == nil && len(b) > 0 {
			bodyReader = bytes.NewReader(b)
		}
	}

	req, err := http.NewRequest(msg.Method, rawURL, bodyReader)
	if err != nil {
		c.sendErrorResp(msg.ID, http.StatusInternalServerError, err.Error())
		return
	}

	for k, vs := range msg.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	// Inject service auth for mclaude-server
	if c.serviceToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.serviceToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.sendErrorResp(msg.ID, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	respHeaders := make(map[string][]string)
	for k, vs := range resp.Header {
		lk := strings.ToLower(k)
		switch lk {
		case "connection", "transfer-encoding":
			continue
		}
		respHeaders[lk] = vs
	}

	c.send(&TunnelMsg{ //nolint:errcheck
		Type:    "http_response",
		ID:      msg.ID,
		Status:  resp.StatusCode,
		Headers: respHeaders,
		Body:    base64.StdEncoding.EncodeToString(body),
	})
}

func (c *Connector) sendErrorResp(id string, status int, errMsg string) {
	body := fmt.Sprintf(`{"error":%q}`, errMsg)
	c.send(&TunnelMsg{ //nolint:errcheck
		Type:    "http_response",
		ID:      id,
		Status:  status,
		Headers: map[string][]string{"content-type": {"application/json"}},
		Body:    base64.StdEncoding.EncodeToString([]byte(body)),
	})
}

// ── WebSocket bridge ───────────────────────────────────────────────────────

func (c *Connector) handleWSConnect(msg *TunnelMsg) {
	u, err := url.Parse(c.mclaudeURL)
	if err != nil {
		c.notifyWSClose(msg.ID, 1011, "bad mclaude url")
		return
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/ws"

	// Always auth upstream with our service token (replace phone's web token)
	q := url.Values{}
	if c.serviceToken != "" {
		q.Set("token", c.serviceToken)
	}
	u.RawQuery = q.Encode()

	wsConn, _, err := c.wsDialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("ws connect to mclaude: %v", err)
		c.notifyWSClose(msg.ID, 1011, "upstream connect failed")
		return
	}

	wsID := msg.ID
	c.wsMu.Lock()
	c.wsConns[wsID] = wsConn
	c.wsMu.Unlock()

	// upstream → tunnel → relay → phone
	go func() {
		defer func() {
			wsConn.Close()
			c.wsMu.Lock()
			delete(c.wsConns, wsID)
			c.wsMu.Unlock()
			c.notifyWSClose(wsID, 1000, "upstream closed")
		}()
		for {
			mt, data, err := wsConn.ReadMessage()
			if err != nil {
				break
			}
			c.send(&TunnelMsg{ //nolint:errcheck
				Type:   "ws_message",
				ID:     wsID,
				Data:   base64.StdEncoding.EncodeToString(data),
				Binary: mt == websocket.BinaryMessage,
			})
		}
	}()
}

func (c *Connector) handleWSMessage(msg *TunnelMsg) {
	c.wsMu.Lock()
	wsConn, ok := c.wsConns[msg.ID]
	c.wsMu.Unlock()
	if !ok {
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return
	}
	mt := websocket.TextMessage
	if msg.Binary {
		mt = websocket.BinaryMessage
	}
	wsConn.WriteMessage(mt, data) //nolint:errcheck
}

func (c *Connector) handleWSClose(msg *TunnelMsg) {
	c.wsMu.Lock()
	wsConn, ok := c.wsConns[msg.ID]
	if ok {
		delete(c.wsConns, msg.ID)
	}
	c.wsMu.Unlock()
	if ok {
		wsConn.Close()
	}
}

func (c *Connector) notifyWSClose(id string, code int, reason string) {
	c.send(&TunnelMsg{Type: "ws_close", ID: id, Code: code, Reason: reason}) //nolint:errcheck
}

// ── Entry point ────────────────────────────────────────────────────────────

func main() {
	relayURL := os.Getenv("RELAY_URL")
	tunnelToken := os.Getenv("TUNNEL_TOKEN")
	mclaudeURL := os.Getenv("MCLAUDE_URL")
	serviceToken := os.Getenv("SERVICE_TOKEN")
	tlsSkipVerify := os.Getenv("TLS_SKIP_VERIFY") == "1" || os.Getenv("TLS_SKIP_VERIFY") == "true"

	if relayURL == "" {
		log.Fatal("RELAY_URL is required (e.g. https://relay.example.com)")
	}
	if tunnelToken == "" {
		log.Fatal("TUNNEL_TOKEN is required")
	}
	if mclaudeURL == "" {
		mclaudeURL = "http://localhost:8377"
	}
	if tlsSkipVerify {
		log.Println("TLS verification disabled for mclaude-server (TLS_SKIP_VERIFY=true)")
	}

	log.Printf("mclaude-connector starting  relay=%s  mclaude=%s", relayURL, mclaudeURL)
	conn := NewConnector(relayURL, tunnelToken, mclaudeURL, serviceToken, tlsSkipVerify)
	conn.Run()
}

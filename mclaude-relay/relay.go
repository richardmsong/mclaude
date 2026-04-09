package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// TunnelMsg is the multiplexed message format on the tunnel WebSocket.
// It carries both HTTP request/response pairs and WebSocket frames.
type TunnelMsg struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// http_request fields
	Method  string              `json:"method,omitempty"`
	Path    string              `json:"path,omitempty"`
	Query   string              `json:"query,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"` // base64-encoded

	// http_response fields
	Status int `json:"status,omitempty"`

	// ws_message fields
	Data   string `json:"data,omitempty"` // base64-encoded frame payload
	Binary bool   `json:"binary,omitempty"`

	// ws_close fields
	Code   int    `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type pendingHTTP struct {
	ch chan *TunnelMsg
}

type wsClient struct {
	ch chan *TunnelMsg // buffered; relay writes, phone read-loop drains
}

// Relay manages the connector tunnel and bridges phone HTTP/WS to the laptop.
type Relay struct {
	tunnelToken string
	webToken    string

	// tunnel connection (one at a time; connector reconnects on restart)
	mu     sync.RWMutex
	tunnel *websocket.Conn

	// serialise writes to the tunnel WS (gorilla is not concurrent-write-safe)
	sendMu sync.Mutex

	// in-flight HTTP requests waiting for a response from the connector
	pendingMu sync.Mutex
	pending   map[string]*pendingHTTP

	// active phone WebSocket clients
	wsMu      sync.Mutex
	wsClients map[string]*wsClient

	reqSeq atomic.Uint64
	wsSeq  atomic.Uint64

	upgrader websocket.Upgrader
}

func NewRelay(tunnelToken, webToken string) *Relay {
	return &Relay{
		tunnelToken: tunnelToken,
		webToken:    webToken,
		pending:     make(map[string]*pendingHTTP),
		wsClients:   make(map[string]*wsClient),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

// ── Tunnel (connector side) ────────────────────────────────────────────────

// HandleTunnel accepts the laptop connector's persistent WebSocket.
func (r *Relay) HandleTunnel(w http.ResponseWriter, req *http.Request) {
	if extractToken(req) != r.tunnelToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("tunnel upgrade: %v", err)
		return
	}
	defer conn.Close()

	log.Println("connector tunnel connected")

	r.mu.Lock()
	if r.tunnel != nil {
		r.tunnel.Close() // evict old connector
	}
	r.tunnel = conn
	r.mu.Unlock()

	// Keepalive: ping every 30s so Traefik/intermediaries don't kill idle tunnel.
	// Reset read deadline on each pong; if no pong in 60s the read below errors.
	const pingInterval = 30 * time.Second
	const readDeadline = 60 * time.Second

	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readDeadline))
	})
	conn.SetReadDeadline(time.Now().Add(readDeadline)) //nolint:errcheck

	pingStop := make(chan struct{})
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.sendMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				r.sendMu.Unlock()
				if err != nil {
					return
				}
			case <-pingStop:
				return
			}
		}
	}()

	defer func() {
		close(pingStop)

		r.mu.Lock()
		if r.tunnel == conn {
			r.tunnel = nil
		}
		r.mu.Unlock()
		log.Println("connector tunnel disconnected")

		// Fail all pending HTTP requests.
		r.pendingMu.Lock()
		for id, p := range r.pending {
			select {
			case p.ch <- nil:
			default:
			}
			delete(r.pending, id)
		}
		r.pendingMu.Unlock()

		// Close all active phone WS connections so they reconnect immediately
		// instead of hanging indefinitely waiting for tunnel messages.
		r.wsMu.Lock()
		for _, client := range r.wsClients {
			select {
			case client.ch <- &TunnelMsg{Type: "ws_close"}:
			default:
			}
		}
		r.wsMu.Unlock()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("tunnel read: %v", err)
			}
			break
		}
		var msg TunnelMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("tunnel unmarshal: %v", err)
			continue
		}
		r.dispatchTunnelMsg(&msg)
	}
}

func (r *Relay) dispatchTunnelMsg(msg *TunnelMsg) {
	switch msg.Type {
	case "http_response":
		r.pendingMu.Lock()
		p, ok := r.pending[msg.ID]
		if ok {
			delete(r.pending, msg.ID)
		}
		r.pendingMu.Unlock()
		if ok {
			select {
			case p.ch <- msg:
			default:
			}
		}

	case "ws_message", "ws_close":
		r.wsMu.Lock()
		client, ok := r.wsClients[msg.ID]
		r.wsMu.Unlock()
		if ok {
			select {
			case client.ch <- msg:
			default:
				log.Printf("ws client %s channel full", msg.ID)
			}
		}
	}
}

// tunnelSend serialises a write to the tunnel WebSocket.
func (r *Relay) tunnelSend(msg *TunnelMsg) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	r.mu.RLock()
	conn := r.tunnel
	r.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("no connector tunnel")
	}
	return conn.WriteJSON(msg)
}

// ── HTTP API proxy ─────────────────────────────────────────────────────────

func (r *Relay) HandleAPI(w http.ResponseWriter, req *http.Request) {
	// Auth
	if extractToken(req) != r.webToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Read body
	body, err := io.ReadAll(io.LimitReader(req.Body, 32*1024*1024))
	if err != nil {
		http.Error(w, "body read error", http.StatusInternalServerError)
		return
	}

	// Build forwarded headers (drop auth + hop-by-hop)
	fwdHeaders := make(map[string][]string)
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		switch lk {
		case "authorization", "host", "connection", "content-length",
			"transfer-encoding", "upgrade", "te", "trailers":
			continue
		}
		fwdHeaders[lk] = vs
	}

	id := fmt.Sprintf("req-%d", r.reqSeq.Add(1))

	tunnelReq := &TunnelMsg{
		Type:    "http_request",
		ID:      id,
		Method:  req.Method,
		Path:    req.URL.Path,
		Query:   req.URL.RawQuery,
		Headers: fwdHeaders,
		Body:    base64.StdEncoding.EncodeToString(body),
	}

	// Register pending slot
	ch := make(chan *TunnelMsg, 1)
	r.pendingMu.Lock()
	r.pending[id] = &pendingHTTP{ch: ch}
	r.pendingMu.Unlock()

	if err := r.tunnelSend(tunnelReq); err != nil {
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
		http.Error(w, "tunnel unavailable", http.StatusServiceUnavailable)
		return
	}

	select {
	case resp := <-ch:
		if resp == nil {
			http.Error(w, "tunnel disconnected", http.StatusServiceUnavailable)
			return
		}
		for k, vs := range resp.Headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.Status)
		if resp.Body != "" {
			if decoded, err := base64.StdEncoding.DecodeString(resp.Body); err == nil {
				w.Write(decoded)
			}
		}

	case <-time.After(30 * time.Second):
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
		http.Error(w, "request timeout", http.StatusGatewayTimeout)

	case <-req.Context().Done():
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
	}
}

// ── Phone WebSocket ────────────────────────────────────────────────────────

// HandleClientWS handles a browser's /ws connection and bridges it through
// the tunnel to mclaude-server running on the laptop.
func (r *Relay) HandleClientWS(w http.ResponseWriter, req *http.Request) {
	if extractToken(req) != r.webToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("client ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	id := fmt.Sprintf("ws-%d", r.wsSeq.Add(1))

	ch := make(chan *TunnelMsg, 128)
	r.wsMu.Lock()
	r.wsClients[id] = &wsClient{ch: ch}
	r.wsMu.Unlock()

	defer func() {
		r.wsMu.Lock()
		delete(r.wsClients, id)
		r.wsMu.Unlock()
		// tell connector the phone disconnected
		r.tunnelSend(&TunnelMsg{ //nolint:errcheck
			Type:   "ws_close",
			ID:     id,
			Code:   1000,
			Reason: "client disconnected",
		})
	}()

	// Notify connector of new WS client (pass raw query so connector can
	// substitute its own service token when connecting upstream)
	if err := r.tunnelSend(&TunnelMsg{
		Type:  "ws_connect",
		ID:    id,
		Query: req.URL.RawQuery,
	}); err != nil {
		return
	}

	// phone → relay → tunnel (connector → mclaude-server)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			r.tunnelSend(&TunnelMsg{ //nolint:errcheck
				Type:   "ws_message",
				ID:     id,
				Data:   base64.StdEncoding.EncodeToString(data),
				Binary: msgType == websocket.BinaryMessage,
			})
		}
	}()

	// tunnel → relay → phone
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg.Type == "ws_close" {
				return
			}
			if msg.Type == "ws_message" {
				payload, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					continue
				}
				msgType := websocket.TextMessage
				if msg.Binary {
					msgType = websocket.BinaryMessage
				}
				if err := conn.WriteMessage(msgType, payload); err != nil {
					return
				}
			}
		case <-readDone:
			return
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────

func extractToken(req *http.Request) string {
	if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return req.URL.Query().Get("token")
}

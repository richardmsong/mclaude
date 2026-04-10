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

	// pty fields
	Rows int `json:"rows,omitempty"`
	Cols int `json:"cols,omitempty"`
}

type pendingHTTP struct {
	ch chan *TunnelMsg
}

// tunnelEntry represents a single connected laptop's tunnel.
type tunnelEntry struct {
	conn     *websocket.Conn
	sendMu   sync.Mutex
	hostname string
	pending  map[string]*pendingHTTP
	pendMu   sync.Mutex
}

func (t *tunnelEntry) send(msg *TunnelMsg) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	return t.conn.WriteJSON(msg)
}

// wsClient represents a connected phone/browser WebSocket.
type wsClient struct {
	ch chan *TunnelMsg // buffered; relay writes, phone read-loop drains
	// Maps tunnel hostname → tunnel-side WS ID for this client.
	// When phone sends a WS frame, relay forwards to all tunnels using these IDs.
	tunnelIDs   map[string]string // hostname -> tunnelWsID
	tunnelIDsMu sync.Mutex
}

// Relay manages connector tunnels and bridges phone HTTP/WS to laptops.
type Relay struct {
	tunnelToken string
	webToken    string

	// Multi-tunnel: one entry per connected laptop
	tunnelsMu sync.RWMutex
	tunnels   map[string]*tunnelEntry // hostname -> entry

	// active phone WebSocket clients
	wsMu      sync.Mutex
	wsClients map[string]*wsClient

	reqSeq atomic.Uint64
	wsSeq  atomic.Uint64
	ptySeq atomic.Uint64

	// active PTY sessions (browser → tunnel)
	ptyMu       sync.Mutex
	ptySessions map[string]*ptySession

	upgrader websocket.Upgrader
}

type ptySession struct {
	clientConn *websocket.Conn
	clientMu   sync.Mutex
	hostname   string
}

func NewRelay(tunnelToken, webToken string) *Relay {
	return &Relay{
		tunnelToken: tunnelToken,
		webToken:    webToken,
		tunnels:     make(map[string]*tunnelEntry),
		wsClients:   make(map[string]*wsClient),
		ptySessions: make(map[string]*ptySession),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

// ── Tunnel (connector side) ────────────────────────────────────────────────

// HandleTunnel accepts a laptop connector's persistent WebSocket.
func (r *Relay) HandleTunnel(w http.ResponseWriter, req *http.Request) {
	if extractToken(req) != r.tunnelToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	hostname := req.Header.Get("X-Hostname")
	if hostname == "" {
		hostname = "default"
	}

	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("tunnel upgrade: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("connector tunnel connected: %s", hostname)

	entry := &tunnelEntry{
		conn:     conn,
		hostname: hostname,
		pending:  make(map[string]*pendingHTTP),
	}

	// Evict old tunnel for same hostname only
	r.tunnelsMu.Lock()
	if old, ok := r.tunnels[hostname]; ok {
		old.conn.Close()
	}
	r.tunnels[hostname] = entry
	r.tunnelsMu.Unlock()

	// Keepalive pings
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
				entry.sendMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				entry.sendMu.Unlock()
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

		r.tunnelsMu.Lock()
		if r.tunnels[hostname] == entry {
			delete(r.tunnels, hostname)
		}
		r.tunnelsMu.Unlock()
		log.Printf("connector tunnel disconnected: %s", hostname)

		// Fail pending HTTP requests for this tunnel
		entry.pendMu.Lock()
		for id, p := range entry.pending {
			select {
			case p.ch <- nil:
			default:
			}
			delete(entry.pending, id)
		}
		entry.pendMu.Unlock()

		// Notify all phone WS clients about tunnel disconnect.
		// Send ws_close for any WS IDs associated with this tunnel,
		// then broadcast updated (empty) sessions for this laptop.
		r.wsMu.Lock()
		for _, client := range r.wsClients {
			client.tunnelIDsMu.Lock()
			if tunnelWsID, ok := client.tunnelIDs[hostname]; ok {
				delete(client.tunnelIDs, hostname)
				select {
				case client.ch <- &TunnelMsg{Type: "ws_close", ID: tunnelWsID}:
				default:
				}
			}
			client.tunnelIDsMu.Unlock()
		}
		r.wsMu.Unlock()

		// Reconnect all WS clients to remaining tunnels by sending
		// a sessions refresh (the phone will re-fetch via WS).
		r.broadcastSessionsRefresh()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("tunnel read [%s]: %v", hostname, err)
			}
			break
		}
		var msg TunnelMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("tunnel unmarshal [%s]: %v", hostname, err)
			continue
		}
		r.dispatchTunnelMsg(hostname, entry, &msg)
	}
}

func (r *Relay) dispatchTunnelMsg(hostname string, entry *tunnelEntry, msg *TunnelMsg) {
	switch msg.Type {
	case "http_response":
		entry.pendMu.Lock()
		p, ok := entry.pending[msg.ID]
		if ok {
			delete(entry.pending, msg.ID)
		}
		entry.pendMu.Unlock()
		if ok {
			select {
			case p.ch <- msg:
			default:
			}
		}

	case "pty_data":
		r.ptyMu.Lock()
		ps, ok := r.ptySessions[msg.ID]
		r.ptyMu.Unlock()
		if ok {
			data, err := base64.StdEncoding.DecodeString(msg.Data)
			if err == nil {
				ps.clientMu.Lock()
				ps.clientConn.WriteMessage(websocket.BinaryMessage, data) //nolint:errcheck
				ps.clientMu.Unlock()
			}
		}

	case "pty_close":
		r.ptyMu.Lock()
		ps, ok := r.ptySessions[msg.ID]
		if ok {
			delete(r.ptySessions, msg.ID)
		}
		r.ptyMu.Unlock()
		if ok {
			ps.clientMu.Lock()
			ps.clientConn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg.Reason)) //nolint:errcheck
			ps.clientConn.Close()
			ps.clientMu.Unlock()
		}

	case "ws_message", "ws_close":
		// Find the phone WS client that owns this tunnel-side WS ID.
		r.wsMu.Lock()
		for _, client := range r.wsClients {
			client.tunnelIDsMu.Lock()
			matched := false
			for h, tid := range client.tunnelIDs {
				if tid == msg.ID && h == hostname {
					matched = true
					break
				}
			}
			client.tunnelIDsMu.Unlock()
			if matched {
				// Prefix session IDs in the message data before forwarding
				prefixed := prefixWSMessage(msg, hostname)
				select {
				case client.ch <- prefixed:
				default:
					log.Printf("ws client channel full for tunnel %s", hostname)
				}
			}
		}
		r.wsMu.Unlock()
	}
}

// broadcastSessionsRefresh triggers all phone WS clients to get fresh session data.
// We do this by re-establishing WS connections to all tunnels for each client.
// Simpler approach: just let the phone's WS reconnect logic handle it.
// For now, we signal by sending a synthetic "sessions" message with empty data,
// which the phone interprets as "refresh needed."
func (r *Relay) broadcastSessionsRefresh() {
	// The phone will get updated sessions on its next WS reconnect or poll.
	// For immediate update, we could fan-out GET /sessions and push via WS,
	// but that's complex. The phone already handles tunnel disconnect gracefully
	// by attempting WS reconnect.
}

// ── Tunnel helpers ────────────────────────────────────────────────────────

func (r *Relay) getTunnel(hostname string) *tunnelEntry {
	r.tunnelsMu.RLock()
	defer r.tunnelsMu.RUnlock()
	return r.tunnels[hostname]
}

func (r *Relay) getAllTunnels() map[string]*tunnelEntry {
	r.tunnelsMu.RLock()
	defer r.tunnelsMu.RUnlock()
	cp := make(map[string]*tunnelEntry, len(r.tunnels))
	for k, v := range r.tunnels {
		cp[k] = v
	}
	return cp
}

// getTunnelsForFilter returns tunnels matching the filter, or all tunnels if filter is empty.
func (r *Relay) getTunnelsForFilter(hostname string) map[string]*tunnelEntry {
	if hostname == "" {
		return r.getAllTunnels()
	}
	t := r.getTunnel(hostname)
	if t == nil {
		return nil
	}
	return map[string]*tunnelEntry{hostname: t}
}

// ConnectedLaptops returns the list of connected laptop hostnames.
func (r *Relay) ConnectedLaptops() []string {
	r.tunnelsMu.RLock()
	defer r.tunnelsMu.RUnlock()
	names := make([]string, 0, len(r.tunnels))
	for k := range r.tunnels {
		names = append(names, k)
	}
	return names
}

// ── Session ID namespacing ────────────────────────────────────────────────

const laptopSep = "~"

// prefixSessionID adds the laptop hostname prefix to a session ID.
func prefixSessionID(hostname, sessionID string) string {
	return hostname + laptopSep + sessionID
}

// parsePrefixedID splits a prefixed session ID into hostname and original ID.
// Returns ("default", id) if no prefix found (backward compat).
func parsePrefixedID(prefixedID string) (hostname, sessionID string) {
	if idx := strings.Index(prefixedID, laptopSep); idx >= 0 {
		return prefixedID[:idx], prefixedID[idx+1:]
	}
	return "default", prefixedID
}

// prefixWSMessage rewrites session IDs in WebSocket messages from a tunnel.
// It decodes the base64 WS payload, parses the JSON, rewrites IDs, re-encodes.
func prefixWSMessage(msg *TunnelMsg, hostname string) *TunnelMsg {
	if msg.Data == "" {
		return msg
	}

	decoded, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return msg
	}

	// The WS frame is a JSON object like {"type":"sessions","data":"[...]"}
	// where "data" is a JSON-encoded string (double-encoded).
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		return msg
	}

	var msgType string
	if raw, ok := envelope["type"]; ok {
		json.Unmarshal(raw, &msgType) //nolint:errcheck
	}

	var dataStr string
	if raw, ok := envelope["data"]; ok {
		// data is a JSON string containing stringified JSON
		if err := json.Unmarshal(raw, &dataStr); err != nil {
			// data might not be a string — pass through unchanged
			return msg
		}
	}

	switch msgType {
	case "sessions":
		var sessions []map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &sessions); err == nil {
			for i := range sessions {
				if id, ok := sessions[i]["id"].(string); ok {
					sessions[i]["id"] = prefixSessionID(hostname, id)
				}
				sessions[i]["laptop"] = hostname
			}
			rewritten, _ := json.Marshal(sessions)
			rewrittenStr, _ := json.Marshal(string(rewritten))
			envelope["data"] = rewrittenStr
		}

	case "output", "event", "more_output":
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &data); err == nil {
			if id, ok := data["id"].(string); ok {
				data["id"] = prefixSessionID(hostname, id)
			}
			rewritten, _ := json.Marshal(data)
			rewrittenStr, _ := json.Marshal(string(rewritten))
			envelope["data"] = rewrittenStr
		}
	default:
		// Unknown message type — pass through unchanged
		return msg
	}

	result, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("prefixWSMessage: marshal error: %v", err)
		return msg
	}

	return &TunnelMsg{
		Type:   msg.Type,
		ID:     msg.ID,
		Data:   base64.StdEncoding.EncodeToString(result),
		Binary: msg.Binary,
	}
}

// ── Tunneled static files (no auth required for page load) ────────────────

func (r *Relay) HandleTunnelStatic(w http.ResponseWriter, req *http.Request, staticHost string) {
	tunnel := r.getTunnel(staticHost)
	if tunnel == nil {
		// Try any available tunnel as fallback
		tunnels := r.getAllTunnels()
		for _, t := range tunnels {
			tunnel = t
			break
		}
	}
	if tunnel == nil {
		http.Error(w, "no tunnel available for static files", http.StatusServiceUnavailable)
		return
	}
	r.proxyToTunnel(tunnel, w, req)
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

	path := req.URL.Path

	// Fan-out endpoints: merge responses from all tunnels (or single tunnel if X-Laptop-ID set)
	laptopFilter := req.Header.Get("X-Laptop-ID")

	if path == "/sessions" && req.Method == "GET" {
		r.fanOutSessions(w, req, laptopFilter)
		return
	}
	if path == "/projects" && req.Method == "GET" {
		r.fanOutJSON(w, req, "/projects", laptopFilter)
		return
	}
	if path == "/skills" && req.Method == "GET" {
		r.fanOutJSON(w, req, "/skills", laptopFilter)
		return
	}

	// Session-specific routes: extract laptop from session ID prefix
	if strings.HasPrefix(path, "/sessions/") {
		parts := strings.SplitN(strings.TrimPrefix(path, "/sessions/"), "/", 2)
		if len(parts) >= 1 {
			hostname, realID := parsePrefixedID(parts[0])
			tunnel := r.getTunnel(hostname)
			if tunnel == nil {
				http.Error(w, "laptop not connected: "+hostname, http.StatusServiceUnavailable)
				return
			}
			// Reconstruct path with unprefixed ID
			newPath := "/sessions/" + realID
			if len(parts) > 1 {
				newPath += "/" + parts[1]
			}
			req.URL.Path = newPath
			r.proxyToTunnel(tunnel, w, req)
			return
		}
	}

	// POST /sessions: route by "laptop" field in body or X-Laptop-ID header
	if path == "/sessions" && req.Method == "POST" {
		hostname := req.Header.Get("X-Laptop-ID")
		if hostname == "" {
			// Try to read laptop from body (peek)
			hostname = r.peekLaptopFromBody(req)
		}
		if hostname == "" {
			// Default to first available tunnel
			tunnels := r.getAllTunnels()
			for h := range tunnels {
				hostname = h
				break
			}
		}
		tunnel := r.getTunnel(hostname)
		if tunnel == nil {
			http.Error(w, "laptop not connected: "+hostname, http.StatusServiceUnavailable)
			return
		}
		r.proxyToTunnel(tunnel, w, req)
		return
	}

	// Screenshots/files: route by X-Laptop-ID header
	if strings.HasPrefix(path, "/screenshots") || strings.HasPrefix(path, "/files") {
		hostname := req.Header.Get("X-Laptop-ID")
		if hostname == "" {
			tunnels := r.getAllTunnels()
			for h := range tunnels {
				hostname = h
				break
			}
		}
		tunnel := r.getTunnel(hostname)
		if tunnel == nil {
			http.Error(w, "laptop not connected", http.StatusServiceUnavailable)
			return
		}
		r.proxyToTunnel(tunnel, w, req)
		return
	}

	// Default: proxy to first available tunnel
	tunnels := r.getAllTunnels()
	for _, tunnel := range tunnels {
		r.proxyToTunnel(tunnel, w, req)
		return
	}
	http.Error(w, "no tunnel available", http.StatusServiceUnavailable)
}

// peekLaptopFromBody tries to read the "laptop" field from a JSON request body
// without consuming it. Returns empty string if not found.
func (r *Relay) peekLaptopFromBody(req *http.Request) string {
	// We can't peek without reading, so we skip this for now.
	// The client should set X-Laptop-ID header instead.
	return ""
}

// ── Fan-out helpers ───────────────────────────────────────────────────────

// fanOutSessions sends GET /sessions to tunnels, merges, prefixes IDs.
// If laptopFilter is non-empty, only queries that single tunnel.
func (r *Relay) fanOutSessions(w http.ResponseWriter, req *http.Request, laptopFilter string) {
	tunnels := r.getTunnelsForFilter(laptopFilter)
	if len(tunnels) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]")) //nolint:errcheck
		return
	}

	type result struct {
		hostname string
		sessions []map[string]interface{}
	}

	ch := make(chan result, len(tunnels))
	for hostname, tunnel := range tunnels {
		go func(h string, t *tunnelEntry) {
			resp := r.sendHTTPViaTunnel(t, "GET", "/sessions", req.URL.RawQuery, nil, nil)
			if resp == nil || resp.Status != 200 {
				ch <- result{h, nil}
				return
			}
			body, err := base64.StdEncoding.DecodeString(resp.Body)
			if err != nil {
				ch <- result{h, nil}
				return
			}
			var sessions []map[string]interface{}
			if err := json.Unmarshal(body, &sessions); err != nil {
				ch <- result{h, nil}
				return
			}
			// Add laptop field and prefix IDs
			for i := range sessions {
				if id, ok := sessions[i]["id"].(string); ok {
					sessions[i]["id"] = prefixSessionID(h, id)
				}
				sessions[i]["laptop"] = h
			}
			ch <- result{h, sessions}
		}(hostname, tunnel)
	}

	var merged []map[string]interface{}
	for i := 0; i < len(tunnels); i++ {
		r := <-ch
		if r.sessions != nil {
			merged = append(merged, r.sessions...)
		}
	}
	if merged == nil {
		merged = []map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(merged) //nolint:errcheck
}

// fanOutJSON fans out a GET request to tunnels and merges JSON array responses.
// If laptopFilter is non-empty, only queries that single tunnel.
func (r *Relay) fanOutJSON(w http.ResponseWriter, req *http.Request, path string, laptopFilter string) {
	tunnels := r.getTunnelsForFilter(laptopFilter)
	if len(tunnels) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]")) //nolint:errcheck
		return
	}

	type result struct {
		hostname string
		items    []map[string]interface{}
	}

	ch := make(chan result, len(tunnels))
	for hostname, tunnel := range tunnels {
		go func(h string, t *tunnelEntry) {
			resp := r.sendHTTPViaTunnel(t, "GET", path, req.URL.RawQuery, nil, nil)
			if resp == nil || resp.Status != 200 {
				ch <- result{h, nil}
				return
			}
			body, err := base64.StdEncoding.DecodeString(resp.Body)
			if err != nil {
				ch <- result{h, nil}
				return
			}
			var items []map[string]interface{}
			if err := json.Unmarshal(body, &items); err != nil {
				ch <- result{h, nil}
				return
			}
			for i := range items {
				items[i]["laptop"] = h
			}
			ch <- result{h, items}
		}(hostname, tunnel)
	}

	var merged []map[string]interface{}
	for i := 0; i < len(tunnels); i++ {
		r := <-ch
		if r.items != nil {
			merged = append(merged, r.items...)
		}
	}
	if merged == nil {
		merged = []map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(merged) //nolint:errcheck
}

// sendHTTPViaTunnel sends a single HTTP request through a specific tunnel and waits for response.
func (r *Relay) sendHTTPViaTunnel(t *tunnelEntry, method, path, query string, headers map[string][]string, body []byte) *TunnelMsg {
	id := fmt.Sprintf("req-%d", r.reqSeq.Add(1))

	ch := make(chan *TunnelMsg, 1)
	t.pendMu.Lock()
	t.pending[id] = &pendingHTTP{ch: ch}
	t.pendMu.Unlock()

	bodyB64 := ""
	if len(body) > 0 {
		bodyB64 = base64.StdEncoding.EncodeToString(body)
	}

	if err := t.send(&TunnelMsg{
		Type:    "http_request",
		ID:      id,
		Method:  method,
		Path:    path,
		Query:   query,
		Headers: headers,
		Body:    bodyB64,
	}); err != nil {
		t.pendMu.Lock()
		delete(t.pending, id)
		t.pendMu.Unlock()
		return nil
	}

	select {
	case resp := <-ch:
		return resp
	case <-time.After(25 * time.Second):
		t.pendMu.Lock()
		delete(t.pending, id)
		t.pendMu.Unlock()
		return nil
	}
}

// proxyToTunnel forwards an HTTP request through a specific tunnel.
func (r *Relay) proxyToTunnel(t *tunnelEntry, w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(io.LimitReader(req.Body, 32*1024*1024))
	if err != nil {
		http.Error(w, "body read error", http.StatusInternalServerError)
		return
	}

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

	resp := r.sendHTTPViaTunnel(t, req.Method, req.URL.Path, req.URL.RawQuery, fwdHeaders, body)
	if resp == nil {
		http.Error(w, "tunnel unavailable", http.StatusServiceUnavailable)
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
			w.Write(decoded) //nolint:errcheck
		}
	}
}

// ── Phone WebSocket ────────────────────────────────────────────────────────

// HandleClientWS handles a browser's /ws connection and bridges it through
// all connected tunnels to mclaude-servers on each laptop.
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

	clientID := fmt.Sprintf("ws-%d", r.wsSeq.Add(1))

	ch := make(chan *TunnelMsg, 256)
	client := &wsClient{
		ch:        ch,
		tunnelIDs: make(map[string]string),
	}

	r.wsMu.Lock()
	r.wsClients[clientID] = client
	r.wsMu.Unlock()

	defer func() {
		r.wsMu.Lock()
		delete(r.wsClients, clientID)
		r.wsMu.Unlock()

		// Notify all tunnels that this WS client disconnected
		client.tunnelIDsMu.Lock()
		for hostname, tunnelWsID := range client.tunnelIDs {
			if t := r.getTunnel(hostname); t != nil {
				t.send(&TunnelMsg{ //nolint:errcheck
					Type:   "ws_close",
					ID:     tunnelWsID,
					Code:   1000,
					Reason: "client disconnected",
				})
			}
		}
		client.tunnelIDsMu.Unlock()
	}()

	// Connect to selected laptop's tunnel, or all if none specified
	laptopParam := req.URL.Query().Get("laptop")
	tunnels := r.getTunnelsForFilter(laptopParam)
	for hostname, tunnel := range tunnels {
		tunnelWsID := fmt.Sprintf("%s-%s", clientID, hostname)

		client.tunnelIDsMu.Lock()
		client.tunnelIDs[hostname] = tunnelWsID
		client.tunnelIDsMu.Unlock()

		tunnel.send(&TunnelMsg{ //nolint:errcheck
			Type:  "ws_connect",
			ID:    tunnelWsID,
			Query: req.URL.RawQuery,
		})
	}

	// phone → relay → tunnel(s)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				break
			}

			// For load_more commands, route to the specific tunnel that owns
			// the session and unprefix the session ID before forwarding.
			if msgType == websocket.TextMessage {
				var cmd struct {
					Type  string `json:"type"`
					ID    string `json:"id"`
					Lines int    `json:"lines"`
				}
				if json.Unmarshal(data, &cmd) == nil && cmd.Type == "load_more" && cmd.ID != "" {
					hostname, realID := parsePrefixedID(cmd.ID)
					client.tunnelIDsMu.Lock()
					tunnelWsID, ok := client.tunnelIDs[hostname]
					client.tunnelIDsMu.Unlock()
					if ok {
						if t := r.getTunnel(hostname); t != nil {
							rewritten, _ := json.Marshal(map[string]interface{}{
								"type":  "load_more",
								"id":    realID,
								"lines": cmd.Lines,
							})
							t.send(&TunnelMsg{ //nolint:errcheck
								Type:   "ws_message",
								ID:     tunnelWsID,
								Data:   base64.StdEncoding.EncodeToString(rewritten),
								Binary: false,
							})
						}
					}
					continue
				}
			}

			encoded := base64.StdEncoding.EncodeToString(data)
			// Forward all other messages to all tunnels
			client.tunnelIDsMu.Lock()
			for hostname, tunnelWsID := range client.tunnelIDs {
				if t := r.getTunnel(hostname); t != nil {
					t.send(&TunnelMsg{ //nolint:errcheck
						Type:   "ws_message",
						ID:     tunnelWsID,
						Data:   encoded,
						Binary: msgType == websocket.BinaryMessage,
					})
				}
			}
			client.tunnelIDsMu.Unlock()
		}
	}()

	// all tunnels → relay → phone
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg.Type == "ws_close" {
				// One tunnel closed, but don't disconnect the phone.
				// Only disconnect if ALL tunnels are gone.
				client.tunnelIDsMu.Lock()
				remaining := len(client.tunnelIDs)
				client.tunnelIDsMu.Unlock()
				if remaining == 0 {
					return
				}
				continue
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

// ── PTY WebSocket ──────────────────────────────────────────────────────────

// HandlePtyWS handles a browser's /ws/pty connection and bridges it to a
// PTY session on the target laptop's connector via the tunnel.
func (r *Relay) HandlePtyWS(w http.ResponseWriter, req *http.Request) {
	if extractToken(req) != r.webToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	hostname := req.URL.Query().Get("laptop")
	if hostname == "" {
		// Default to first available tunnel
		tunnels := r.getAllTunnels()
		for h := range tunnels {
			hostname = h
			break
		}
	}

	tunnel := r.getTunnel(hostname)
	if tunnel == nil {
		http.Error(w, "laptop not connected: "+hostname, http.StatusServiceUnavailable)
		return
	}

	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("pty ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	ptyID := fmt.Sprintf("pty-%d", r.ptySeq.Add(1))

	rows := 24
	cols := 80
	if v := req.URL.Query().Get("rows"); v != "" {
		fmt.Sscanf(v, "%d", &rows)
	}
	if v := req.URL.Query().Get("cols"); v != "" {
		fmt.Sscanf(v, "%d", &cols)
	}

	ps := &ptySession{
		clientConn: conn,
		hostname:   hostname,
	}
	r.ptyMu.Lock()
	r.ptySessions[ptyID] = ps
	r.ptyMu.Unlock()

	defer func() {
		r.ptyMu.Lock()
		delete(r.ptySessions, ptyID)
		r.ptyMu.Unlock()

		// Tell connector to close PTY
		if t := r.getTunnel(hostname); t != nil {
			t.send(&TunnelMsg{Type: "pty_close", ID: ptyID}) //nolint:errcheck
		}
	}()

	// Ask connector to spawn PTY
	if err := tunnel.send(&TunnelMsg{
		Type: "pty_connect",
		ID:   ptyID,
		Rows: rows,
		Cols: cols,
	}); err != nil {
		log.Printf("pty_connect send failed: %v", err)
		return
	}

	log.Printf("pty session started: %s → %s", ptyID, hostname)

	// Browser → tunnel: read frames and forward as pty_data / pty_resize
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if msgType == websocket.TextMessage {
			// Check if it's a resize command
			var cmd struct {
				Type string `json:"type"`
				Rows int    `json:"rows"`
				Cols int    `json:"cols"`
			}
			if json.Unmarshal(data, &cmd) == nil && cmd.Type == "resize" {
				if t := r.getTunnel(hostname); t != nil {
					t.send(&TunnelMsg{ //nolint:errcheck
						Type: "pty_resize",
						ID:   ptyID,
						Rows: cmd.Rows,
						Cols: cmd.Cols,
					})
				}
				continue
			}
		}

		// Terminal input
		encoded := base64.StdEncoding.EncodeToString(data)
		if t := r.getTunnel(hostname); t != nil {
			t.send(&TunnelMsg{ //nolint:errcheck
				Type: "pty_data",
				ID:   ptyID,
				Data: encoded,
			})
		}
	}

	log.Printf("pty session ended: %s", ptyID)
}

// ── Helpers ────────────────────────────────────────────────────────────────

func extractToken(req *http.Request) string {
	if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return req.URL.Query().Get("token")
}

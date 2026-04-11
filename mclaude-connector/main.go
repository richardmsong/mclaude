package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
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
	Rows    int    `json:"rows,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Command string `json:"command,omitempty"` // optional command to run instead of default shell
}

// API prefixes that should always be proxied to mclaude-server.
var apiPrefixes = []string{
	"/sessions", "/projects", "/skills",
	"/screenshots", "/files", "/telemetry",
	"/auth/", "/ws", "/tunnel", "/health",
	"/usage", "/admin", "/laptops", "/tmux-sessions",
}

func isAPIPath(path string) bool {
	for _, prefix := range apiPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Connector maintains a persistent tunnel WebSocket to the relay and proxies
// requests to the local mclaude-server.
type Connector struct {
	relayURL      string
	tunnelToken   string
	mclaudeURL    string
	serviceToken  string
	staticDir     string // local directory for static files (empty = proxy all to mclaude-server)
	hostname      string // short hostname sent to relay for multi-laptop identification
	tlsSkipVerify bool

	sendMu  sync.Mutex
	conn    *websocket.Conn
	connMu  sync.RWMutex

	wsMu    sync.Mutex
	wsConns map[string]*wsEntry

	ptyMu       sync.Mutex
	ptySessions map[string]*ptyEntry

	httpClient *http.Client
	wsDialer   *websocket.Dialer

	sendSeq atomic.Uint64
}

// wsEntry wraps a websocket connection with a write mutex to prevent concurrent writes.
type wsEntry struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type ptyEntry struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func NewConnector(relayURL, tunnelToken, mclaudeURL, serviceToken, staticDir, hostname string, tlsSkipVerify bool) *Connector {
	tlsCfg := &tls.Config{InsecureSkipVerify: tlsSkipVerify} //nolint:gosec
	return &Connector{
		relayURL:      relayURL,
		tunnelToken:   tunnelToken,
		mclaudeURL:    mclaudeURL,
		serviceToken:  serviceToken,
		staticDir:     staticDir,
		hostname:      hostname,
		tlsSkipVerify: tlsSkipVerify,
		wsConns:       make(map[string]*wsEntry),
		ptySessions:   make(map[string]*ptyEntry),
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
	hdr.Set("X-Hostname", c.hostname)

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
		for id, entry := range c.wsConns {
			entry.conn.Close()
			delete(c.wsConns, id)
		}
		c.wsMu.Unlock()

		// close all PTY sessions
		c.ptyMu.Lock()
		for id, entry := range c.ptySessions {
			entry.ptmx.Close()
			entry.cmd.Process.Kill()
			delete(c.ptySessions, id)
		}
		c.ptyMu.Unlock()
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
	case "pty_connect":
		c.handlePtyConnect(msg)
	case "pty_data":
		c.handlePtyData(msg)
	case "pty_resize":
		c.handlePtyResize(msg)
	case "pty_close":
		c.handlePtyClose(msg)
	}
}

// ── HTTP proxy ─────────────────────────────────────────────────────────────

func (c *Connector) handleHTTP(msg *TunnelMsg) {
	// Serve static files from local disk when configured and path is non-API
	if c.staticDir != "" && !isAPIPath(msg.Path) {
		if msg.Path == "/__static-version" {
			c.serveStaticVersion(msg)
			return
		}
		c.serveStaticFile(msg)
		return
	}

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

// serveStaticFile reads a file from the local static directory and sends it
// back through the tunnel. Enables editing files locally and refreshing the
// remote browser without redeploying anything.
func (c *Connector) serveStaticFile(msg *TunnelMsg) {
	path := msg.Path
	if path == "" || path == "/" {
		path = "/index.html"
	}

	// Sanitise: clean the path, ensure it stays under staticDir
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		c.sendErrorResp(msg.ID, http.StatusForbidden, "invalid path")
		return
	}

	filePath := filepath.Join(c.staticDir, cleaned)
	data, err := os.ReadFile(filePath)
	if err != nil {
		c.sendErrorResp(msg.ID, http.StatusNotFound, "not found")
		return
	}

	ct := mime.TypeByExtension(filepath.Ext(filePath))
	if ct == "" {
		ct = http.DetectContentType(data)
	}

	c.send(&TunnelMsg{ //nolint:errcheck
		Type:   "http_response",
		ID:     msg.ID,
		Status: http.StatusOK,
		Headers: map[string][]string{
			"content-type":  {ct},
			"cache-control": {"no-cache, no-store, must-revalidate"},
		},
		Body: base64.StdEncoding.EncodeToString(data),
	})
}

// serveStaticVersion returns the mtime of index.html so the web app can
// detect changes and auto-reload.
func (c *Connector) serveStaticVersion(msg *TunnelMsg) {
	indexPath := filepath.Join(c.staticDir, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		c.sendErrorResp(msg.ID, http.StatusNotFound, "index.html not found")
		return
	}
	body := fmt.Sprintf(`{"mtime":%d}`, info.ModTime().UnixMilli())
	c.send(&TunnelMsg{ //nolint:errcheck
		Type:   "http_response",
		ID:     msg.ID,
		Status: http.StatusOK,
		Headers: map[string][]string{
			"content-type":  {"application/json"},
			"cache-control": {"no-cache, no-store"},
		},
		Body: base64.StdEncoding.EncodeToString([]byte(body)),
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
	entry := &wsEntry{conn: wsConn}
	c.wsMu.Lock()
	c.wsConns[wsID] = entry
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
	entry, ok := c.wsConns[msg.ID]
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
	entry.mu.Lock()
	entry.conn.WriteMessage(mt, data) //nolint:errcheck
	entry.mu.Unlock()
}

func (c *Connector) handleWSClose(msg *TunnelMsg) {
	c.wsMu.Lock()
	entry, ok := c.wsConns[msg.ID]
	if ok {
		delete(c.wsConns, msg.ID)
	}
	c.wsMu.Unlock()
	if ok {
		entry.conn.Close()
	}
}

func (c *Connector) notifyWSClose(id string, code int, reason string) {
	c.send(&TunnelMsg{Type: "ws_close", ID: id, Code: code, Reason: reason}) //nolint:errcheck
}

// ── PTY bridge ────────────────────────────────────────────────────────────

func (c *Connector) handlePtyConnect(msg *TunnelMsg) {
	var cmd *exec.Cmd
	if msg.Command != "" && strings.HasPrefix(msg.Command, "tmux ") {
		// Only allow tmux commands for safety
		parts := strings.Fields(msg.Command)
		cmd = exec.Command(parts[0], parts[1:]...)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd = exec.Command(shell)
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		c.send(&TunnelMsg{Type: "pty_close", ID: msg.ID, Reason: err.Error()}) //nolint:errcheck
		return
	}

	if msg.Rows > 0 && msg.Cols > 0 {
		pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(msg.Rows), Cols: uint16(msg.Cols)}) //nolint:errcheck
	}

	c.ptyMu.Lock()
	c.ptySessions[msg.ID] = &ptyEntry{ptmx: ptmx, cmd: cmd}
	c.ptyMu.Unlock()

	log.Printf("pty session started: %s (%s)", msg.ID, cmd.Path)

	// Read PTY output → tunnel
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				c.send(&TunnelMsg{ //nolint:errcheck
					Type: "pty_data",
					ID:   msg.ID,
					Data: base64.StdEncoding.EncodeToString(buf[:n]),
				})
			}
			if err != nil {
				break
			}
		}
		cmd.Wait() //nolint:errcheck
		c.ptyMu.Lock()
		delete(c.ptySessions, msg.ID)
		c.ptyMu.Unlock()
		c.send(&TunnelMsg{Type: "pty_close", ID: msg.ID, Reason: "exited"}) //nolint:errcheck
		log.Printf("pty session ended: %s", msg.ID)
	}()
}

func (c *Connector) handlePtyData(msg *TunnelMsg) {
	c.ptyMu.Lock()
	entry, ok := c.ptySessions[msg.ID]
	c.ptyMu.Unlock()
	if !ok {
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return
	}
	entry.ptmx.Write(data) //nolint:errcheck
}

func (c *Connector) handlePtyResize(msg *TunnelMsg) {
	c.ptyMu.Lock()
	entry, ok := c.ptySessions[msg.ID]
	c.ptyMu.Unlock()
	if !ok {
		return
	}
	if msg.Rows > 0 && msg.Cols > 0 {
		pty.Setsize(entry.ptmx, &pty.Winsize{Rows: uint16(msg.Rows), Cols: uint16(msg.Cols)}) //nolint:errcheck
	}
}

func (c *Connector) handlePtyClose(msg *TunnelMsg) {
	c.ptyMu.Lock()
	entry, ok := c.ptySessions[msg.ID]
	if ok {
		delete(c.ptySessions, msg.ID)
	}
	c.ptyMu.Unlock()
	if ok {
		entry.ptmx.Close()
		entry.cmd.Process.Kill()
		log.Printf("pty session closed: %s", msg.ID)
	}
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
	staticDir := os.Getenv("STATIC_DIR")

	// Hostname for multi-laptop identification.
	// Defaults to short hostname (before first dot). Override with CONNECTOR_NAME.
	hostname := os.Getenv("CONNECTOR_NAME")
	if hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			h = "unknown"
		}
		if idx := strings.Index(h, "."); idx > 0 {
			h = h[:idx]
		}
		hostname = h
	}

	if tlsSkipVerify {
		log.Println("TLS verification disabled for mclaude-server (TLS_SKIP_VERIFY=true)")
	}

	if staticDir != "" {
		log.Printf("Serving static files from local disk: %s", staticDir)
	}

	log.Printf("mclaude-connector starting  relay=%s  mclaude=%s  hostname=%s", relayURL, mclaudeURL, hostname)
	conn := NewConnector(relayURL, tunnelToken, mclaudeURL, serviceToken, staticDir, hostname, tlsSkipVerify)
	conn.Run()
}

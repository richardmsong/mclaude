// Package cmd — anthropic.go implements "mclaude auth anthropic".
//
// Spec: docs/mclaude-cli/spec-cli.md §mclaude auth anthropic
// ADR:  docs/adr-0091-per-user-anthropic-token.md
//
// Sub-commands:
//
//	mclaude auth anthropic              — PKCE OAuth via loopback listener
//	mclaude auth anthropic --token <v>  — manual paste; skips OAuth
//	mclaude auth anthropic --status     — print connected/not-set + per-host pubkey status
//	mclaude auth anthropic --revoke     — delete tokens.anthropic from KV (client-side only)
//
// Exit codes (returned by RunAuthAnthropic):
//
//	0 — success on all hosts
//	1 — token saved but at least one host could not receive it (retry later)
//	2 — no token configured / OAuth aborted
//	3 — KV write failed
package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	clicontext "mclaude-cli/context"
	"golang.org/x/crypto/nacl/box"
)

// --------------------------------------------------------------------------
// OAuth endpoint configuration (overridable for tests)
// --------------------------------------------------------------------------

// AnthropicOAuthConfig holds the OAuth 2.0 PKCE parameters for claude.ai.
// All fields are exported so tests can inject mock values.
type AnthropicOAuthConfig struct {
	// AuthorizeURL is the claude.ai OAuth authorization endpoint.
	// Default: https://claude.ai/oauth/authorize
	AuthorizeURL string
	// TokenURL is the claude.ai OAuth token exchange endpoint.
	// Default: https://claude.ai/oauth/token
	TokenURL string
	// ClientID is the OAuth 2.0 client_id registered for mclaude-cli.
	// Default: mclaude-cli (overridden by ANTHROPIC_OAUTH_CLIENT_ID env var in production)
	ClientID string
	// RedirectPort is the loopback port for the PKCE redirect.
	// 0 means "auto-assign" (OS picks an available port). Tests set a fixed port.
	RedirectPort int
}

// DefaultAnthropicOAuthConfig returns the production oauth config.
func DefaultAnthropicOAuthConfig() AnthropicOAuthConfig {
	return AnthropicOAuthConfig{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		TokenURL:     "https://claude.ai/oauth/token",
		ClientID:     "mclaude-cli",
		RedirectPort: 0, // OS-assigned
	}
}

// --------------------------------------------------------------------------
// AuthAnthropicFlags
// --------------------------------------------------------------------------

// AuthAnthropicFlags holds the parsed flags for "mclaude auth anthropic".
type AuthAnthropicFlags struct {
	// Token is the Anthropic access token (--token <v>). When non-empty, OAuth is skipped.
	Token string
	// Status requests status output instead of token setup.
	Status bool
	// Revoke requests deletion of tokens.anthropic from KV.
	Revoke bool

	// Overrides for tests.
	ContextPath string
	AuthPath    string
	// OAuthConfig overrides the default PKCE OAuth endpoints for tests.
	OAuthConfig *AnthropicOAuthConfig
	// NATSConn injects a pre-connected NATS connection for tests.
	NATSConn NATSConn
	// HTTPClient injects a custom HTTP client for tests (token exchange).
	HTTPClient *http.Client
	// ServerURL overrides the control-plane base URL.
	ServerURL string
}

// AuthAnthropicResult is returned by RunAuthAnthropic on success.
type AuthAnthropicResult struct {
	// ExitCode is one of 0, 1, 2, 3 (see spec).
	ExitCode int
	// HostResults contains per-host delivery results.
	HostResults []HostDeliveryResult
}

// HostDeliveryResult holds the result of delivering a token to one host.
type HostDeliveryResult struct {
	HostSlug  string
	Delivered bool
	Err       error
}

// --------------------------------------------------------------------------
// Exit code constants (spec §mclaude auth anthropic)
// --------------------------------------------------------------------------

const (
	// ExitAuthSuccess means all hosts received the token successfully.
	ExitAuthSuccess = 0
	// ExitAuthPartialDelivery means token was saved but ≥1 host could not receive it.
	ExitAuthPartialDelivery = 1
	// ExitAuthNoToken means no token was configured or OAuth was aborted.
	ExitAuthNoToken = 2
	// ExitAuthKVFailed means the KV write failed.
	ExitAuthKVFailed = 3
)

// --------------------------------------------------------------------------
// Narrow KV interfaces (avoids implementing the full nats.JetStreamContext
// in tests — we only need KV get/put/delete operations).
// --------------------------------------------------------------------------

// KVEntry is a narrow interface for nats.KeyValueEntry (only Value() is needed).
// Exported for test implementations.
type KVEntry interface {
	Value() []byte
}

// KVBucket is a narrow interface over nats.KeyValue (only the ops we use).
// Exported for test implementations.
type KVBucket interface {
	Get(key string) (KVEntry, error)
	Put(key string, value []byte) (uint64, error)
	Delete(key string, opts ...nats.DeleteOpt) error
}

// KVAccessor is a narrow interface that can open a KV bucket by name.
// Implemented by both the production NATS wrapper and test mocks.
// Exported for test implementations.
type KVAccessor interface {
	KeyValue(bucket string) (KVBucket, error)
}

// jsKVAccessor is an alias for KVAccessor (used internally).
type jsKVAccessor = KVAccessor

// realJSKVAccessor wraps nats.JetStreamContext to implement KVAccessor.
// nats.KeyValue.Get() returns nats.KeyValueEntry which satisfies KVEntry.
type realJSKVAccessor struct {
	js nats.JetStreamContext
}

func (r *realJSKVAccessor) KeyValue(bucket string) (KVBucket, error) {
	kv, err := r.js.KeyValue(bucket)
	if err != nil {
		return nil, err
	}
	return &realKVBucket{kv: kv}, nil
}

// realKVBucket wraps nats.KeyValue to implement KVBucket (adapts KVEntry return).
type realKVBucket struct {
	kv nats.KeyValue
}

func (r *realKVBucket) Get(key string) (KVEntry, error) {
	return r.kv.Get(key)
}
func (r *realKVBucket) Put(key string, value []byte) (uint64, error) {
	return r.kv.Put(key, value)
}
func (r *realKVBucket) Delete(key string, opts ...nats.DeleteOpt) error {
	return r.kv.Delete(key, opts...)
}

// --------------------------------------------------------------------------
// NATS KV operations
// --------------------------------------------------------------------------

// anthropicNATSConn wraps a *nats.Conn to implement NATSConn as well as expose
// KV-specific operations.
type anthropicNATSConn interface {
	NATSConn
	getJSKVAccessor() (KVAccessor, error)
}

// natsConnWrapper wraps *nats.Conn to implement anthropicNATSConn.
type natsConnWrapper struct {
	*nats.Conn
}

func (w *natsConnWrapper) getJSKVAccessor() (KVAccessor, error) {
	js, err := w.Conn.JetStream()
	if err != nil {
		return nil, err
	}
	return &realJSKVAccessor{js: js}, nil
}

// connectNATSForAnthropic connects to NATS and returns an anthropicNATSConn.
func connectNATSForAnthropic(natsURL, jwt, nkeySeed string) (anthropicNATSConn, error) {
	kp, err := nkeys.FromSeed([]byte(nkeySeed))
	if err != nil {
		return nil, fmt.Errorf("parse nkey seed: %w", err)
	}
	nc, err := nats.Connect(natsURL,
		nats.UserJWT(
			func() (string, error) { return jwt, nil },
			func(nonce []byte) ([]byte, error) { return kp.Sign(nonce) },
		),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS at %s: %w", natsURL, err)
	}
	return &natsConnWrapper{nc}, nil
}

// kvPutToken writes the token to mclaude-user-info-{uslug} at key tokens.anthropic.
func kvPutToken(js jsKVAccessor, uslug, token string) error {
	bucketName := fmt.Sprintf("mclaude-user-info-%s", uslug)
	kv, err := js.KeyValue(bucketName)
	if err != nil {
		return fmt.Errorf("open KV bucket %s: %w", bucketName, err)
	}
	if _, err := kv.Put("tokens.anthropic", []byte(token)); err != nil {
		return fmt.Errorf("KV put tokens.anthropic in %s: %w", bucketName, err)
	}
	return nil
}

// kvGetToken reads tokens.anthropic from mclaude-user-info-{uslug}.
// Returns ("", nil) if the key does not exist.
func kvGetToken(js jsKVAccessor, uslug string) (string, error) {
	bucketName := fmt.Sprintf("mclaude-user-info-%s", uslug)
	kv, err := js.KeyValue(bucketName)
	if err != nil {
		return "", fmt.Errorf("open KV bucket %s: %w", bucketName, err)
	}
	entry, err := kv.Get("tokens.anthropic")
	if err != nil {
		if err == nats.ErrKeyNotFound {
			return "", nil
		}
		return "", fmt.Errorf("KV get tokens.anthropic: %w", err)
	}
	return string(entry.Value()), nil
}

// kvDeleteToken deletes tokens.anthropic from mclaude-user-info-{uslug}.
func kvDeleteToken(js jsKVAccessor, uslug string) error {
	bucketName := fmt.Sprintf("mclaude-user-info-%s", uslug)
	kv, err := js.KeyValue(bucketName)
	if err != nil {
		return fmt.Errorf("open KV bucket %s: %w", bucketName, err)
	}
	if err := kv.Delete("tokens.anthropic"); err != nil && err != nats.ErrKeyNotFound {
		return fmt.Errorf("KV delete tokens.anthropic: %w", err)
	}
	return nil
}

// --------------------------------------------------------------------------
// Control-plane HTTP helpers
// --------------------------------------------------------------------------

// grantsResponse is the response from GET /api/users/{uslug}/grants.
type grantsResponse struct {
	Grants []grantEntry `json:"grants"`
}

type grantEntry struct {
	HostSlug string `json:"hostSlug"`
	Status   string `json:"status"` // "pending", "accepted", "revoked"
}

// fetchAcceptedGrants calls GET /api/users/{uslug}/grants and returns the
// slugs of accepted hosts.
func fetchAcceptedGrants(serverURL, uslug, jwt string, httpClient *http.Client) ([]string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	reqURL := fmt.Sprintf("%s/api/users/%s/grants", serverURL, uslug)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET /grants request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /api/users/%s/grants: %w", uslug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/users/%s/grants: unexpected status %d", uslug, resp.StatusCode)
	}
	var gr grantsResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, fmt.Errorf("decode grants response: %w", err)
	}
	var accepted []string
	for _, g := range gr.Grants {
		if g.Status == "accepted" {
			accepted = append(accepted, g.HostSlug)
		}
	}
	return accepted, nil
}

// pubkeyResponse is the response from GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey.
type pubkeyResponse struct {
	// X25519Pub is the base64-encoded X25519 public key (32 bytes).
	X25519Pub string `json:"x25519Pub"`
}

// fetchUserAgentPubkey calls GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey.
// Returns ("", nil) if the pubkey is not registered yet (204 or empty body).
func fetchUserAgentPubkey(serverURL, uslug, hslug, jwt string, httpClient *http.Client) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	reqURL := fmt.Sprintf("%s/api/users/%s/hosts/%s/user-agent/pubkey", serverURL, uslug, hslug)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create pubkey request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET user-agent/pubkey for %s: %w", hslug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET user-agent/pubkey for %s: unexpected status %d", hslug, resp.StatusCode)
	}
	var pr pubkeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", fmt.Errorf("decode pubkey response: %w", err)
	}
	return pr.X25519Pub, nil
}

// --------------------------------------------------------------------------
// Sealed-box wrapping (X25519 / NaCl)
// --------------------------------------------------------------------------

// SealToken wraps token for the given X25519 public key using NaCl anonymous
// sealed box (crypto_box_seal). Returns base64-encoded ciphertext.
//
// x25519PubBase64 must be standard base64-encoded 32 bytes.
// Exported for testing.
func SealToken(token, x25519PubBase64 string) (string, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(x25519PubBase64)
	if err != nil {
		// Try raw-URL encoding as fallback.
		pubBytes, err = base64.RawURLEncoding.DecodeString(x25519PubBase64)
		if err != nil {
			return "", fmt.Errorf("decode X25519 pubkey: %w", err)
		}
	}
	if len(pubBytes) != 32 {
		return "", fmt.Errorf("X25519 pubkey must be 32 bytes, got %d", len(pubBytes))
	}
	var recipientKey [32]byte
	copy(recipientKey[:], pubBytes)

	sealed, err := box.SealAnonymous(nil, []byte(token), &recipientKey, rand.Reader)
	if err != nil {
		return "", fmt.Errorf("sealed-box encrypt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// --------------------------------------------------------------------------
// Token delivery — NATS request/reply
// --------------------------------------------------------------------------

// tokenDeliveryPayload is the NATS payload for agent.token-delivery.
// Spec: ADR-0091 §Phase 3
type tokenDeliveryPayload struct {
	Provider     string `json:"provider"`
	TokenWrapped string `json:"token_wrapped"`
}

// DeliverTokenToHost seals the token and sends it to the user-agent via NATS
// request/reply on mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery.
// Exported for testing.
func DeliverTokenToHost(nc NATSConn, uslug, hslug, token, pubkeyBase64 string) error {
	wrapped, err := SealToken(token, pubkeyBase64)
	if err != nil {
		return fmt.Errorf("seal token for %s: %w", hslug, err)
	}

	payload := tokenDeliveryPayload{
		Provider:     "anthropic",
		TokenWrapped: wrapped,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal delivery payload: %w", err)
	}

	subject := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-delivery", uslug, hslug)
	msg, err := nc.Request(subject, data, 15*time.Second)
	if err != nil {
		return fmt.Errorf("NATS request to %s: %w", subject, err)
	}

	// Check for error response.
	var respMap map[string]interface{}
	if err := json.Unmarshal(msg.Data, &respMap); err == nil {
		if errMsg, ok := respMap["error"].(string); ok && errMsg != "" {
			return fmt.Errorf("user-agent returned error: %s", errMsg)
		}
	}
	return nil
}

// --------------------------------------------------------------------------
// PKCE OAuth flow (loopback listener)
// --------------------------------------------------------------------------

// PKCEParams holds the generated PKCE code verifier and challenge.
// Exported for testing.
type PKCEParams struct {
	Verifier  string
	Challenge string // base64url(SHA256(verifier))
	State     string
}

// GeneratePKCE generates a PKCE code_verifier and code_challenge.
// Exported for testing.
func GeneratePKCE() (PKCEParams, error) {
	// code_verifier: 32 random bytes, base64url-encoded (no padding).
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return PKCEParams{}, fmt.Errorf("generate code_verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// code_challenge: SHA256(verifier), base64url-encoded (no padding).
	challenge := ComputeS256Challenge(verifier)

	// state: 16 random bytes, base64url-encoded.
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return PKCEParams{}, fmt.Errorf("generate state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	return PKCEParams{Verifier: verifier, Challenge: challenge, State: state}, nil
}

// ComputeS256Challenge computes the S256 code_challenge from a code_verifier.
// challenge = base64url(SHA256(verifier)). Exported for testing.
func ComputeS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// StartLoopbackListener starts an HTTP listener on the specified port (or a
// random port when port == 0). Returns the listener and the full redirect URI.
// Exported for testing.
func StartLoopbackListener(port int) (net.Listener, string, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("start loopback listener on %s: %w", addr, err)
	}
	// Get the actual port (for port == 0 / OS-assigned).
	actualAddr := ln.Addr().(*net.TCPAddr)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", actualAddr.Port)
	return ln, redirectURI, nil
}

// openBrowser opens the given URL in the default system browser.
// Unexported — not testable directly (system dependency).
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("unsupported OS %q: open %s in your browser manually", runtime.GOOS, rawURL)
	}
	return cmd.Start()
}

// BuildAuthURL constructs the OAuth 2.0 authorization URL with PKCE parameters.
// Exported for testing.
func BuildAuthURL(cfg AnthropicOAuthConfig, pkce PKCEParams, redirectURI string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", cfg.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", pkce.State)
	return cfg.AuthorizeURL + "?" + params.Encode()
}

// LoopbackCallbackResult holds the code/state returned by the OAuth redirect.
// Exported for testing.
type LoopbackCallbackResult struct {
	Code    string
	State   string
	ErrDesc string
}

// WaitForCallback listens on ln for a single OAuth redirect callback.
// Returns the code and state from the callback URL query params, or an error.
// Sends a simple HTML response to close the browser tab.
// Exported for testing.
func WaitForCallback(ctx context.Context, ln net.Listener) (LoopbackCallbackResult, error) {
	resultCh := make(chan LoopbackCallbackResult, 1)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			result := LoopbackCallbackResult{
				Code:    q.Get("code"),
				State:   q.Get("state"),
				ErrDesc: q.Get("error_description"),
			}
			if result.ErrDesc == "" {
				result.ErrDesc = q.Get("error")
			}
			body := `<html><body><h2>You can close this tab and return to the terminal.</h2></body></html>`
			if result.ErrDesc != "" {
				body = fmt.Sprintf(`<html><body><h2>Authentication failed: %s</h2></body></html>`, result.ErrDesc)
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, body)

			select {
			case resultCh <- result:
			default:
			}
		}),
	}

	// Serve in background.
	go func() {
		_ = srv.Serve(ln)
	}()

	// Wait for callback or context cancellation.
	select {
	case result := <-resultCh:
		_ = srv.Shutdown(context.Background())
		return result, nil
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return LoopbackCallbackResult{}, ctx.Err()
	}
}

// tokenExchangeResponse is the response body from the OAuth token endpoint.
type tokenExchangeResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

// ExchangeCodeForToken exchanges an OAuth authorization code for an access_token.
// Exported for testing.
func ExchangeCodeForToken(cfg AnthropicOAuthConfig, code, redirectURI, verifier string, httpClient *http.Client) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("client_id", cfg.ClientID)
	params.Set("code", code)
	params.Set("redirect_uri", redirectURI)
	params.Set("code_verifier", verifier)

	req, err := http.NewRequest(http.MethodPost, cfg.TokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", cfg.TokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var tr tokenExchangeResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode token response (status %d): %w", resp.StatusCode, err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("token exchange error %q: %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token endpoint returned empty access_token (status %d)", resp.StatusCode)
	}
	return tr.AccessToken, nil
}

// runPKCEOAuth performs the full PKCE OAuth flow and returns the access_token.
// Returns ("", nil) if the user aborts (context cancelled).
func runPKCEOAuth(ctx context.Context, cfg AnthropicOAuthConfig, out io.Writer, httpClient *http.Client) (string, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return "", err
	}

	ln, redirectURI, err := StartLoopbackListener(cfg.RedirectPort)
	if err != nil {
		return "", err
	}

	authURL := BuildAuthURL(cfg, pkce, redirectURI)

	fmt.Fprintln(out, "Opening Anthropic OAuth in your browser...")
	fmt.Fprintf(out, "  %s\n", authURL)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "If the browser does not open automatically, copy and paste the URL above.")

	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(out, "warning: could not open browser: %v\n", err)
	}

	fmt.Fprintln(out, "Waiting for OAuth callback...")

	result, err := WaitForCallback(ctx, ln)
	if err != nil {
		return "", fmt.Errorf("OAuth callback: %w (did you cancel?)", err)
	}

	if result.ErrDesc != "" {
		return "", fmt.Errorf("OAuth authorization failed: %s", result.ErrDesc)
	}
	if result.Code == "" {
		return "", fmt.Errorf("OAuth callback did not return a code")
	}
	if result.State != pkce.State {
		return "", fmt.Errorf("OAuth state mismatch (CSRF check failed)")
	}

	fmt.Fprintln(out, "Authorization code received, exchanging for access token...")

	token, err := ExchangeCodeForToken(cfg, result.Code, redirectURI, pkce.Verifier, httpClient)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	return token, nil
}

// --------------------------------------------------------------------------
// RunAuthAnthropic — main entry point
// --------------------------------------------------------------------------

// RunAuthAnthropic implements "mclaude auth anthropic" and its sub-commands.
// Returns (exitCode, error). The exit code is one of ExitAuth*.
func RunAuthAnthropic(flags AuthAnthropicFlags, out io.Writer) (*AuthAnthropicResult, error) {
	// Resolve context.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		return &AuthAnthropicResult{ExitCode: ExitAuthKVFailed}, fmt.Errorf("load context: %w", err)
	}

	// Load auth credentials.
	authPath := flags.AuthPath
	if authPath == "" {
		authPath = DefaultAuthPath()
	}
	creds, err := LoadAuth(authPath)
	if err != nil {
		return &AuthAnthropicResult{ExitCode: ExitAuthNoToken}, err
	}

	uslug := creds.UserSlug
	if uslug == "" {
		uslug = ctx.UserSlug
	}
	if uslug == "" {
		return &AuthAnthropicResult{ExitCode: ExitAuthNoToken},
			fmt.Errorf("user slug not found in credentials or context: run 'mclaude login' again")
	}

	// Resolve NATS URL.
	serverURL := clicontext.ResolveServerURL(flags.ServerURL, ctx)
	natsURL := creds.NATSUrl
	if natsURL == "" {
		natsURL = clicontext.DeriveNATSURL(serverURL)
	}

	// Resolve HTTP client for CP calls.
	httpClient := flags.HTTPClient

	// Connect to NATS (shared across sub-commands).
	var natsConn anthropicNATSConn
	if flags.NATSConn != nil {
		// Test injection: wrap the injected NATSConn.
		natsConn = &injectedNATSConnWrapper{conn: flags.NATSConn}
	} else {
		natsConn, err = connectNATSForAnthropic(natsURL, creds.JWT, creds.NKeySeed)
		if err != nil {
			return &AuthAnthropicResult{ExitCode: ExitAuthKVFailed},
				fmt.Errorf("connect to NATS: %w", err)
		}
		defer natsConn.Close()
	}

	kvAcc, err := natsConn.getJSKVAccessor()
	if err != nil {
		return &AuthAnthropicResult{ExitCode: ExitAuthKVFailed},
			fmt.Errorf("JetStream context: %w", err)
	}

	// --revoke
	if flags.Revoke {
		return runRevoke(kvAcc, uslug, out)
	}

	// --status
	if flags.Status {
		return runStatus(kvAcc, uslug, serverURL, creds.JWT, httpClient, out)
	}

	// Token acquisition: --token or PKCE OAuth.
	var token string
	if flags.Token != "" {
		token = flags.Token
	} else {
		// PKCE OAuth flow.
		oauthCfg := DefaultAnthropicOAuthConfig()
		if flags.OAuthConfig != nil {
			oauthCfg = *flags.OAuthConfig
		}
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		token, err = runPKCEOAuth(bgCtx, oauthCfg, out, httpClient)
		if err != nil {
			fmt.Fprintf(out, "OAuth aborted: %v\n", err)
			return &AuthAnthropicResult{ExitCode: ExitAuthNoToken}, nil
		}
		if token == "" {
			fmt.Fprintln(out, "OAuth aborted.")
			return &AuthAnthropicResult{ExitCode: ExitAuthNoToken}, nil
		}
	}

	// Step 3: write token to KV.
	fmt.Fprintln(out, "Saving token to mclaude-user-info KV...")
	if err := kvPutToken(kvAcc, uslug, token); err != nil {
		fmt.Fprintf(out, "error: KV write failed: %v\n", err)
		return &AuthAnthropicResult{ExitCode: ExitAuthKVFailed}, nil
	}
	fmt.Fprintln(out, "Token saved.")

	// Step 4: deliver to all accepted hosts.
	fmt.Fprintln(out, "Delivering token to accepted hosts...")
	hostResults, err := deliverToAcceptedHosts(natsConn, uslug, serverURL, creds.JWT, token, httpClient, out)
	if err != nil {
		// Non-fatal — token is saved; delivery failures are exit code 1.
		fmt.Fprintf(out, "warning: host delivery error: %v\n", err)
	}

	exitCode := ExitAuthSuccess
	for _, r := range hostResults {
		if !r.Delivered {
			exitCode = ExitAuthPartialDelivery
			break
		}
	}

	if exitCode == ExitAuthSuccess {
		fmt.Fprintln(out, "Done. Token saved and delivered to all accepted hosts.")
	} else {
		fmt.Fprintln(out, "Token saved. Some hosts could not receive the token — retry later.")
	}

	return &AuthAnthropicResult{
		ExitCode:    exitCode,
		HostResults: hostResults,
	}, nil
}

// runRevoke deletes tokens.anthropic from KV.
func runRevoke(js jsKVAccessor, uslug string, out io.Writer) (*AuthAnthropicResult, error) {
	fmt.Fprintln(out, "Revoking Anthropic token from KV (client-side only)...")
	if err := kvDeleteToken(js, uslug); err != nil {
		return &AuthAnthropicResult{ExitCode: ExitAuthKVFailed},
			fmt.Errorf("delete token: %w", err)
	}
	fmt.Fprintln(out, "Token deleted from mclaude-user-info KV.")
	fmt.Fprintln(out, "Note: host-side cleanup (Secret deletion, JWT revocation) is deferred to a future release.")
	return &AuthAnthropicResult{ExitCode: ExitAuthSuccess}, nil
}

// runStatus reads KV + per-host pubkey status.
func runStatus(
	js jsKVAccessor,
	uslug, serverURL, jwt string,
	httpClient *http.Client,
	out io.Writer,
) (*AuthAnthropicResult, error) {
	token, err := kvGetToken(js, uslug)
	if err != nil {
		return &AuthAnthropicResult{ExitCode: ExitAuthKVFailed},
			fmt.Errorf("read token status: %w", err)
	}

	if token == "" {
		fmt.Fprintln(out, "Anthropic token: Not set")
		return &AuthAnthropicResult{ExitCode: ExitAuthNoToken}, nil
	}
	fmt.Fprintln(out, "Anthropic token: Connected")

	// Per-host pubkey status.
	acceptedHosts, err := fetchAcceptedGrants(serverURL, uslug, jwt, httpClient)
	if err != nil {
		fmt.Fprintf(out, "warning: could not fetch accepted grants: %v\n", err)
		return &AuthAnthropicResult{ExitCode: ExitAuthSuccess}, nil
	}

	if len(acceptedHosts) == 0 {
		fmt.Fprintln(out, "No accepted hosts.")
		return &AuthAnthropicResult{ExitCode: ExitAuthSuccess}, nil
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Host trust status:")
	for _, hslug := range acceptedHosts {
		pubkey, err := fetchUserAgentPubkey(serverURL, uslug, hslug, jwt, httpClient)
		if err != nil {
			fmt.Fprintf(out, "  %-30s  pubkey: error (%v)\n", hslug, err)
			continue
		}
		if pubkey == "" {
			fmt.Fprintf(out, "  %-30s  pubkey: not registered (user-agent not yet started)\n", hslug)
		} else {
			fmt.Fprintf(out, "  %-30s  pubkey: registered (deliverable)\n", hslug)
		}
	}
	return &AuthAnthropicResult{ExitCode: ExitAuthSuccess}, nil
}

// deliverToAcceptedHosts fetches accepted grants, then for each host reads
// the X25519 pubkey, seals the token, and sends via NATS request/reply.
func deliverToAcceptedHosts(
	nc NATSConn,
	uslug, serverURL, jwt, token string,
	httpClient *http.Client,
	out io.Writer,
) ([]HostDeliveryResult, error) {
	acceptedHosts, err := fetchAcceptedGrants(serverURL, uslug, jwt, httpClient)
	if err != nil {
		return nil, fmt.Errorf("fetch accepted grants: %w", err)
	}

	if len(acceptedHosts) == 0 {
		fmt.Fprintln(out, "No accepted hosts to deliver to.")
		return nil, nil
	}

	var results []HostDeliveryResult
	for _, hslug := range acceptedHosts {
		pubkey, err := fetchUserAgentPubkey(serverURL, uslug, hslug, jwt, httpClient)
		if err != nil {
			fmt.Fprintf(out, "  %s: error reading pubkey: %v\n", hslug, err)
			results = append(results, HostDeliveryResult{HostSlug: hslug, Delivered: false, Err: err})
			continue
		}
		if pubkey == "" {
			fmt.Fprintf(out, "  %s: user-agent pubkey not registered — skipping delivery\n", hslug)
			results = append(results, HostDeliveryResult{
				HostSlug:  hslug,
				Delivered: false,
				Err:       fmt.Errorf("pubkey not registered"),
			})
			continue
		}

		if err := DeliverTokenToHost(nc, uslug, hslug, token, pubkey); err != nil {
			fmt.Fprintf(out, "  %s: delivery failed: %v\n", hslug, err)
			results = append(results, HostDeliveryResult{HostSlug: hslug, Delivered: false, Err: err})
		} else {
			fmt.Fprintf(out, "  %s: delivered\n", hslug)
			results = append(results, HostDeliveryResult{HostSlug: hslug, Delivered: true})
		}
	}
	return results, nil
}

// --------------------------------------------------------------------------
// injectedNATSConnWrapper — test helper
// --------------------------------------------------------------------------

// injectedNATSConnWrapper wraps an injected NATSConn to implement anthropicNATSConn.
// getJSKVAccessor() is delegated to a mock if the underlying conn implements
// AnthropicTestNATSConn; otherwise returns an error.
type injectedNATSConnWrapper struct {
	conn NATSConn
}

func (w *injectedNATSConnWrapper) Request(subj string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	return w.conn.Request(subj, data, timeout)
}

func (w *injectedNATSConnWrapper) Close() {
	w.conn.Close()
}

func (w *injectedNATSConnWrapper) getJSKVAccessor() (KVAccessor, error) {
	if kva, ok := w.conn.(AnthropicTestNATSConn); ok {
		return kva.MockKVAccessor(), nil
	}
	return nil, fmt.Errorf("injected NATSConn does not implement AnthropicTestNATSConn — use a mock that embeds mockAnthropicNATSConn")
}

// AnthropicTestNATSConn is the interface that test NATSConn implementations must
// satisfy to provide a mock KVAccessor for RunAuthAnthropic.
// Exported so tests in the cmd_test package can implement it.
type AnthropicTestNATSConn interface {
	NATSConn
	MockKVAccessor() KVAccessor
}

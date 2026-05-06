package cmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"golang.org/x/crypto/nacl/box"
	"mclaude-cli/cmd"
)

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

// mockNATSConn implements cmd.NATSConn for testing without a real NATS server.
// It captures request calls and returns pre-configured responses.
type mockNATSConn struct {
	requests  []mockRequest
	responses map[string][]byte // subject → response payload
	closed    bool
}

type mockRequest struct {
	Subject string
	Data    []byte
}

func newMockNATSConn() *mockNATSConn {
	return &mockNATSConn{
		responses: make(map[string][]byte),
	}
}

func (m *mockNATSConn) Request(subj string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	m.requests = append(m.requests, mockRequest{Subject: subj, Data: data})
	if resp, ok := m.responses[subj]; ok {
		return &nats.Msg{Data: resp}, nil
	}
	// Default: success response.
	return &nats.Msg{Data: []byte(`{"ok":true}`)}, nil
}

func (m *mockNATSConn) Close() {
	m.closed = true
}

// mockKVAccessor implements cmd.KVAccessor for testing.
type mockKVAccessor struct {
	kvBuckets map[string]*mockKV
}

type mockKV struct {
	entries map[string][]byte
	deleted map[string]bool
}

func newMockKVAccessor() *mockKVAccessor {
	return &mockKVAccessor{kvBuckets: make(map[string]*mockKV)}
}

func (m *mockKVAccessor) KeyValue(bucket string) (cmd.KVBucket, error) {
	kv, ok := m.kvBuckets[bucket]
	if !ok {
		kv = &mockKV{
			entries: make(map[string][]byte),
			deleted: make(map[string]bool),
		}
		m.kvBuckets[bucket] = kv
	}
	return kv, nil
}

// createBucket creates a named KV bucket (for test setup).
func (m *mockKVAccessor) createBucket(name string) *mockKV {
	kv := &mockKV{
		entries: make(map[string][]byte),
		deleted: make(map[string]bool),
	}
	m.kvBuckets[name] = kv
	return kv
}

// mockKVEntry implements cmd.KVEntry.
type mockKVEntry struct {
	value []byte
}

func (e *mockKVEntry) Value() []byte { return e.value }

// mockKV implements cmd.KVBucket.
func (kv *mockKV) Get(key string) (cmd.KVEntry, error) {
	if kv.deleted[key] {
		return nil, nats.ErrKeyNotFound
	}
	v, ok := kv.entries[key]
	if !ok {
		return nil, nats.ErrKeyNotFound
	}
	return &mockKVEntry{value: v}, nil
}

func (kv *mockKV) Put(key string, value []byte) (uint64, error) {
	kv.entries[key] = value
	delete(kv.deleted, key)
	return 1, nil
}

func (kv *mockKV) Delete(key string, opts ...nats.DeleteOpt) error {
	kv.deleted[key] = true
	return nil
}

// mockAnthropicNATSConn implements cmd.AnthropicTestNATSConn (NATSConn + MockKVAccessor).
type mockAnthropicNATSConn struct {
	*mockNATSConn
	kva *mockKVAccessor
}

func newMockAnthropicNATSConn() *mockAnthropicNATSConn {
	return &mockAnthropicNATSConn{
		mockNATSConn: newMockNATSConn(),
		kva:          newMockKVAccessor(),
	}
}

func (m *mockAnthropicNATSConn) MockKVAccessor() cmd.KVAccessor {
	return m.kva
}

// --------------------------------------------------------------------------
// PKCE generation tests
// --------------------------------------------------------------------------

func TestGeneratePKCE_VerifierLength(t *testing.T) {
	pkce, err := cmd.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	// code_verifier: 32 bytes → 43 chars base64url (no padding).
	if len(pkce.Verifier) == 0 {
		t.Error("Verifier is empty")
	}
	if pkce.Challenge == "" {
		t.Error("Challenge is empty")
	}
	if pkce.State == "" {
		t.Error("State is empty")
	}
}

func TestGeneratePKCE_ChallengeIsS256(t *testing.T) {
	pkce, err := cmd.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	// Verify that challenge == base64url(SHA256(verifier)).
	expected := cmd.ComputeS256Challenge(pkce.Verifier)
	if pkce.Challenge != expected {
		t.Errorf("challenge = %q; want %q", pkce.Challenge, expected)
	}
}

func TestGeneratePKCE_UniqueEachCall(t *testing.T) {
	pkce1, err := cmd.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	pkce2, err := cmd.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	if pkce1.Verifier == pkce2.Verifier {
		t.Error("verifiers should be unique across calls")
	}
	if pkce1.State == pkce2.State {
		t.Error("states should be unique across calls")
	}
}

// --------------------------------------------------------------------------
// Loopback listener tests
// --------------------------------------------------------------------------

func TestStartLoopbackListener_BindsPort(t *testing.T) {
	ln, redirectURI, err := cmd.StartLoopbackListener(0)
	if err != nil {
		t.Fatalf("StartLoopbackListener: %v", err)
	}
	defer ln.Close()

	if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") {
		t.Errorf("redirectURI = %q; want http://127.0.0.1:<port>/callback", redirectURI)
	}
	if !strings.HasSuffix(redirectURI, "/callback") {
		t.Errorf("redirectURI = %q; want suffix /callback", redirectURI)
	}
}

func TestWaitForCallback_ReceivesCode(t *testing.T) {
	ln, redirectURI, err := cmd.StartLoopbackListener(0)
	if err != nil {
		t.Fatalf("StartLoopbackListener: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resultCh := make(chan cmd.LoopbackCallbackResult, 1)
	go func() {
		r, err := cmd.WaitForCallback(ctx, ln)
		if err != nil {
			resultCh <- cmd.LoopbackCallbackResult{ErrDesc: err.Error()}
			return
		}
		resultCh <- r
	}()

	// Simulate OAuth redirect.
	callbackURL := redirectURI + "?code=test-auth-code&state=test-state"
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	select {
	case result := <-resultCh:
		if result.Code != "test-auth-code" {
			t.Errorf("Code = %q; want test-auth-code", result.Code)
		}
		if result.State != "test-state" {
			t.Errorf("State = %q; want test-state", result.State)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for callback result")
	}
}

func TestWaitForCallback_ErrorResponse(t *testing.T) {
	ln, redirectURI, err := cmd.StartLoopbackListener(0)
	if err != nil {
		t.Fatalf("StartLoopbackListener: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resultCh := make(chan cmd.LoopbackCallbackResult, 1)
	go func() {
		r, err := cmd.WaitForCallback(ctx, ln)
		if err != nil {
			resultCh <- cmd.LoopbackCallbackResult{ErrDesc: err.Error()}
			return
		}
		resultCh <- r
	}()

	// Simulate OAuth error redirect.
	callbackURL := redirectURI + "?error=access_denied&error_description=User+denied+access"
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("GET callback error: %v", err)
	}
	resp.Body.Close()

	select {
	case result := <-resultCh:
		if result.ErrDesc == "" {
			t.Error("ErrDesc should be set on error callback")
		}
		if result.Code != "" {
			t.Errorf("Code should be empty on error callback, got %q", result.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for error callback result")
	}
}

// --------------------------------------------------------------------------
// Token exchange tests (mocked HTTP server)
// --------------------------------------------------------------------------

func TestExchangeCodeForToken_Success(t *testing.T) {
	// Mock the token endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q; want authorization_code", r.Form.Get("grant_type"))
		}
		if r.Form.Get("code") != "test-code" {
			t.Errorf("code = %q; want test-code", r.Form.Get("code"))
		}
		if r.Form.Get("code_verifier") != "test-verifier" {
			t.Errorf("code_verifier = %q; want test-verifier", r.Form.Get("code_verifier"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"sk-ant-abc123","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()

	cfg := cmd.AnthropicOAuthConfig{
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		ClientID:     "test-client",
	}

	token, err := cmd.ExchangeCodeForToken(cfg, "test-code", "http://127.0.0.1:9999/callback", "test-verifier", srv.Client())
	if err != nil {
		t.Fatalf("ExchangeCodeForToken: %v", err)
	}
	if token != "sk-ant-abc123" {
		t.Errorf("token = %q; want sk-ant-abc123", token)
	}
}

func TestExchangeCodeForToken_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"code expired"}`)
	}))
	defer srv.Close()

	cfg := cmd.AnthropicOAuthConfig{
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		ClientID:     "test-client",
	}

	_, err := cmd.ExchangeCodeForToken(cfg, "expired-code", "http://127.0.0.1:9999/callback", "verifier", srv.Client())
	if err == nil {
		t.Fatal("ExchangeCodeForToken: expected error for error response, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error %q; want mention of 'invalid_grant'", err.Error())
	}
}

func TestExchangeCodeForToken_EmptyAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token_type":"Bearer"}`)
	}))
	defer srv.Close()

	cfg := cmd.AnthropicOAuthConfig{
		AuthorizeURL: srv.URL + "/authorize",
		TokenURL:     srv.URL + "/token",
		ClientID:     "test-client",
	}

	_, err := cmd.ExchangeCodeForToken(cfg, "code", "http://127.0.0.1:9999/callback", "verifier", srv.Client())
	if err == nil {
		t.Fatal("expected error for empty access_token")
	}
}

// --------------------------------------------------------------------------
// Build authorization URL test
// --------------------------------------------------------------------------

func TestBuildAuthURL_ContainsRequiredParams(t *testing.T) {
	cfg := cmd.AnthropicOAuthConfig{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		ClientID:     "mclaude-cli",
	}
	pkce := cmd.PKCEParams{
		Verifier:  "test-verifier",
		Challenge: "test-challenge",
		State:     "test-state",
	}
	redirectURI := "http://127.0.0.1:12345/callback"

	authURL := cmd.BuildAuthURL(cfg, pkce, redirectURI)

	for _, want := range []string{
		"response_type=code",
		"client_id=mclaude-cli",
		"code_challenge=test-challenge",
		"code_challenge_method=S256",
		"state=test-state",
	} {
		if !strings.Contains(authURL, want) {
			t.Errorf("auth URL missing %q; got %s", want, authURL)
		}
	}
}

// --------------------------------------------------------------------------
// Sealed-box wrap tests
// --------------------------------------------------------------------------

func TestSealToken_ProducesBase64Ciphertext(t *testing.T) {
	// Generate a real X25519 keypair.
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])
	token := "sk-ant-test-token-12345"

	sealed, err := cmd.SealToken(token, pubBase64)
	if err != nil {
		t.Fatalf("SealToken: %v", err)
	}
	if sealed == "" {
		t.Fatal("SealToken returned empty string")
	}

	// Decode and verify the sealed box can be opened with the private key.
	ciphertext, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode sealed ciphertext: %v", err)
	}

	plaintext, ok := box.OpenAnonymous(nil, ciphertext, pub, priv)
	if !ok {
		t.Fatal("OpenAnonymous failed: sealed token could not be decrypted with the X25519 private key")
	}
	if string(plaintext) != token {
		t.Errorf("decrypted = %q; want %q", string(plaintext), token)
	}
}

func TestSealToken_InvalidPubkey(t *testing.T) {
	_, err := cmd.SealToken("token", "not-valid-base64!!!")
	if err == nil {
		t.Fatal("SealToken: expected error for invalid pubkey base64, got nil")
	}
}

func TestSealToken_WrongLength(t *testing.T) {
	// 16 bytes base64-encoded — wrong length (need 32).
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := cmd.SealToken("token", shortKey)
	if err == nil {
		t.Fatal("SealToken: expected error for wrong-length pubkey, got nil")
	}
}

func TestSealToken_UniqueEachCall(t *testing.T) {
	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])
	token := "same-token"

	sealed1, err := cmd.SealToken(token, pubBase64)
	if err != nil {
		t.Fatalf("SealToken (1): %v", err)
	}
	sealed2, err := cmd.SealToken(token, pubBase64)
	if err != nil {
		t.Fatalf("SealToken (2): %v", err)
	}
	// SealAnonymous is non-deterministic (uses ephemeral keypair internally).
	if sealed1 == sealed2 {
		t.Error("SealToken: two calls with same inputs should produce different ciphertexts (ephemeral key)")
	}
}

// --------------------------------------------------------------------------
// Per-host delivery tests (mocked NATS + HTTP)
// --------------------------------------------------------------------------

func TestDeliverTokenToHost_SendsToCorrectSubject(t *testing.T) {
	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])

	nc := newMockNATSConn()
	nc.responses["mclaude.users.alice.hosts.dev-local.agent.token-delivery"] = []byte(`{"ok":true}`)

	if err := cmd.DeliverTokenToHost(nc, "alice", "dev-local", "my-token", pubBase64); err != nil {
		t.Fatalf("DeliverTokenToHost: %v", err)
	}

	if len(nc.requests) != 1 {
		t.Fatalf("expected 1 NATS request, got %d", len(nc.requests))
	}
	req := nc.requests[0]
	if req.Subject != "mclaude.users.alice.hosts.dev-local.agent.token-delivery" {
		t.Errorf("subject = %q; want mclaude.users.alice.hosts.dev-local.agent.token-delivery", req.Subject)
	}

	// Verify payload structure.
	var payload map[string]string
	if err := json.Unmarshal(req.Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["provider"] != "anthropic" {
		t.Errorf("provider = %q; want anthropic", payload["provider"])
	}
	if payload["token_wrapped"] == "" {
		t.Error("token_wrapped is empty")
	}
	// Verify token_wrapped is base64.
	if _, err := base64.StdEncoding.DecodeString(payload["token_wrapped"]); err != nil {
		t.Errorf("token_wrapped is not valid base64: %v", err)
	}
}

func TestDeliverTokenToHost_TokenWrappedDecryptable(t *testing.T) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])

	nc := newMockNATSConn()
	nc.responses["mclaude.users.alice.hosts.dev-local.agent.token-delivery"] = []byte(`{"ok":true}`)

	originalToken := "sk-ant-super-secret-token"
	if err := cmd.DeliverTokenToHost(nc, "alice", "dev-local", originalToken, pubBase64); err != nil {
		t.Fatalf("DeliverTokenToHost: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(nc.requests[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload["token_wrapped"])
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, pub, priv)
	if !ok {
		t.Fatal("OpenAnonymous failed")
	}
	if string(plaintext) != originalToken {
		t.Errorf("decrypted = %q; want %q", string(plaintext), originalToken)
	}
}

func TestDeliverTokenToHost_AgentError(t *testing.T) {
	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])

	nc := newMockNATSConn()
	subject := "mclaude.users.alice.hosts.bad-host.agent.token-delivery"
	nc.responses[subject] = []byte(`{"error":"host not ready"}`)

	err = cmd.DeliverTokenToHost(nc, "alice", "bad-host", "token", pubBase64)
	if err == nil {
		t.Fatal("expected error when agent returns error, got nil")
	}
	if !strings.Contains(err.Error(), "host not ready") {
		t.Errorf("error %q; want mention of 'host not ready'", err.Error())
	}
}

// --------------------------------------------------------------------------
// RunAuthAnthropic --token tests (with mock NATS + HTTP server)
// --------------------------------------------------------------------------

// mockCPServer creates a test HTTP server simulating the control-plane endpoints
// for grants and pubkey.
func mockCPServer(t *testing.T, grants []string, pubkeys map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /api/users/{uslug}/grants
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/grants") &&
			!strings.Contains(r.URL.Path, "/grant/accept") {
			var entries []map[string]string
			for _, h := range grants {
				entries = append(entries, map[string]string{"hostSlug": h, "status": "accepted"})
			}
			resp := map[string]interface{}{"grants": entries}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		// GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/user-agent/pubkey") {
			// Extract hslug from path: /api/users/{u}/hosts/{h}/user-agent/pubkey
			parts := strings.Split(r.URL.Path, "/")
			var hslug string
			for i, p := range parts {
				if p == "hosts" && i+1 < len(parts) {
					hslug = parts[i+1]
					break
				}
			}
			if pub, ok := pubkeys[hslug]; ok {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"x25519Pub": pub})
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestRunAuthAnthropic_ManualToken_NoHosts(t *testing.T) {
	dir := t.TempDir()

	// Setup auth.json.
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	// Mock CP: no accepted grants.
	cpSrv := mockCPServer(t, nil, nil)
	defer cpSrv.Close()

	// Mock NATS conn with JetStream.
	mockConn := newMockAnthropicNATSConn()
	// Pre-create the bucket.
	mockConn.kva.createBucket("mclaude-user-info-alice")

	var out bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Token:     "sk-ant-manual-token",
		AuthPath:  authPath,
		ServerURL: cpSrv.URL,
		NATSConn:  mockConn,
		HTTPClient: cpSrv.Client(),
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want %d (ExitAuthSuccess)", result.ExitCode, cmd.ExitAuthSuccess)
	}

	// Verify token was written to KV.
	kv, err := mockConn.kva.KeyValue("mclaude-user-info-alice")
	if err != nil {
		t.Fatalf("KeyValue: %v", err)
	}
	entry, err := kv.Get("tokens.anthropic")
	if err != nil {
		t.Fatalf("KV get tokens.anthropic: %v", err)
	}
	if string(entry.Value()) != "sk-ant-manual-token" {
		t.Errorf("KV value = %q; want sk-ant-manual-token", entry.Value())
	}
}

func TestRunAuthAnthropic_ManualToken_DeliversToHost(t *testing.T) {
	dir := t.TempDir()

	// Generate X25519 keypair for the mock user-agent.
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])

	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	// CP returns one accepted host with a pubkey.
	cpSrv := mockCPServer(t, []string{"dev-local"}, map[string]string{"dev-local": pubBase64})
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	// Token delivery succeeds.
	subject := "mclaude.users.alice.hosts.dev-local.agent.token-delivery"
	mockConn.responses[subject] = []byte(`{"ok":true}`)

	var out bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Token:      "sk-ant-real-token",
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want 0; output: %s", result.ExitCode, out.String())
	}
	if len(result.HostResults) != 1 {
		t.Fatalf("expected 1 host result, got %d", len(result.HostResults))
	}
	if !result.HostResults[0].Delivered {
		t.Errorf("host dev-local: Delivered = false; want true")
	}

	// Verify NATS request was made with correct payload.
	if len(mockConn.requests) != 1 {
		t.Fatalf("expected 1 NATS request, got %d", len(mockConn.requests))
	}
	if mockConn.requests[0].Subject != subject {
		t.Errorf("NATS subject = %q; want %q", mockConn.requests[0].Subject, subject)
	}

	// Verify the sealed token can be decrypted.
	var payload map[string]string
	if err := json.Unmarshal(mockConn.requests[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal delivery payload: %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload["token_wrapped"])
	if err != nil {
		t.Fatalf("decode token_wrapped: %v", err)
	}
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, pub, priv)
	if !ok {
		t.Fatal("OpenAnonymous: decryption failed")
	}
	if string(plaintext) != "sk-ant-real-token" {
		t.Errorf("decrypted token = %q; want sk-ant-real-token", plaintext)
	}
}

func TestRunAuthAnthropic_ManualToken_PartialDelivery(t *testing.T) {
	dir := t.TempDir()

	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])

	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	// CP returns two accepted hosts.
	cpSrv := mockCPServer(t, []string{"host-ok", "host-offline"}, map[string]string{
		"host-ok":      pubBase64,
		"host-offline": pubBase64,
	})
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	// host-ok succeeds; host-offline fails (no response configured → default success).
	// Use a custom NATSConn that fails for the offline host.
	failConn := &failingHostNATSConn{
		mockAnthropicNATSConn: mockConn,
		failSubject:           "mclaude.users.alice.hosts.host-offline.agent.token-delivery",
	}

	var out bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Token:      "sk-ant-token",
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   failConn,
		HTTPClient: cpSrv.Client(),
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthPartialDelivery {
		t.Errorf("ExitCode = %d; want %d (ExitAuthPartialDelivery)", result.ExitCode, cmd.ExitAuthPartialDelivery)
	}
}

// failingHostNATSConn returns an error for a specific subject.
type failingHostNATSConn struct {
	*mockAnthropicNATSConn
	failSubject string
}

func (f *failingHostNATSConn) Request(subj string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	if subj == f.failSubject {
		return nil, fmt.Errorf("connection refused (simulated offline host)")
	}
	return f.mockAnthropicNATSConn.Request(subj, data, timeout)
}

// --------------------------------------------------------------------------
// --status tests
// --------------------------------------------------------------------------

func TestRunAuthAnthropic_Status_NotSet(t *testing.T) {
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	cpSrv := mockCPServer(t, nil, nil)
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	// Bucket exists but no token.
	mockConn.kva.createBucket("mclaude-user-info-alice")

	var out bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Status:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --status: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthNoToken {
		t.Errorf("ExitCode = %d; want %d (ExitAuthNoToken)", result.ExitCode, cmd.ExitAuthNoToken)
	}
	if !strings.Contains(out.String(), "Not set") {
		t.Errorf("output %q; want mention of 'Not set'", out.String())
	}
}

func TestRunAuthAnthropic_Status_Connected(t *testing.T) {
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBase64 := base64.StdEncoding.EncodeToString(pub[:])

	cpSrv := mockCPServer(t, []string{"dev-local"}, map[string]string{"dev-local": pubBase64})
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	// Pre-populate token.
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("existing-token"))

	var out bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Status:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --status: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want %d (ExitAuthSuccess)", result.ExitCode, cmd.ExitAuthSuccess)
	}
	if !strings.Contains(out.String(), "Connected") {
		t.Errorf("output %q; want mention of 'Connected'", out.String())
	}
	if !strings.Contains(out.String(), "dev-local") {
		t.Errorf("output %q; want mention of host 'dev-local'", out.String())
	}
	if !strings.Contains(out.String(), "registered") {
		t.Errorf("output %q; want mention of 'registered' pubkey status", out.String())
	}
}

func TestRunAuthAnthropic_Status_HostNoPubkey(t *testing.T) {
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	// CP returns host with no pubkey.
	cpSrv := mockCPServer(t, []string{"not-started-host"}, map[string]string{})
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("my-token"))

	var out bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Status:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --status: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want %d", result.ExitCode, cmd.ExitAuthSuccess)
	}
	if !strings.Contains(out.String(), "not registered") {
		t.Errorf("output %q; want mention of 'not registered'", out.String())
	}
}

// --------------------------------------------------------------------------
// --revoke tests
// --------------------------------------------------------------------------

func TestRunAuthAnthropic_Revoke_NoHosts(t *testing.T) {
	// --revoke with no accepted hosts: KV deleted, summary "Token removed."
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	cpSrv := mockCPServer(t, nil, nil)
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("to-be-revoked"))

	var out, errOut bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Revoke:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
		Stderr:     &errOut,
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --revoke: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want 0", result.ExitCode)
	}

	// Token should be deleted from KV.
	_, err = kv.Get("tokens.anthropic")
	if err != nats.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after revoke, got: %v", err)
	}

	// No NATS requests should be sent (no hosts to revoke from).
	if len(mockConn.requests) != 0 {
		t.Errorf("expected 0 NATS requests with no accepted hosts, got %d", len(mockConn.requests))
	}

	if !strings.Contains(out.String(), "Token removed") {
		t.Errorf("stdout %q; want mention of 'Token removed'", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("expected empty stderr with no hosts, got %q", errOut.String())
	}
}

func TestRunAuthAnthropic_Revoke_AllHostsSucceed(t *testing.T) {
	// --revoke with 2 accepted hosts that both succeed.
	// Summary: "Token removed. Revoked on 2/2 hosts."
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	cpSrv := mockCPServer(t, []string{"host-a", "host-b"}, nil)
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("my-token"))

	// Pre-configure successful responses for both token-revoke subjects.
	mockConn.responses["mclaude.users.alice.hosts.host-a.agent.token-revoke"] = []byte(`{"ok":true}`)
	mockConn.responses["mclaude.users.alice.hosts.host-b.agent.token-revoke"] = []byte(`{"ok":true}`)

	var out, errOut bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Revoke:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
		Stderr:     &errOut,
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --revoke: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want 0 (ExitAuthSuccess)", result.ExitCode)
	}

	// Token should be deleted from KV.
	_, err = kv.Get("tokens.anthropic")
	if err != nats.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after revoke, got: %v", err)
	}

	// Both hosts should have received a token-revoke NATS request.
	if len(mockConn.requests) != 2 {
		t.Fatalf("expected 2 NATS requests, got %d", len(mockConn.requests))
	}
	subjects := make(map[string]bool)
	for _, req := range mockConn.requests {
		subjects[req.Subject] = true
		// Verify request payload has provider=anthropic.
		var payload map[string]string
		if err := json.Unmarshal(req.Data, &payload); err != nil {
			t.Errorf("unmarshal revoke payload: %v", err)
		}
		if payload["provider"] != "anthropic" {
			t.Errorf("revoke payload provider = %q; want anthropic", payload["provider"])
		}
	}
	for _, hslug := range []string{"host-a", "host-b"} {
		subj := fmt.Sprintf("mclaude.users.alice.hosts.%s.agent.token-revoke", hslug)
		if !subjects[subj] {
			t.Errorf("expected NATS request to %s, not found", subj)
		}
	}

	stdout := out.String()
	if !strings.Contains(stdout, "2/2") {
		t.Errorf("stdout %q; want '2/2'", stdout)
	}
	if !strings.Contains(stdout, "Token removed") {
		t.Errorf("stdout %q; want 'Token removed'", stdout)
	}
	// No warnings on stderr when all succeed.
	if errOut.Len() != 0 {
		t.Errorf("expected empty stderr when all hosts succeed, got %q", errOut.String())
	}
}

func TestRunAuthAnthropic_Revoke_PartialFailure(t *testing.T) {
	// --revoke with 2 accepted hosts; one fails.
	// Summary: "Token removed. Revoked on 1/2 hosts."
	// Exit code: still ExitAuthSuccess (KV delete succeeded).
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	cpSrv := mockCPServer(t, []string{"host-ok", "host-offline"}, nil)
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("my-token"))

	// host-ok succeeds; host-offline fails via failingHostNATSConn.
	mockConn.responses["mclaude.users.alice.hosts.host-ok.agent.token-revoke"] = []byte(`{"ok":true}`)
	failConn := &failingHostNATSConn{
		mockAnthropicNATSConn: mockConn,
		failSubject:           "mclaude.users.alice.hosts.host-offline.agent.token-revoke",
	}

	var out, errOut bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Revoke:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   failConn,
		HTTPClient: cpSrv.Client(),
		Stderr:     &errOut,
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --revoke: %v", err)
	}
	// Exit code must be 0 (ExitAuthSuccess) even with partial failure — KV delete succeeded.
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want 0 (ExitAuthSuccess) even on partial host failure", result.ExitCode)
	}

	// Token deleted from KV.
	_, err = kv.Get("tokens.anthropic")
	if err != nats.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after revoke, got: %v", err)
	}

	// Warning for failed host goes to stderr.
	stderrOutput := errOut.String()
	if !strings.Contains(stderrOutput, "Warning: could not revoke on host-offline") {
		t.Errorf("stderr %q; want warning for host-offline", stderrOutput)
	}
	// Summary (1/2) goes to stdout.
	stdoutOutput := out.String()
	if !strings.Contains(stdoutOutput, "1/2") {
		t.Errorf("stdout %q; want '1/2'", stdoutOutput)
	}
}

func TestRunAuthAnthropic_Revoke_AllHostsFail(t *testing.T) {
	// --revoke with 1 accepted host that fails.
	// Summary: "Token removed from account. Warning: 1 hosts could not be reached..."
	// Exit code: still ExitAuthSuccess (KV delete succeeded).
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	cpSrv := mockCPServer(t, []string{"unreachable-host"}, nil)
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("my-token"))

	failConn := &failingHostNATSConn{
		mockAnthropicNATSConn: mockConn,
		failSubject:           "mclaude.users.alice.hosts.unreachable-host.agent.token-revoke",
	}

	var out, errOut bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Revoke:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   failConn,
		HTTPClient: cpSrv.Client(),
		Stderr:     &errOut,
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --revoke: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want 0 (ExitAuthSuccess) when KV delete succeeded", result.ExitCode)
	}

	// Token deleted from KV.
	_, err = kv.Get("tokens.anthropic")
	if err != nats.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after revoke, got: %v", err)
	}

	// Warning for failing host goes to stderr.
	stderrOutput := errOut.String()
	if !strings.Contains(stderrOutput, "Warning: could not revoke on unreachable-host") {
		t.Errorf("stderr %q; want warning for unreachable-host", stderrOutput)
	}

	// All-failed summary message goes to stdout.
	stdoutOutput := out.String()
	if !strings.Contains(stdoutOutput, "could not be reached") {
		t.Errorf("stdout %q; want 'could not be reached'", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Token removed from account") {
		t.Errorf("stdout %q; want 'Token removed from account'", stdoutOutput)
	}
}

func TestRunAuthAnthropic_Revoke_AgentErrorResponse(t *testing.T) {
	// --revoke where host agent returns { "ok": false, "error": "..." }
	// Counts as a failure for that host; warning printed to stderr; exit code still 0.
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	creds := &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUANQQXHQ3Q4JS5O6BLQW2QIQIHDKFWQB4CJEWFZK2HBYJ76CCWMRGRQY",
		UserSlug: "alice",
	}
	if err := cmd.SaveAuth(authPath, creds); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	cpSrv := mockCPServer(t, []string{"error-host"}, nil)
	defer cpSrv.Close()

	mockConn := newMockAnthropicNATSConn()
	mockConn.kva.createBucket("mclaude-user-info-alice")
	kv, _ := mockConn.kva.KeyValue("mclaude-user-info-alice")
	_, _ = kv.Put("tokens.anthropic", []byte("my-token"))

	// Agent returns an application-level error.
	mockConn.responses["mclaude.users.alice.hosts.error-host.agent.token-revoke"] = []byte(`{"ok":false,"error":"permission denied"}`)

	var out, errOut bytes.Buffer
	flags := cmd.AuthAnthropicFlags{
		Revoke:     true,
		AuthPath:   authPath,
		ServerURL:  cpSrv.URL,
		NATSConn:   mockConn,
		HTTPClient: cpSrv.Client(),
		Stderr:     &errOut,
	}

	result, err := cmd.RunAuthAnthropic(flags, &out)
	if err != nil {
		t.Fatalf("RunAuthAnthropic --revoke: %v", err)
	}
	if result.ExitCode != cmd.ExitAuthSuccess {
		t.Errorf("ExitCode = %d; want 0 (ExitAuthSuccess)", result.ExitCode)
	}

	// Token deleted from KV despite host error.
	_, err = kv.Get("tokens.anthropic")
	if err != nats.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after revoke, got: %v", err)
	}

	// Warning goes to stderr.
	stderrOutput := errOut.String()
	if !strings.Contains(stderrOutput, "Warning: could not revoke on error-host") {
		t.Errorf("stderr %q; want warning for error-host", stderrOutput)
	}
}

func TestSendTokenRevoke_SendsToCorrectSubject(t *testing.T) {
	nc := newMockNATSConn()
	nc.responses["mclaude.users.alice.hosts.dev-local.agent.token-revoke"] = []byte(`{"ok":true}`)

	if err := cmd.SendTokenRevoke(nc, "alice", "dev-local"); err != nil {
		t.Fatalf("SendTokenRevoke: %v", err)
	}

	if len(nc.requests) != 1 {
		t.Fatalf("expected 1 NATS request, got %d", len(nc.requests))
	}
	req := nc.requests[0]
	if req.Subject != "mclaude.users.alice.hosts.dev-local.agent.token-revoke" {
		t.Errorf("subject = %q; want mclaude.users.alice.hosts.dev-local.agent.token-revoke", req.Subject)
	}

	var payload map[string]string
	if err := json.Unmarshal(req.Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["provider"] != "anthropic" {
		t.Errorf("provider = %q; want anthropic", payload["provider"])
	}
}

func TestSendTokenRevoke_AgentError(t *testing.T) {
	nc := newMockNATSConn()
	nc.responses["mclaude.users.alice.hosts.bad-host.agent.token-revoke"] = []byte(`{"ok":false,"error":"not ready"}`)

	err := cmd.SendTokenRevoke(nc, "alice", "bad-host")
	if err == nil {
		t.Fatal("expected error when agent returns ok=false, got nil")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("error %q; want mention of 'not ready'", err.Error())
	}
}

// --------------------------------------------------------------------------
// Exit code tests
// --------------------------------------------------------------------------

func TestRunAuthAnthropic_NotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	// No auth.json file.

	flags := cmd.AuthAnthropicFlags{
		Token:    "sk-ant-token",
		AuthPath: dir + "/auth.json",
	}

	result, err := cmd.RunAuthAnthropic(flags, bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("expected error when not logged in, got nil")
	}
	if result == nil {
		t.Fatal("result should not be nil even on error")
	}
	if result.ExitCode != cmd.ExitAuthNoToken {
		t.Errorf("ExitCode = %d; want %d (ExitAuthNoToken)", result.ExitCode, cmd.ExitAuthNoToken)
	}
}

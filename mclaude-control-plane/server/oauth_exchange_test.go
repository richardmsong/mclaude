// oauth_exchange_test.go — unit tests for POST /api/auth/oauth/exchange (ADR-0092).
package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	cpauth "mclaude-control-plane/auth"
)

// fakeHTTPDoer is a test double for oauthExchangeHTTPClient.
type fakeHTTPDoer struct {
	resp *http.Response
	err  error
}

func (f *fakeHTTPDoer) Do(_ *http.Request) (*http.Response, error) {
	return f.resp, f.err
}

// fakeHTTPResponse builds a minimal *http.Response with a JSON body.
func fakeHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Status:     http.StatusText(status),
	}
}

// authHeader returns a valid Bearer token for the given server.
func authHeader(t *testing.T, srv *Server) string {
	t.Helper()
	accountKP := srv.accountKP
	userKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("CreateUser nkey: %v", err)
	}
	userPub, _ := userKP.PublicKey()
	jwtStr, err := cpauth.IssueUserJWT(userPub, "unit-test-user", "unit-test-slug", nil, accountKP, int64(time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}
	return "Bearer " + jwtStr
}

// newOAuthTestServer creates a Server with a real account key pair (no DB/NATS needed).
func newOAuthTestServer(t *testing.T) *Server {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return NewServer(nil, kp, "nats://localhost:4222", "", time.Hour)
}

// TestHandleOAuthExchange_Success verifies the happy path: upstream returns 200 with
// access_token, CP returns 200 { "access_token": "..." }.
func TestHandleOAuthExchange_Success(t *testing.T) {
	srv := newOAuthTestServer(t)

	// Install fake upstream.
	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = &fakeHTTPDoer{
		resp: fakeHTTPResponse(http.StatusOK, `{"access_token":"sk-ant-oat01-test-token","token_type":"Bearer"}`),
	}
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "authcode123",
		CodeVerifier: "verifier456",
		RedirectURI:  "https://mclaude.internal/auth/oauth/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp OAuthExchangeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken != "sk-ant-oat01-test-token" {
		t.Errorf("access_token = %q; want 'sk-ant-oat01-test-token'", resp.AccessToken)
	}
}

// TestHandleOAuthExchange_UpstreamError verifies that when claude.ai returns 400,
// CP returns 400 with { error, error_description }.
func TestHandleOAuthExchange_UpstreamError(t *testing.T) {
	srv := newOAuthTestServer(t)

	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = &fakeHTTPDoer{
		resp: fakeHTTPResponse(http.StatusBadRequest,
			`{"error":"invalid_grant","error_description":"The authorization code is invalid or has expired."}`),
	}
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "expired-code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://mclaude.internal/auth/oauth/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body: %s", rec.Code, rec.Body.String())
	}

	var errResp OAuthExchangeErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Error != "invalid_grant" {
		t.Errorf("error = %q; want 'invalid_grant'", errResp.Error)
	}
	if errResp.ErrorDescription == "" {
		t.Error("error_description should not be empty")
	}
}

// TestHandleOAuthExchange_UpstreamServerError verifies that upstream 5xx maps to 502.
func TestHandleOAuthExchange_UpstreamServerError(t *testing.T) {
	srv := newOAuthTestServer(t)

	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = &fakeHTTPDoer{
		resp: fakeHTTPResponse(http.StatusInternalServerError, `{"error":"server_error"}`),
	}
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://mclaude.internal/auth/oauth/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 for upstream 5xx; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_UpstreamNetworkError verifies that a network-level failure
// (Do() returns error) results in 502.
func TestHandleOAuthExchange_UpstreamNetworkError(t *testing.T) {
	srv := newOAuthTestServer(t)

	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = &fakeHTTPDoer{
		err: io.ErrUnexpectedEOF,
	}
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://mclaude.internal/auth/oauth/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 for network error; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_RequiresAuth verifies that the endpoint rejects requests
// without a valid JWT (auth middleware applies).
func TestHandleOAuthExchange_RequiresAuth(t *testing.T) {
	srv := newOAuthTestServer(t)

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://example.com/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 without auth; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_InvalidToken verifies 401 for an invalid JWT.
func TestHandleOAuthExchange_InvalidToken(t *testing.T) {
	srv := newOAuthTestServer(t)

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://example.com/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 for invalid JWT; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_MissingFields verifies 400 for missing required fields.
func TestHandleOAuthExchange_MissingFields(t *testing.T) {
	srv := newOAuthTestServer(t)

	cases := []struct {
		name string
		body string
	}{
		{"missing code", `{"code_verifier":"v","redirect_uri":"u","client_id":"c"}`},
		{"missing code_verifier", `{"code":"c","redirect_uri":"u","client_id":"ci"}`},
		{"missing redirect_uri", `{"code":"c","code_verifier":"v","client_id":"ci"}`},
		{"missing client_id", `{"code":"c","code_verifier":"v","redirect_uri":"u"}`},
		{"empty body", `{}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange",
				strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(t, srv))

			mux := http.NewServeMux()
			srv.RegisterRoutes(mux)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d; want 400 for %s", rec.Code, tc.name)
			}
		})
	}
}

// TestHandleOAuthExchange_InvalidJSON verifies 400 on malformed JSON body.
func TestHandleOAuthExchange_InvalidJSON(t *testing.T) {
	srv := newOAuthTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 for invalid JSON; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_MethodNotAllowed verifies that GET returns 405.
func TestHandleOAuthExchange_MethodNotAllowed(t *testing.T) {
	srv := newOAuthTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/exchange", nil)
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405 for GET; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_UpstreamRequestFields verifies that CP sends the correct
// form fields to the upstream (grant_type, code, code_verifier, redirect_uri, client_id).
func TestHandleOAuthExchange_UpstreamRequestFields(t *testing.T) {
	srv := newOAuthTestServer(t)

	var capturedReq *http.Request
	capturing := &capturingHTTPDoer{
		inner: &fakeHTTPDoer{
			resp: fakeHTTPResponse(http.StatusOK, `{"access_token":"tok"}`),
		},
		capturedReq: &capturedReq,
	}

	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = capturing
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	exchangeReq := OAuthExchangeRequest{
		Code:         "mycode",
		CodeVerifier: "myverifier",
		RedirectURI:  "https://example.com/callback",
		ClientID:     "mclaude-cli",
	}
	bodyBytes, _ := json.Marshal(exchangeReq)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	if capturedReq == nil {
		t.Fatal("upstream request was not captured")
	}

	// Verify URL.
	if capturedReq.URL.String() != anthropicTokenEndpoint {
		t.Errorf("upstream URL = %q; want %q", capturedReq.URL.String(), anthropicTokenEndpoint)
	}

	// Verify Content-Type.
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q; want application/x-www-form-urlencoded", ct)
	}

	// Verify form fields.
	if err := capturedReq.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	checks := map[string]string{
		"grant_type":    "authorization_code",
		"code":          exchangeReq.Code,
		"code_verifier": exchangeReq.CodeVerifier,
		"redirect_uri":  exchangeReq.RedirectURI,
		"client_id":     exchangeReq.ClientID,
	}
	for field, want := range checks {
		if got := capturedReq.FormValue(field); got != want {
			t.Errorf("upstream form[%q] = %q; want %q", field, got, want)
		}
	}
}

// TestHandleOAuthExchange_DoesNotStoreToken verifies that a successful exchange
// does not write anything to the DB or NATS (CP is pass-through for the token).
// Since server has nil DB and nil NATS, any DB/NATS call would panic.
// The test passes if the handler returns 200 without panicking.
func TestHandleOAuthExchange_DoesNotStoreToken(t *testing.T) {
	srv := newOAuthTestServer(t) // nil DB, nil NATS

	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = &fakeHTTPDoer{
		resp: fakeHTTPResponse(http.StatusOK, `{"access_token":"sk-ant-oat01-passthrough"}`),
	}
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://example.com/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req) // would panic if it touched s.db or s.nc

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleOAuthExchange_UpstreamMissingAccessToken verifies 502 when upstream
// returns 200 but access_token is absent from the response body.
func TestHandleOAuthExchange_UpstreamMissingAccessToken(t *testing.T) {
	srv := newOAuthTestServer(t)

	orig := oauthExchangeHTTPClient
	oauthExchangeHTTPClient = &fakeHTTPDoer{
		resp: fakeHTTPResponse(http.StatusOK, `{"token_type":"Bearer"}`), // missing access_token
	}
	t.Cleanup(func() { oauthExchangeHTTPClient = orig })

	body, _ := json.Marshal(OAuthExchangeRequest{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://example.com/callback",
		ClientID:     "mclaude-cli",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, srv))

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 when access_token missing; body: %s", rec.Code, rec.Body.String())
	}
}

// capturingHTTPDoer wraps another httpDoer and captures the outbound request.
type capturingHTTPDoer struct {
	inner       httpDoer
	capturedReq **http.Request
}

func (c *capturingHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	*c.capturedReq = req
	return c.inner.Do(req)
}

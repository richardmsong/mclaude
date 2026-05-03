package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

// newTestServerWithNatsWsURL creates a Server with a specific natsWsURL for unit tests.
func newTestServerWithNatsWsURL(t *testing.T, natsWsURL string) *Server {
	t.Helper()
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account key: %v", err)
	}
	return NewServer(nil, accountKP, "nats://localhost:4222", natsWsURL, 8*time.Hour, "test-admin-token")
}

// setupAuthorizedDeviceCode inserts a completed (authorized) device code entry into the
// global store and returns the device code. The entry is removed automatically on test cleanup.
func setupAuthorizedDeviceCode(t *testing.T, userSlug, jwt string) string {
	t.Helper()
	deviceCode, err := generateCLIDeviceCode()
	if err != nil {
		t.Fatalf("generateCLIDeviceCode: %v", err)
	}
	globalCLIDeviceCodeStore.mu.Lock()
	globalCLIDeviceCodeStore.codes[deviceCode] = &cliDeviceCodeEntry{
		UserCode:  "TEST-0001",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		Completed: true,
		UserSlug:  userSlug,
		JWT:       jwt,
	}
	globalCLIDeviceCodeStore.mu.Unlock()
	t.Cleanup(func() {
		globalCLIDeviceCodeStore.mu.Lock()
		delete(globalCLIDeviceCodeStore.codes, deviceCode)
		globalCLIDeviceCodeStore.mu.Unlock()
	})
	return deviceCode
}

// pollDeviceCode sends POST /api/auth/device-code/poll and returns the recorder.
func pollDeviceCode(t *testing.T, srv *Server, deviceCode string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"deviceCode":"` + deviceCode + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/poll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleCLIDeviceCodePoll(rec, req)
	return rec
}

// TestCLIDeviceCodePoll_AuthorizedIncludesNatsUrl verifies that when the server has a
// non-empty natsWsURL, the poll response for an authorized device code includes natsUrl.
func TestCLIDeviceCodePoll_AuthorizedIncludesNatsUrl(t *testing.T) {
	const natsWsURL = "wss://dev-nats.example.com"
	srv := newTestServerWithNatsWsURL(t, natsWsURL)
	deviceCode := setupAuthorizedDeviceCode(t, "alice-gmail", "test.jwt.token")

	rec := pollDeviceCode(t, srv, deviceCode)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var resp CLIDeviceCodePollResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Status != "authorized" {
		t.Errorf("status = %q; want authorized", resp.Status)
	}
	if resp.NATSUrl != natsWsURL {
		t.Errorf("natsUrl = %q; want %q", resp.NATSUrl, natsWsURL)
	}
	if resp.UserSlug != "alice-gmail" {
		t.Errorf("userSlug = %q; want alice-gmail", resp.UserSlug)
	}
	if resp.JWT != "test.jwt.token" {
		t.Errorf("jwt = %q; want test.jwt.token", resp.JWT)
	}
}

// TestCLIDeviceCodePoll_AuthorizedNatsUrlEmpty verifies that when natsWsURL is empty,
// the poll response omits the natsUrl key entirely (omitempty behavior).
func TestCLIDeviceCodePoll_AuthorizedNatsUrlEmpty(t *testing.T) {
	srv := newTestServerWithNatsWsURL(t, "") // empty natsWsURL
	deviceCode := setupAuthorizedDeviceCode(t, "bob-gmail", "test.jwt.token2")

	rec := pollDeviceCode(t, srv, deviceCode)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	rawJSON := rec.Body.String()

	// Confirm the struct field is empty.
	var resp CLIDeviceCodePollResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "authorized" {
		t.Errorf("status = %q; want authorized", resp.Status)
	}
	if resp.NATSUrl != "" {
		t.Errorf("natsUrl struct field = %q; want empty", resp.NATSUrl)
	}

	// Confirm the raw JSON does not contain the "natsUrl" key at all (omitempty).
	if strings.Contains(rawJSON, `"natsUrl"`) {
		t.Errorf("response JSON contains natsUrl key but should be omitted when empty: %s", rawJSON)
	}
}

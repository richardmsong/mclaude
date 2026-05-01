package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// cliDeviceCodeEntry stores a pending CLI device-code auth flow.
// Distinct from the host device-code flow in hosts.go.
type cliDeviceCodeEntry struct {
	UserCode     string
	ExpiresAt    time.Time
	Completed    bool
	UserID       string
	UserSlug     string
	JWT          string
}

// cliDeviceCodeStore is an in-memory store for CLI device-code auth flows.
type cliDeviceCodeStore struct {
	mu    sync.RWMutex
	codes map[string]*cliDeviceCodeEntry // key: device code (opaque)
}

var globalCLIDeviceCodeStore = &cliDeviceCodeStore{
	codes: make(map[string]*cliDeviceCodeEntry),
}

// CLIDeviceCodeRequest is the body for POST /api/auth/device-code.
type CLIDeviceCodeRequest struct{}

// CLIDeviceCodeResponse is returned for POST /api/auth/device-code.
type CLIDeviceCodeResponse struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`        // human-readable code for web UI
	VerificationURL string `json:"verificationUrl"` // URL for user to visit
	ExpiresIn       int    `json:"expiresIn"`       // seconds until expiry
	Interval        int    `json:"interval"`        // polling interval in seconds
}

// CLIDeviceCodePollRequest is the body for POST /api/auth/device-code/poll.
type CLIDeviceCodePollRequest struct {
	DeviceCode string `json:"deviceCode"`
}

// CLIDeviceCodePollResponse is returned for POST /api/auth/device-code/poll.
type CLIDeviceCodePollResponse struct {
	Status   string `json:"status"`             // "pending" or "authorized"
	JWT      string `json:"jwt,omitempty"`
	UserSlug string `json:"userSlug,omitempty"`
}

// generateCLIDeviceCode generates an opaque device code (URL-safe random string).
func generateCLIDeviceCode() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// generateUserCode generates a human-readable user code (e.g., ABCD-1234).
func generateUserCode() (string, error) {
	const chars = "BCDFGHJKLMNPQRSTVWXZ23456789" // no ambiguous chars
	result := make([]byte, 9) // 4 + dash + 4
	for i := 0; i < 4; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[n.Int64()]
	}
	result[4] = '-'
	for i := 5; i < 9; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[n.Int64()]
	}
	return string(result), nil
}

// handleCLIDeviceCodeCreate handles POST /api/auth/device-code (ADR-0054).
// Initiates device-code login flow for CLI. Returns device code and user code.
func (s *Server) handleCLIDeviceCodeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	deviceCode, err := generateCLIDeviceCode()
	if err != nil {
		http.Error(w, "failed to generate device code", http.StatusInternalServerError)
		return
	}
	userCode, err := generateUserCode()
	if err != nil {
		http.Error(w, "failed to generate user code", http.StatusInternalServerError)
		return
	}

	const ttlMinutes = 15
	expiresAt := time.Now().Add(ttlMinutes * time.Minute)

	globalCLIDeviceCodeStore.mu.Lock()
	globalCLIDeviceCodeStore.codes[deviceCode] = &cliDeviceCodeEntry{
		UserCode:  userCode,
		ExpiresAt: expiresAt,
	}
	globalCLIDeviceCodeStore.mu.Unlock()

	verificationURL := s.externalURL + "/api/auth/device-code/verify?code=" + userCode

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CLIDeviceCodeResponse{ //nolint:errcheck
		DeviceCode:      deviceCode,
		UserCode:        userCode,
		VerificationURL: verificationURL,
		ExpiresIn:       ttlMinutes * 60,
		Interval:        5, // poll every 5 seconds
	})
}

// handleCLIDeviceCodePoll handles POST /api/auth/device-code/poll (ADR-0054).
// CLI polls this until the user completes verification or the code expires.
func (s *Server) handleCLIDeviceCodePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CLIDeviceCodePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.DeviceCode == "" {
		http.Error(w, "deviceCode is required", http.StatusBadRequest)
		return
	}

	globalCLIDeviceCodeStore.mu.RLock()
	entry, exists := globalCLIDeviceCodeStore.codes[req.DeviceCode]
	globalCLIDeviceCodeStore.mu.RUnlock()

	if !exists {
		http.Error(w, "device code not found", http.StatusNotFound)
		return
	}
	if time.Now().After(entry.ExpiresAt) {
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]string{"status": "expired"}) //nolint:errcheck
		return
	}

	if entry.Completed {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CLIDeviceCodePollResponse{ //nolint:errcheck
			Status:   "authorized",
			JWT:      entry.JWT,
			UserSlug: entry.UserSlug,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CLIDeviceCodePollResponse{ //nolint:errcheck
		Status: "pending",
	})
}

// handleCLIDeviceCodeVerify handles GET /api/auth/device-code/verify (ADR-0054).
// Web UI endpoint where the user enters the device code and authenticates.
// This endpoint serves the verification page (or handles the form submission).
// For simplicity, this serves a minimal HTML page that accepts the user code
// and authenticates via the user's existing session cookie/JWT.
func (s *Server) handleCLIDeviceCodeVerify(w http.ResponseWriter, r *http.Request) {
	// GET: serve the verification page
	if r.Method == http.MethodGet {
		userCode := r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>MClaude CLI Authorization</title></head>
<body>
<h1>Authorize CLI Access</h1>
<p>Enter the code shown in your terminal:</p>
<form method="POST" action="/api/auth/device-code/verify">
  <input type="hidden" name="code" value="%s">
  <input type="text" name="user_code" placeholder="Enter code" value="%s">
  <input type="password" name="password" placeholder="Password">
  <input type="email" name="email" placeholder="Email">
  <button type="submit">Authorize</button>
</form>
</body>
</html>`, userCode, userCode)
		return
	}

	// POST: process the form submission
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		userCode := r.FormValue("user_code")
		email := r.FormValue("email")
		password := r.FormValue("password")

		if userCode == "" || email == "" || password == "" {
			http.Error(w, "user_code, email, and password are required", http.StatusBadRequest)
			return
		}

		if s.db == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		// Authenticate the user
		user, err := s.db.GetUserByEmail(r.Context(), email)
		if err != nil || user == nil || !checkPassword(password, user.PasswordHash) {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		// Find the device code entry by user code
		var deviceCode string
		var entry *cliDeviceCodeEntry
		globalCLIDeviceCodeStore.mu.Lock()
		for code, e := range globalCLIDeviceCodeStore.codes {
			if e.UserCode == userCode && !e.Completed && time.Now().Before(e.ExpiresAt) {
				deviceCode = code
				entry = e
				break
			}
		}
		if entry != nil {
			// Issue JWT for the user
			expirySecs := int64(s.jwtExpiry.Seconds())
			var jwt string
			if user.NKeyPublic != nil && *user.NKeyPublic != "" {
				hostSlugs, _ := s.db.GetHostAccessSlugs(r.Context(), user.ID)
				jwt, _ = IssueUserJWT(*user.NKeyPublic, user.ID, user.Slug, hostSlugs, s.accountKP, expirySecs)
			} else {
				jwt, _, _ = IssueUserJWTLegacy(user.ID, user.Slug, s.accountKP, expirySecs)
			}
			entry.Completed = true
			entry.UserID = user.ID
			entry.UserSlug = user.Slug
			entry.JWT = jwt
		}
		globalCLIDeviceCodeStore.mu.Unlock()

		if entry == nil || deviceCode == "" {
			http.Error(w, "invalid or expired user code", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>CLI Authorized</title></head>
<body>
<h1>✓ CLI Authorized</h1>
<p>You can close this tab. Your CLI is now authenticated.</p>
</body>
</html>`)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

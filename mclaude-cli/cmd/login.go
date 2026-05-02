// Package cmd — login.go implements the mclaude login command.
//
// Device-code flow (spec-cli.md):
//
//  1. CLI generates an NKey pair locally — the private seed never leaves the machine.
//  2. CLI sends POST /api/auth/device-code to the control-plane with { publicKey }.
//  3. Control-plane returns { deviceCode, userCode, verificationUrl, expiresIn, interval }.
//  4. CLI displays the verification URL and user code.
//  5. CLI polls POST /api/auth/device-code/poll with { deviceCode } at the specified interval.
//  6. On success, the poll response returns { status: "authorized", jwt, userSlug }.
//  7. CLI writes credentials to ~/.mclaude/auth.json (mode 0600).
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	clicontext "mclaude-cli/context"

	"github.com/nats-io/nkeys"
)

// LoginFlags holds parsed flags for "mclaude login".
type LoginFlags struct {
	// ServerURL overrides the control-plane base URL.
	// Defaults to context.Server or DefaultServerURL.
	ServerURL string
	// ContextPath overrides ~/.mclaude/context.json (for tests).
	ContextPath string
	// AuthPath overrides ~/.mclaude/auth.json (for tests).
	AuthPath string
}

// LoginResult is returned by RunLogin on success.
type LoginResult struct {
	// UserSlug is the authenticated user's slug.
	UserSlug string
	// AuthPath is the path where credentials were written.
	AuthPath string
}

// deviceCodeRequest is the POST /api/auth/device-code request body.
type deviceCodeRequest struct {
	PublicKey string `json:"publicKey"`
}

// deviceCodeResponse is the POST /api/auth/device-code response body.
type deviceCodeResponse struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
	ExpiresIn       int    `json:"expiresIn"` // seconds
	Interval        int    `json:"interval"`  // polling interval in seconds
}

// deviceCodePollRequest is the POST /api/auth/device-code/poll request body.
type deviceCodePollRequest struct {
	DeviceCode string `json:"deviceCode"`
}

// deviceCodePollResponse is the POST /api/auth/device-code/poll response body.
// Status is "pending" while the user has not yet verified the code, or
// "authorized" once the user completes authentication.
// On "authorized", JWT and UserSlug are populated.
type deviceCodePollResponse struct {
	// Status is "pending" or "authorized".
	Status   string `json:"status"`
	JWT      string `json:"jwt,omitempty"`
	UserSlug string `json:"userSlug,omitempty"`
	// Error is set when the code has expired or an error occurred.
	Error string `json:"error,omitempty"`
}

// RunLogin performs the device-code authentication flow and writes credentials
// to ~/.mclaude/auth.json.
//
// HTTP calls are made to the control-plane. If the control-plane is unavailable,
// RunLogin returns an error describing the failure.
func RunLogin(flags LoginFlags, out io.Writer) (*LoginResult, error) {
	// Resolve context.
	ctxPath := flags.ContextPath
	if ctxPath == "" {
		ctxPath = clicontext.DefaultPath()
	}
	ctx, err := clicontext.Load(ctxPath)
	if err != nil {
		ctx = &clicontext.Context{}
	}

	serverURL := clicontext.ResolveServerURL(flags.ServerURL, ctx)

	// 1. Generate NKey pair locally. The seed never leaves this machine.
	kp, err := nkeys.CreateUser()
	if err != nil {
		return nil, fmt.Errorf("generate nkey pair: %w", err)
	}
	pubKey, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("get nkey public key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, fmt.Errorf("get nkey seed: %w", err)
	}

	// 2. POST /api/auth/device-code with { publicKey }.
	dcResp, err := postDeviceCode(serverURL, pubKey)
	if err != nil {
		return nil, fmt.Errorf("request device code: %w", err)
	}

	// 3. Display the verification URL and user code.
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "To authenticate, open the following URL in your browser:")
	fmt.Fprintf(out, "  %s\n", dcResp.VerificationURL)
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "Then enter the code: %s\n", dcResp.UserCode)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Waiting for browser authentication...")

	// 4. Poll until success or expiry.
	interval := time.Duration(dcResp.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)
	if dcResp.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}

	var jwt, userSlug string
	for time.Now().Before(deadline) {
		time.Sleep(interval)

		pollResp, err := pollDeviceCode(serverURL, dcResp.DeviceCode)
		if err != nil {
			// Transient error — keep polling.
			continue
		}
		if pollResp.Error != "" {
			return nil, fmt.Errorf("authentication failed: %s", pollResp.Error)
		}
		if pollResp.Status == "pending" {
			// User hasn't completed authentication yet — keep polling.
			continue
		}
		if pollResp.Status == "authorized" && pollResp.JWT != "" {
			jwt = pollResp.JWT
			userSlug = pollResp.UserSlug
			break
		}
	}

	if jwt == "" {
		return nil, fmt.Errorf("authentication timed out: device code expired after %d seconds", dcResp.ExpiresIn)
	}

	// 5. Write credentials to ~/.mclaude/auth.json.
	authPath := flags.AuthPath
	if authPath == "" {
		authPath = DefaultAuthPath()
	}
	creds := &AuthCredentials{
		JWT:      jwt,
		NKeySeed: string(seed),
		UserSlug: userSlug,
	}
	if err := SaveAuth(authPath, creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}

	// Update context.json with the user slug.
	ctx.UserSlug = userSlug
	if err := clicontext.Save(ctxPath, ctx); err != nil {
		// Non-fatal: auth.json is the primary credential store.
		fmt.Fprintf(out, "warning: could not update context file: %v\n", err)
	}

	fmt.Fprintf(out, "Logged in as %s\n", userSlug)
	fmt.Fprintf(out, "Credentials saved to %s\n", authPath)

	return &LoginResult{
		UserSlug: userSlug,
		AuthPath: authPath,
	}, nil
}

// postDeviceCode sends POST /api/auth/device-code with the NKey public key.
func postDeviceCode(serverURL, pubKey string) (*deviceCodeResponse, error) {
	body, err := json.Marshal(deviceCodeRequest{PublicKey: pubKey})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := http.Post(serverURL+"/api/auth/device-code", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST /api/auth/device-code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST /api/auth/device-code: unexpected status %d", resp.StatusCode)
	}
	var dcResp deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcResp); err != nil {
		return nil, fmt.Errorf("decode device-code response: %w", err)
	}
	if dcResp.DeviceCode == "" {
		return nil, fmt.Errorf("server returned empty device code")
	}
	return &dcResp, nil
}

// pollDeviceCode sends POST /api/auth/device-code/poll.
func pollDeviceCode(serverURL, deviceCode string) (*deviceCodePollResponse, error) {
	body, err := json.Marshal(deviceCodePollRequest{DeviceCode: deviceCode})
	if err != nil {
		return nil, fmt.Errorf("marshal poll request: %w", err)
	}
	resp, err := http.Post(serverURL+"/api/auth/device-code/poll", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST /api/auth/device-code/poll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST /api/auth/device-code/poll: unexpected status %d", resp.StatusCode)
	}
	var pollResp deviceCodePollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}
	return &pollResp, nil
}

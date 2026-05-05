package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/nats-io/nkeys"
)

// agentJWTTTL is the expected TTL for session-agent JWTs.
// The refresh loop triggers at TTL - refreshBuffer before expiry.
const agentJWTTTL = 5 * time.Minute

// agentJWTRefreshBuffer is how early the refresh fires before TTL expiry.
const agentJWTRefreshBuffer = 60 * time.Second

// AgentAuth holds the NKey pair and JWT for HTTP challenge-response authentication
// (ADR-0054). The NKey seed never leaves this struct — only the public key and
// signed JWT are shared with external parties.
//
// Usage:
//  1. Create with NewAgentAuth().
//  2. Call PublicKey() to get the public key string to send to the host controller.
//  3. Call Authenticate(ctx, authURL) to run the challenge-response flow and get a JWT.
//  4. Use JWTFunc() and SignFunc() with nats.UserJWT() for NATS connection.
//  5. Call StartRefreshLoop(ctx, authURL) to proactively refresh before TTL expiry.
type AgentAuth struct {
	kp         nkeys.KeyPair
	mu         sync.RWMutex
	currentJWT string
}

// NewAgentAuth generates a new NKey pair at startup. The private seed never
// leaves the process — only the public key is shared with the host controller.
func NewAgentAuth() (*AgentAuth, error) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		return nil, fmt.Errorf("generate NKey pair: %w", err)
	}
	return &AgentAuth{kp: kp}, nil
}

// PublicKey returns the NKey public key string (starts with "U").
// Send this to the host controller for registration with CP.
func (a *AgentAuth) PublicKey() (string, error) {
	return a.kp.PublicKey()
}

// JWTFunc returns a function suitable for use with nats.UserJWT().
// The function returns the most recently fetched JWT.
func (a *AgentAuth) JWTFunc() func() (string, error) {
	return func() (string, error) {
		a.mu.RLock()
		jwt := a.currentJWT
		a.mu.RUnlock()
		if jwt == "" {
			return "", fmt.Errorf("no JWT available — call Authenticate() first")
		}
		return jwt, nil
	}
}

// SignFunc returns a function suitable for use with nats.UserJWT() for NKey signing.
// The NATS server sends a challenge nonce; this signs it with the agent's private seed.
func (a *AgentAuth) SignFunc() func([]byte) ([]byte, error) {
	return func(nonce []byte) ([]byte, error) {
		return a.kp.Sign(nonce)
	}
}

// Authenticate runs the HTTP challenge-response flow against the CP auth endpoint.
// On success, stores the JWT internally and returns it.
// Requires that the agent's public key has been registered with CP via the host controller.
//
// authURL is the base URL of the control-plane (e.g. "https://cp.example.com").
// The function calls:
//   - POST {authURL}/api/auth/challenge {"nkey_public": "<key>"}
//   - POST {authURL}/api/auth/verify   {"nkey_public": "<key>", "challenge": "<nonce>", "signature": "<sig>"}
func (a *AgentAuth) Authenticate(ctx context.Context, authURL string) (string, error) {
	pubKey, err := a.kp.PublicKey()
	if err != nil {
		return "", fmt.Errorf("get public key: %w", err)
	}

	// Step 1: Request a challenge nonce.
	challenge, err := a.requestChallenge(ctx, authURL, pubKey)
	if err != nil {
		return "", fmt.Errorf("request challenge: %w", err)
	}

	// Step 2: Sign the challenge nonce with the NKey seed.
	sig, err := a.kp.Sign([]byte(challenge))
	if err != nil {
		return "", fmt.Errorf("sign challenge: %w", err)
	}

	// Step 3: Verify the signature and receive the JWT.
	jwt, err := a.verifyChallenge(ctx, authURL, pubKey, challenge, sig)
	if err != nil {
		return "", fmt.Errorf("verify challenge: %w", err)
	}

	// Store the JWT for subsequent JWTFunc() calls.
	a.mu.Lock()
	a.currentJWT = jwt
	a.mu.Unlock()

	return jwt, nil
}

// requestChallenge calls POST /api/auth/challenge and returns the nonce.
func (a *AgentAuth) requestChallenge(ctx context.Context, authURL, pubKey string) (string, error) {
	body, _ := json.Marshal(map[string]string{"nkey_public": pubKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/api/auth/challenge", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /api/auth/challenge: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("challenge returned HTTP %d: %s", resp.StatusCode, data)
	}

	var result struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode challenge response: %w", err)
	}
	if result.Challenge == "" {
		return "", fmt.Errorf("challenge response missing 'challenge' field")
	}
	return result.Challenge, nil
}

// verifyChallenge calls POST /api/auth/verify and returns the JWT.
func (a *AgentAuth) verifyChallenge(ctx context.Context, authURL, pubKey, challenge string, sig []byte) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"nkey_public": pubKey,
		"challenge":   challenge,
		"signature":   sig,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/api/auth/verify", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /api/auth/verify: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("verify returned HTTP %d: %s", resp.StatusCode, data)
	}

	var result struct {
		OK    bool   `json:"ok"`
		JWT   string `json:"jwt"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode verify response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("verify rejected: %s", result.Error)
	}
	if result.JWT == "" {
		return "", fmt.Errorf("verify response missing 'jwt' field")
	}
	return result.JWT, nil
}

// StartRefreshLoop runs a background goroutine that proactively refreshes the JWT
// before the 5-minute TTL expires. The loop fires at (TTL - refreshBuffer) intervals.
// Errors are written to stderr but do not crash the agent — the current JWT is used
// until it expires, at which point NATS will surface auth errors.
//
// The loop is also triggered immediately on "permissions violation" events from the
// permViolationCh channel. Pass nil to disable this trigger.
func (a *AgentAuth) StartRefreshLoop(ctx context.Context, authURL string, permViolationCh <-chan struct{}) {
	ticker := time.NewTicker(agentJWTTTL - agentJWTRefreshBuffer)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := a.Authenticate(ctx, authURL); err != nil {
					// Log as warning; current JWT is still valid for up to refreshBuffer.
					fmt.Fprintf(os.Stderr, "agent_auth: JWT refresh failed: %v\n", err)
				}
			case _, ok := <-permViolationCh:
				if !ok {
					return
				}
				// Immediate refresh on permissions violation.
				if _, err := a.Authenticate(ctx, authURL); err != nil {
					fmt.Fprintf(os.Stderr, "agent_auth: immediate JWT refresh failed: %v\n", err)
				}
			}
		}
	}()
}

// WritePublicKeyToFile writes the NKey public key to the given file path.
// Used by the agent to expose its public key to the host controller via local IPC
// (BYOH: write to a temp file and tell the controller the path; K8s: shared volume).
func (a *AgentAuth) WritePublicKeyToFile(path string) error {
	pubKey, err := a.kp.PublicKey()
	if err != nil {
		return fmt.Errorf("get public key: %w", err)
	}
	return os.WriteFile(path, []byte(pubKey+"\n"), 0600)
}

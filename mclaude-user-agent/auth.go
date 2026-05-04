package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
)

const (
	// agentJWTTTL is the expected TTL for user-agent JWTs (5-minute, per ADR-0091).
	agentJWTTTL = 5 * time.Minute
	// agentJWTRefreshBuffer is how early the refresh fires before TTL expiry.
	agentJWTRefreshBuffer = 60 * time.Second
)

// UserAgentAuth manages the NKey identity and JWT credential for the user-agent.
// It extends the hostauth challenge-response pattern by sending x25519_pub during
// the verify step (ADR-0091).
type UserAgentAuth struct {
	kp        nkeys.KeyPair
	cpURL     string
	x25519Pub []byte // 32-byte X25519 public key, set after keypair generation
	log       zerolog.Logger

	mu         sync.RWMutex
	currentJWT string
}

// NewUserAgentAuth creates a UserAgentAuth from an NKey seed byte slice.
// The seed must be the raw NATS seed string (SUAB... or similar).
func NewUserAgentAuth(seed []byte, cpURL string, log zerolog.Logger) (*UserAgentAuth, error) {
	kp, err := nkeys.ParseDecoratedUserNKey(seed)
	if err != nil {
		return nil, fmt.Errorf("parse NKey seed: %w", err)
	}
	return &UserAgentAuth{
		kp:    kp,
		cpURL: cpURL,
		log:   log,
	}, nil
}

// SetX25519Pub stores the X25519 public key to be sent during challenge-response verify.
// Must be called before Authenticate.
func (a *UserAgentAuth) SetX25519Pub(pub []byte) {
	a.mu.Lock()
	a.x25519Pub = pub
	a.mu.Unlock()
}

// PublicKey returns the NKey public key string.
func (a *UserAgentAuth) PublicKey() string {
	pub, err := a.kp.PublicKey()
	if err != nil {
		panic(fmt.Sprintf("user_agent_auth: PublicKey() failed: %v", err))
	}
	return pub
}

// JWTFunc returns a function for nats.UserJWT() that returns the current JWT.
func (a *UserAgentAuth) JWTFunc() func() (string, error) {
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

// SignFunc returns a function for nats.UserJWT() that signs NATS nonces.
func (a *UserAgentAuth) SignFunc() func([]byte) ([]byte, error) {
	return func(nonce []byte) ([]byte, error) {
		return a.kp.Sign(nonce)
	}
}

// Authenticate runs the HTTP challenge-response flow against the CP auth endpoint.
// Sends x25519_pub in the verify request so CP can store it (ADR-0091).
// Returns the JWT and whether it is a full JWT (vs bootstrap).
func (a *UserAgentAuth) Authenticate(ctx context.Context) (jwt string, err error) {
	pubKey := a.PublicKey()

	// Step 1: Request a challenge nonce.
	challenge, err := a.requestChallenge(ctx, pubKey)
	if err != nil {
		return "", fmt.Errorf("request challenge: %w", err)
	}

	// Step 2: base64-decode the nonce (per ADR-0075), then sign the raw bytes.
	nonceBytes, err := base64.StdEncoding.DecodeString(challenge)
	if err != nil {
		return "", fmt.Errorf("decode challenge nonce: %w", err)
	}
	sig, err := a.kp.Sign(nonceBytes)
	if err != nil {
		return "", fmt.Errorf("sign challenge: %w", err)
	}

	// Step 3: Verify with x25519_pub field.
	newJWT, err := a.verifyChallenge(ctx, pubKey, challenge, sig)
	if err != nil {
		return "", fmt.Errorf("verify challenge: %w", err)
	}

	a.mu.Lock()
	a.currentJWT = newJWT
	a.mu.Unlock()

	return newJWT, nil
}

// requestChallenge calls POST /api/auth/challenge and returns the nonce.
func (a *UserAgentAuth) requestChallenge(ctx context.Context, pubKey string) (string, error) {
	body, _ := json.Marshal(map[string]string{"nkey_public": pubKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cpURL+"/api/auth/challenge", bytes.NewReader(body))
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

// verifyChallenge calls POST /api/auth/verify with the x25519_pub field.
// CP stores the x25519_pub in user_host_agents.x25519_pub on success (ADR-0091).
func (a *UserAgentAuth) verifyChallenge(ctx context.Context, pubKey, challenge string, sig []byte) (string, error) {
	a.mu.RLock()
	x25519Pub := a.x25519Pub
	a.mu.RUnlock()

	payload := map[string]interface{}{
		"nkey_public": pubKey,
		"challenge":   challenge,
		"signature":   sig,
	}
	if len(x25519Pub) > 0 {
		payload["x25519_pub"] = base64.StdEncoding.EncodeToString(x25519Pub)
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cpURL+"/api/auth/verify", bytes.NewReader(body))
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
// before the 5-minute TTL expires. Errors are logged but do not crash the agent.
// onRefresh is called after each successful refresh (used to reconnect NATS if needed).
func (a *UserAgentAuth) StartRefreshLoop(ctx context.Context, onRefresh func(jwt string)) {
	a.startRefreshLoopWithInterval(ctx, agentJWTTTL-agentJWTRefreshBuffer, onRefresh)
}

// startRefreshLoopWithInterval is the internal implementation, extracted for unit testing.
func (a *UserAgentAuth) startRefreshLoopWithInterval(ctx context.Context, interval time.Duration, onRefresh func(jwt string)) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				jwt, err := a.Authenticate(ctx)
				if err != nil {
					a.log.Warn().Err(err).Msg("user_agent_auth: JWT refresh failed")
				} else {
					a.log.Info().Msg("user_agent_auth: JWT refreshed")
					if onRefresh != nil {
						onRefresh(jwt)
					}
				}
			}
		}
	}()
}

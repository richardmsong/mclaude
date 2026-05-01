package main

import (
	"bytes"
	"context"
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
	// hostJWTTTL is the expected TTL for host-scoped JWTs (ADR-0054).
	hostJWTTTL = 5 * time.Minute
	// hostJWTRefreshBuffer is how early the refresh fires before TTL expiry.
	hostJWTRefreshBuffer = 60 * time.Second
)

// HostAuth manages the host controller's NKey identity and JWT credential refresh.
// It loads the NKey seed from the .creds file and handles proactive JWT refresh
// via the CP HTTP challenge-response protocol (ADR-0054).
type HostAuth struct {
	kp    nkeys.KeyPair
	cpURL string
	log   zerolog.Logger

	mu         sync.RWMutex
	currentJWT string
}

// NewHostAuthFromCredsFile loads the NKey key pair and current JWT from the .creds
// file at credsFile. cpURL is the base URL of the control-plane for HTTP
// challenge-response refresh (e.g. "https://cp.example.com"). If cpURL is empty,
// JWT refresh is disabled and the host JWT is used as-is until it expires.
func NewHostAuthFromCredsFile(credsData []byte, cpURL string, log zerolog.Logger) (*HostAuth, error) {
	kp, err := nkeys.ParseDecoratedUserNKey(credsData)
	if err != nil {
		return nil, fmt.Errorf("parse NKey from creds: %w", err)
	}

	jwt, err := nkeys.ParseDecoratedJWT(credsData)
	if err != nil {
		return nil, fmt.Errorf("parse JWT from creds: %w", err)
	}

	return &HostAuth{
		kp:         kp,
		cpURL:      cpURL,
		log:        log,
		currentJWT: jwt,
	}, nil
}

// JWTFunc returns a function for nats.UserJWT() that returns the current JWT.
// The returned JWT is updated in-place when Refresh() succeeds, so NATS will
// use the latest JWT on the next reconnect challenge.
func (h *HostAuth) JWTFunc() func() (string, error) {
	return func() (string, error) {
		h.mu.RLock()
		jwt := h.currentJWT
		h.mu.RUnlock()
		return jwt, nil
	}
}

// SignFunc returns a function for nats.UserJWT() that signs NATS nonces with
// the host's NKey private seed.
func (h *HostAuth) SignFunc() func([]byte) ([]byte, error) {
	return func(nonce []byte) ([]byte, error) {
		return h.kp.Sign(nonce)
	}
}

// PublicKey returns the NKey public key string (starts with "U").
func (h *HostAuth) PublicKey() (string, error) {
	return h.kp.PublicKey()
}

// Refresh runs the HTTP challenge-response flow against the CP auth endpoint.
// On success, updates the stored JWT. The NATS connection picks up the new JWT
// on the next auth challenge (reconnect). Requires cpURL to be set.
func (h *HostAuth) Refresh(ctx context.Context) (string, error) {
	if h.cpURL == "" {
		return "", fmt.Errorf("no CP URL configured")
	}

	pubKey, err := h.kp.PublicKey()
	if err != nil {
		return "", fmt.Errorf("get public key: %w", err)
	}

	// Step 1: request a challenge nonce.
	challenge, err := h.requestChallenge(ctx, pubKey)
	if err != nil {
		return "", fmt.Errorf("request challenge: %w", err)
	}

	// Step 2: sign the nonce with the NKey seed.
	sig, err := h.kp.Sign([]byte(challenge))
	if err != nil {
		return "", fmt.Errorf("sign challenge: %w", err)
	}

	// Step 3: verify the signature and receive the new JWT.
	newJWT, err := h.verifyChallenge(ctx, pubKey, challenge, sig)
	if err != nil {
		return "", fmt.Errorf("verify challenge: %w", err)
	}

	h.mu.Lock()
	h.currentJWT = newJWT
	h.mu.Unlock()

	return newJWT, nil
}

// StartRefreshLoop runs a background goroutine that proactively refreshes the host
// JWT before the 5-minute TTL expires (ADR-0054). The loop fires at
// hostJWTTTL - hostJWTRefreshBuffer intervals. If cpURL is empty, the loop is a
// no-op and a warning is logged.
//
// Refresh errors are logged as warnings but do not crash the controller — the
// current JWT remains valid for up to hostJWTRefreshBuffer before expiry.
func (h *HostAuth) StartRefreshLoop(ctx context.Context) {
	if h.cpURL == "" {
		h.log.Warn().Msg("host_auth: CP URL not configured — host JWT refresh disabled (JWT will expire in 5 min)")
		return
	}

	go func() {
		ticker := time.NewTicker(hostJWTTTL - hostJWTRefreshBuffer)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := h.Refresh(ctx); err != nil {
					h.log.Warn().Err(err).Msg("host_auth: JWT refresh failed (current JWT still valid)")
				} else {
					h.log.Info().Msg("host_auth: host JWT refreshed successfully")
				}
			}
		}
	}()
}

// requestChallenge calls POST /api/auth/challenge and returns the nonce.
func (h *HostAuth) requestChallenge(ctx context.Context, pubKey string) (string, error) {
	body, _ := json.Marshal(map[string]string{"nkey_public": pubKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cpURL+"/api/auth/challenge", bytes.NewReader(body))
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

// verifyChallenge calls POST /api/auth/verify and returns the new JWT.
func (h *HostAuth) verifyChallenge(ctx context.Context, pubKey, challenge string, sig []byte) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"nkey_public": pubKey,
		"challenge":   challenge,
		"signature":   sig,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cpURL+"/api/auth/verify", bytes.NewReader(body))
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

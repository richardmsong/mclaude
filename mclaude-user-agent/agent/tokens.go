package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"
	agentcrypto "mclaude-user-agent/crypto"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// TokenDeliveryRequest is the NATS payload for agent.token-delivery (request/reply).
type TokenDeliveryRequest struct {
	Provider     string `json:"provider"`
	TokenWrapped string `json:"token_wrapped"` // base64(crypto_box_seal(token, ua_x25519_pub))
}

// TokenDeliveryReply is the NATS reply for agent.token-delivery.
type TokenDeliveryReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TokenManager handles token delivery and Secret management for the user-agent.
type TokenManager struct {
	k8s        kubernetes.Interface
	namespace  string
	uslug      string
	keypair    *agentcrypto.X25519Keypair
	secretName string
	log        zerolog.Logger
}

// NewTokenManager creates a TokenManager.
func NewTokenManager(k8s kubernetes.Interface, namespace, uslug string, keypair *agentcrypto.X25519Keypair, log zerolog.Logger) *TokenManager {
	return &TokenManager{
		k8s:        k8s,
		namespace:  namespace,
		uslug:      uslug,
		keypair:    keypair,
		secretName: "user-tokens-" + uslug,
		log:        log,
	}
}

// HandleDelivery processes a token-delivery request:
//  1. base64-decode token_wrapped
//  2. unseal with X25519 private key
//  3. write plaintext to user-tokens-{uslug} Secret under key {provider}
//  4. return {ok: true} or {ok: false, error: "decrypt_failed"}
func (tm *TokenManager) HandleDelivery(ctx context.Context, payload []byte) []byte {
	var req TokenDeliveryRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		tm.log.Warn().Err(err).Msg("token-delivery: bad payload")
		return replyJSON(TokenDeliveryReply{OK: false, Error: "decrypt_failed"})
	}

	if req.Provider == "" {
		req.Provider = "anthropic"
	}

	// Decode the base64 sealed box.
	sealedBytes, err := base64.StdEncoding.DecodeString(req.TokenWrapped)
	if err != nil {
		tm.log.Warn().Err(err).Msg("token-delivery: base64 decode failed")
		return replyJSON(TokenDeliveryReply{OK: false, Error: "decrypt_failed"})
	}

	// Unseal with X25519 private key.
	plaintext, err := agentcrypto.SealedBoxOpen(sealedBytes, &tm.keypair.Public, &tm.keypair.Private)
	if err != nil {
		tm.log.Warn().Err(err).Msg("token-delivery: unseal failed")
		return replyJSON(TokenDeliveryReply{OK: false, Error: "decrypt_failed"})
	}

	// Write plaintext token to user-tokens-{uslug} Secret under key {provider}.
	if err := tm.patchTokenSecret(ctx, req.Provider, plaintext); err != nil {
		tm.log.Error().Err(err).Str("provider", req.Provider).Msg("token-delivery: write Secret failed")
		return replyJSON(TokenDeliveryReply{OK: false, Error: "secret_write_failed"})
	}

	tm.log.Info().Str("provider", req.Provider).Msg("token-delivery: token written to Secret")
	return replyJSON(TokenDeliveryReply{OK: true})
}

// patchTokenSecret writes or updates a single key in the user-tokens-{uslug} Secret.
// The Secret is created empty by mclaude-controller-k8s; user-agent only patches data keys.
func (tm *TokenManager) patchTokenSecret(ctx context.Context, provider string, token []byte) error {
	secretName := tm.secretName

	// Build a strategic-merge-patch that only updates the one provider key.
	patchPayload, err := json.Marshal(map[string]interface{}{
		"data": map[string][]byte{
			provider: token,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	_, err = tm.k8s.CoreV1().Secrets(tm.namespace).Patch(
		ctx,
		secretName,
		types.StrategicMergePatchType,
		patchPayload,
		metav1.PatchOptions{},
	)
	if err == nil {
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("patch Secret %s: %w", secretName, err)
	}

	// Secret doesn't exist — this shouldn't happen (controller should have created it),
	// but handle gracefully by creating it.
	tm.log.Warn().Str("secret", secretName).Msg("token Secret not found; creating it")
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: tm.namespace,
		},
		Data: map[string][]byte{
			provider: token,
		},
	}
	_, createErr := tm.k8s.CoreV1().Secrets(tm.namespace).Create(ctx, s, metav1.CreateOptions{})
	if createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create Secret %s: %w", secretName, createErr)
	}
	return nil
}

// TokenRevokeRequest is the NATS payload for agent.token-revoke (request/reply).
type TokenRevokeRequest struct {
	Provider string `json:"provider"`
}

// TokenRevokeReply is the NATS reply for agent.token-revoke.
type TokenRevokeReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// StatusReply is the NATS reply for agent.status.
// tokenVersion is the first 8 hex chars of sha256(token), or "" if no token.
type StatusReply struct {
	HasToken     bool   `json:"hasToken"`
	TokenVersion string `json:"tokenVersion,omitempty"`
}

// HandleRevoke processes a token-revoke request (ADR-0092):
//  1. Parse {provider} from payload (defaults to "anthropic").
//  2. Delete the provider key from user-tokens-{uslug} Secret.
//  3. Return {ok: true} on success, {ok: false, error: "..."} on failure.
func (tm *TokenManager) HandleRevoke(ctx context.Context, payload []byte) []byte {
	var req TokenRevokeRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		tm.log.Warn().Err(err).Msg("token-revoke: bad payload")
		return replyRevokeJSON(TokenRevokeReply{OK: false, Error: "bad_request"})
	}
	if req.Provider == "" {
		req.Provider = "anthropic"
	}

	if err := tm.deleteTokenSecret(ctx, req.Provider); err != nil {
		tm.log.Error().Err(err).Str("provider", req.Provider).Msg("token-revoke: delete Secret key failed")
		return replyRevokeJSON(TokenRevokeReply{OK: false, Error: "secret_delete_failed"})
	}

	tm.log.Info().Str("provider", req.Provider).Msg("token-revoke: provider key deleted from Secret")
	return replyRevokeJSON(TokenRevokeReply{OK: true})
}

// HandleStatus processes an agent.status request (ADR-0092):
//
// Request: {} (empty; payload ignored).
// Reply: {hasToken: bool, tokenVersion: "<first-8-hex-of-sha256>"}.
//
// tokenVersion is computed over the raw bytes stored in the Secret for the
// "anthropic" provider. If no token is stored, hasToken is false and
// tokenVersion is omitted.
func (tm *TokenManager) HandleStatus(ctx context.Context, _ []byte) []byte {
	reply := tm.buildStatusReply(ctx)
	b, _ := json.Marshal(reply)
	return b
}

// buildStatusReply reads the current token from the Secret and returns a StatusReply.
func (tm *TokenManager) buildStatusReply(ctx context.Context) StatusReply {
	secret, err := tm.k8s.CoreV1().Secrets(tm.namespace).Get(ctx, tm.secretName, metav1.GetOptions{})
	if err != nil {
		tm.log.Warn().Err(err).Str("secret", tm.secretName).Msg("status: cannot read Secret")
		return StatusReply{HasToken: false}
	}
	token := secret.Data["anthropic"]
	if len(token) == 0 {
		return StatusReply{HasToken: false}
	}
	return StatusReply{
		HasToken:     true,
		TokenVersion: tokenVersion(token),
	}
}

// tokenVersion returns the first 8 hex characters of sha256(token).
func tokenVersion(token []byte) string {
	sum := sha256.Sum256(token)
	return fmt.Sprintf("%x", sum[:4]) // 4 bytes = 8 hex chars
}

// deleteTokenSecret removes a single provider key from the user-tokens-{uslug} Secret
// using a strategic merge patch that sets the key to nil (removes it).
func (tm *TokenManager) deleteTokenSecret(ctx context.Context, provider string) error {
	// A strategic merge patch with a null value removes the key from Secret.Data.
	patchPayload, err := json.Marshal(map[string]interface{}{
		"data": map[string]interface{}{
			provider: nil,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal delete patch: %w", err)
	}

	_, err = tm.k8s.CoreV1().Secrets(tm.namespace).Patch(
		ctx,
		tm.secretName,
		types.StrategicMergePatchType,
		patchPayload,
		metav1.PatchOptions{},
	)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Secret doesn't exist — treat as success (already deleted).
			return nil
		}
		return fmt.Errorf("patch Secret %s: %w", tm.secretName, err)
	}
	return nil
}

// replyJSON marshals a TokenDeliveryReply to JSON bytes.
func replyJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// replyRevokeJSON marshals a TokenRevokeReply to JSON bytes.
func replyRevokeJSON(v TokenRevokeReply) []byte {
	b, _ := json.Marshal(v)
	return b
}

package agent

import (
	"context"
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

// replyJSON marshals a TokenDeliveryReply to JSON bytes.
func replyJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

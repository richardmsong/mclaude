package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// X25519Keypair holds the X25519 private and public keys used for
// crypto_box_seal_open (NaCl sealed-box unsealing) — ADR-0091.
type X25519Keypair struct {
	Private [32]byte
	Public  [32]byte
}

// GenerateX25519Keypair generates a new X25519 keypair suitable for NaCl box operations.
// Uses crypto/ecdh.X25519() which handles RFC 7748 clamping internally — ADR-0096.
func GenerateX25519Keypair() (*X25519Keypair, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 keypair: %w", err)
	}

	kp := &X25519Keypair{}
	copy(kp.Private[:], key.Bytes())
	copy(kp.Public[:], key.PublicKey().Bytes())
	return kp, nil
}

// LoadOrGenerateX25519Keypair reads the x25519-private key from the user-agent-key
// Secret. If absent, generates a new keypair and writes the private key back to the
// Secret via the k8s API. Returns the keypair ready for use (ADR-0091 Boot step 2).
//
// The private key is stored as base64(32 raw bytes) in the Secret's Data["x25519-private"].
func LoadOrGenerateX25519Keypair(ctx context.Context, k8s kubernetes.Interface, namespace, secretName string) (*X25519Keypair, error) {
	secret, err := k8s.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %s not found: controller did not provision it", secretName)
		}
		return nil, fmt.Errorf("get secret %s: %w", secretName, err)
	}

	if privBytes, ok := secret.Data["x25519-private"]; ok && len(privBytes) > 0 {
		// k8s Secret Data values are raw bytes; the value was stored as base64 text.
		decoded, decErr := base64.StdEncoding.DecodeString(string(privBytes))
		if decErr != nil {
			// If not base64, treat as raw bytes.
			decoded = privBytes
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("x25519-private has wrong length %d (expected 32)", len(decoded))
		}
		ecdhKey, derivErr := ecdh.X25519().NewPrivateKey(decoded)
		if derivErr != nil {
			return nil, fmt.Errorf("derive public key from stored private: %w", derivErr)
		}
		kp := &X25519Keypair{}
		copy(kp.Private[:], ecdhKey.Bytes())
		copy(kp.Public[:], ecdhKey.PublicKey().Bytes())
		return kp, nil
	}

	// x25519-private absent — generate new keypair and write private key to Secret.
	kp, err := GenerateX25519Keypair()
	if err != nil {
		return nil, err
	}

	privB64 := base64.StdEncoding.EncodeToString(kp.Private[:])
	// Strategic-merge-patch on Secret.Data merges with existing keys (does not overwrite nkey-seed).
	patchData, err := json.Marshal(map[string]interface{}{
		"data": map[string][]byte{
			"x25519-private": []byte(privB64),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal x25519 patch: %w", err)
	}

	_, err = k8s.CoreV1().Secrets(namespace).Patch(
		ctx,
		secretName,
		types.StrategicMergePatchType,
		patchData,
		metav1.PatchOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("patch Secret %s with x25519-private: %w", secretName, err)
	}

	return kp, nil
}

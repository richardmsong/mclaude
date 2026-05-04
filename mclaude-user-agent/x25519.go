package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/curve25519"
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
func GenerateX25519Keypair() (*X25519Keypair, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return nil, fmt.Errorf("generate X25519 private key: %w", err)
	}
	// Clamp the private key per RFC 7748.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive X25519 public key: %w", err)
	}

	kp := &X25519Keypair{}
	copy(kp.Private[:], priv[:])
	copy(kp.Public[:], pub)
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
		kp := &X25519Keypair{}
		copy(kp.Private[:], decoded)
		pub, derivErr := curve25519.X25519(kp.Private[:], curve25519.Basepoint)
		if derivErr != nil {
			return nil, fmt.Errorf("derive public key from stored private: %w", derivErr)
		}
		copy(kp.Public[:], pub)
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

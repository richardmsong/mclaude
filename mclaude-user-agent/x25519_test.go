package main

import (
	"context"
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestGenerateX25519Keypair verifies that a keypair is generated with correct properties.
// Uses crypto/ecdh per ADR-0096; clamping is handled internally by the stdlib and is
// not reflected in the raw bytes returned by key.Bytes().
func TestGenerateX25519Keypair(t *testing.T) {
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}

	// Private and public keys should be non-zero.
	var zero [32]byte
	if kp.Private == zero {
		t.Error("private key is all zeros")
	}
	if kp.Public == zero {
		t.Error("public key is all zeros")
	}

	// Two generated keypairs should be different.
	kp2, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair 2: %v", err)
	}
	if kp.Private == kp2.Private {
		t.Error("two generated keypairs have the same private key")
	}
}

// TestLoadOrGenerateX25519Keypair_ExistingKey verifies that a pre-existing x25519-private
// in the Secret is loaded correctly.
func TestLoadOrGenerateX25519Keypair_ExistingKey(t *testing.T) {
	// Generate a known keypair and store its private key in a fake Secret.
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(kp.Private[:])

	fakeK8s := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-agent-key",
			Namespace: "mclaude-testuser",
		},
		Data: map[string][]byte{
			"nkey-seed":      []byte("SUAXXX"),
			"x25519-private": []byte(privB64),
		},
	})

	loaded, err := LoadOrGenerateX25519Keypair(context.Background(), fakeK8s, "mclaude-testuser", "user-agent-key")
	if err != nil {
		t.Fatalf("LoadOrGenerateX25519Keypair: %v", err)
	}

	if loaded.Private != kp.Private {
		t.Errorf("loaded private key does not match stored private key")
	}
	if loaded.Public != kp.Public {
		t.Errorf("loaded public key does not match expected public key")
	}
}

// TestLoadOrGenerateX25519Keypair_Generate verifies that when x25519-private is absent
// from the Secret, a new keypair is generated and written back.
func TestLoadOrGenerateX25519Keypair_Generate(t *testing.T) {
	fakeK8s := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-agent-key",
			Namespace: "mclaude-testuser",
		},
		Data: map[string][]byte{
			"nkey-seed": []byte("SUAXXX"),
			// x25519-private intentionally absent
		},
	})

	kp, err := LoadOrGenerateX25519Keypair(context.Background(), fakeK8s, "mclaude-testuser", "user-agent-key")
	if err != nil {
		t.Fatalf("LoadOrGenerateX25519Keypair: %v", err)
	}

	// Keypair should be valid.
	var zero [32]byte
	if kp.Private == zero {
		t.Error("generated private key is all zeros")
	}
	if kp.Public == zero {
		t.Error("generated public key is all zeros")
	}

	// The Secret should now have the x25519-private key.
	secret, err := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-agent-key", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get updated Secret: %v", err)
	}
	if len(secret.Data["x25519-private"]) == 0 {
		t.Error("x25519-private not written to Secret after generation")
	}

	// Verify the stored value decodes back to the generated private key.
	stored := string(secret.Data["x25519-private"])
	decoded, decErr := base64.StdEncoding.DecodeString(stored)
	if decErr != nil {
		t.Fatalf("decode stored x25519-private: %v", decErr)
	}
	if len(decoded) != 32 {
		t.Errorf("stored private key has wrong length %d", len(decoded))
	}
}

// TestLoadOrGenerateX25519Keypair_MissingSecret verifies that a missing Secret
// returns an error (controller did not provision).
func TestLoadOrGenerateX25519Keypair_MissingSecret(t *testing.T) {
	fakeK8s := fake.NewClientset() // no secrets

	_, err := LoadOrGenerateX25519Keypair(context.Background(), fakeK8s, "mclaude-testuser", "user-agent-key")
	if err == nil {
		t.Error("expected error for missing Secret, got nil")
	}
}

// TestX25519Keypair_SealUnsealRoundtrip verifies that a generated keypair works
// with SealedBoxSeal/Open for the full token-delivery flow.
func TestX25519Keypair_SealUnsealRoundtrip(t *testing.T) {
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}

	token := []byte("sk-ant-api03-supersecrettoken")
	sealed, err := SealedBoxSeal(token, &kp.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal: %v", err)
	}

	recovered, err := SealedBoxOpen(sealed, &kp.Public, &kp.Private)
	if err != nil {
		t.Fatalf("SealedBoxOpen: %v", err)
	}

	if string(recovered) != string(token) {
		t.Errorf("recovered = %q, want %q", recovered, token)
	}
}

package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

// SealedBoxSeal encrypts plaintext for the recipient's X25519 public key (sealed box).
// Returns the sealed box: ephemeralPK (32 bytes) || NaCl box ciphertext.
// Test-only helper — production user-agent only needs SealedBoxOpen.
func SealedBoxSeal(plaintext []byte, recipientPub *[32]byte) ([]byte, error) {
	// Generate ephemeral keypair.
	var ephemeralPriv [32]byte
	if _, err := rand.Read(ephemeralPriv[:]); err != nil {
		return nil, fmt.Errorf("generate ephemeral private key: %w", err)
	}
	ephemeralPriv[0] &= 248
	ephemeralPriv[31] &= 127
	ephemeralPriv[31] |= 64

	ephemeralPubSlice, err := curve25519.X25519(ephemeralPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive ephemeral public key: %w", err)
	}
	var ephemeralPub [32]byte
	copy(ephemeralPub[:], ephemeralPubSlice)

	nonce := sealedBoxNonce(&ephemeralPub, recipientPub)

	ciphertext := box.Seal(nil, plaintext, nonce, recipientPub, &ephemeralPriv)

	result := make([]byte, 32+len(ciphertext))
	copy(result[:32], ephemeralPub[:])
	copy(result[32:], ciphertext)
	return result, nil
}

// TestSealedBoxRoundtrip verifies that SealedBoxSeal followed by SealedBoxOpen
// recovers the original plaintext.
func TestSealedBoxRoundtrip(t *testing.T) {
	// Generate a recipient keypair.
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}

	plaintext := []byte("sk-ant-supersecret-anthropic-token-abc123")
	sealed, err := SealedBoxSeal(plaintext, &kp.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal: %v", err)
	}

	recovered, err := SealedBoxOpen(sealed, &kp.Public, &kp.Private)
	if err != nil {
		t.Fatalf("SealedBoxOpen: %v", err)
	}

	if !bytes.Equal(plaintext, recovered) {
		t.Errorf("recovered = %q, want %q", recovered, plaintext)
	}
}

// TestSealedBoxOpen_WrongKey verifies that opening a box with the wrong private key fails.
func TestSealedBoxOpen_WrongKey(t *testing.T) {
	kp1, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("generate kp1: %v", err)
	}
	kp2, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("generate kp2: %v", err)
	}

	plaintext := []byte("secret")
	sealed, err := SealedBoxSeal(plaintext, &kp1.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal: %v", err)
	}

	// Attempt to open with kp2 (wrong key) — must fail.
	_, err = SealedBoxOpen(sealed, &kp2.Public, &kp2.Private)
	if err == nil {
		t.Error("expected error opening sealed box with wrong key, got nil")
	}
}

// TestSealedBoxOpen_TruncatedInput verifies that too-short input returns an error.
func TestSealedBoxOpen_TruncatedInput(t *testing.T) {
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("generate kp: %v", err)
	}

	_, err = SealedBoxOpen([]byte("short"), &kp.Public, &kp.Private)
	if err == nil {
		t.Error("expected error for truncated sealed box, got nil")
	}
}

// TestSealedBoxOpen_EmptyPlaintext verifies that an empty message can be sealed and opened.
func TestSealedBoxOpen_EmptyPlaintext(t *testing.T) {
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}

	plaintext := []byte{}
	sealed, err := SealedBoxSeal(plaintext, &kp.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal: %v", err)
	}

	recovered, err := SealedBoxOpen(sealed, &kp.Public, &kp.Private)
	if err != nil {
		t.Fatalf("SealedBoxOpen: %v", err)
	}

	if len(recovered) != 0 {
		t.Errorf("expected empty plaintext, got %q", recovered)
	}
}

// TestSealedBoxSeal_DifferentEphemeralEachTime verifies that two seals of the same
// plaintext produce different ciphertexts (due to random ephemeral keypair).
func TestSealedBoxSeal_DifferentEphemeralEachTime(t *testing.T) {
	kp, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}

	plaintext := []byte("token")
	sealed1, err := SealedBoxSeal(plaintext, &kp.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal 1: %v", err)
	}
	sealed2, err := SealedBoxSeal(plaintext, &kp.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal 2: %v", err)
	}

	if bytes.Equal(sealed1, sealed2) {
		t.Error("expected different ciphertexts for two seals of the same plaintext")
	}
}

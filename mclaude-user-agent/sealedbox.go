// Package main implements NaCl crypto_box_seal / crypto_box_seal_open
// (libsodium anonymous sealed box) using golang.org/x/crypto/nacl/box.
//
// The sealed box format is:
//
//	ephemeral_pk (32 bytes) || box(message, nonce, recipient_pub, ephemeral_priv)
//
// where nonce = Blake2b-512(ephemeralPK || recipientPK)[:24].
//
// Reference: https://doc.libsodium.org/public-key_cryptography/sealed_boxes
package main

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

// SealedBoxOpen unseals a NaCl sealed box encrypted with the recipient's X25519 public key.
// The sealed box format: ephemeralPK (32 bytes) || NaCl box ciphertext.
// The nonce is derived from Blake2b-512(ephemeralPK || recipientPK) truncated to 24 bytes,
// matching libsodium's crypto_box_seal implementation.
func SealedBoxOpen(sealed []byte, recipientPub, recipientPriv *[32]byte) ([]byte, error) {
	if len(sealed) < 32+box.Overhead {
		return nil, fmt.Errorf("sealed box too short (%d bytes)", len(sealed))
	}

	var ephemeralPub [32]byte
	copy(ephemeralPub[:], sealed[:32])
	ciphertext := sealed[32:]

	// Derive nonce: first 24 bytes of Blake2b-512(ephemeralPK || recipientPK).
	nonce := sealedBoxNonce(&ephemeralPub, recipientPub)

	// Decrypt with the recipient's private key and the ephemeral public key.
	plaintext, ok := box.Open(nil, ciphertext, nonce, &ephemeralPub, recipientPriv)
	if !ok {
		return nil, fmt.Errorf("box.Open failed: authentication failed or wrong key")
	}
	return plaintext, nil
}

// SealedBoxSeal encrypts plaintext for the recipient's X25519 public key (sealed box).
// Returns the sealed box: ephemeralPK (32 bytes) || NaCl box ciphertext.
// Used in tests to produce valid sealed boxes for unit-testing the Open path.
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

// sealedBoxNonce computes the 24-byte nonce for a sealed box per libsodium's
// crypto_box_seal specification: Blake2b-512(ephemeralPK || recipientPK)[:24].
func sealedBoxNonce(ephemeralPub, recipientPub *[32]byte) *[24]byte {
	h, _ := blake2b.New512(nil)
	h.Write(ephemeralPub[:])
	h.Write(recipientPub[:])
	sum := h.Sum(nil)
	var nonce [24]byte
	copy(nonce[:], sum[:24])
	return &nonce
}

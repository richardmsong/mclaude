package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	agentcrypto "mclaude-user-agent/crypto"
)

// newTestTokenManager builds a TokenManager backed by a fake k8s client.
// The user-tokens-testuser Secret is pre-created (empty).
func newTestTokenManager(t *testing.T) (*TokenManager, *agentcrypto.X25519Keypair, *fake.Clientset) {
	t.Helper()
	kp, err := agentcrypto.GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}

	fakeK8s := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-tokens-testuser",
			Namespace: "mclaude-testuser",
		},
		Data: map[string][]byte{},
	})

	mgr := NewTokenManager(fakeK8s, "mclaude-testuser", "testuser", kp, zerolog.Nop())
	return mgr, kp, fakeK8s
}

// TestHandleDelivery_Success verifies the full token-delivery happy path:
// seal a token, deliver the wrapped payload, verify Secret is patched.
func TestHandleDelivery_Success(t *testing.T) {
	mgr, kp, fakeK8s := newTestTokenManager(t)

	token := []byte("sk-ant-api03-mytoken12345")
	sealed, err := agentcrypto.SealedBoxSeal(token, &kp.Public)
	if err != nil {
		t.Fatalf("SealedBoxSeal: %v", err)
	}

	payload, _ := json.Marshal(TokenDeliveryRequest{
		Provider:     "anthropic",
		TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
	})

	reply := mgr.HandleDelivery(context.Background(), payload)

	var resp TokenDeliveryReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true, got error=%q", resp.Error)
	}

	// Verify the Secret was patched with the plaintext token.
	secret, err := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-tokens-testuser", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Secret: %v", err)
	}
	stored := secret.Data["anthropic"]
	if string(stored) != string(token) {
		t.Errorf("stored token = %q, want %q", stored, token)
	}
}

// TestHandleDelivery_DecryptFailed verifies that a garbled sealed box returns
// {ok: false, error: "decrypt_failed"} and does NOT write to the Secret.
func TestHandleDelivery_DecryptFailed(t *testing.T) {
	mgr, _, fakeK8s := newTestTokenManager(t)

	// Build a payload with garbage ciphertext (wrong key).
	wrongKP, _ := agentcrypto.GenerateX25519Keypair()
	token := []byte("secret")
	sealed, _ := agentcrypto.SealedBoxSeal(token, &wrongKP.Public) // sealed for wrong recipient

	payload, _ := json.Marshal(TokenDeliveryRequest{
		Provider:     "anthropic",
		TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
	})

	reply := mgr.HandleDelivery(context.Background(), payload)

	var resp TokenDeliveryReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp.OK {
		t.Error("expected ok=false for wrong-key delivery, got ok=true")
	}
	if resp.Error != "decrypt_failed" {
		t.Errorf("expected error=decrypt_failed, got %q", resp.Error)
	}

	// Secret should be unchanged (no provider key written).
	secret, _ := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-tokens-testuser", metav1.GetOptions{},
	)
	if len(secret.Data["anthropic"]) != 0 {
		t.Error("Secret was written despite decrypt failure")
	}
}

// TestHandleDelivery_DefaultProvider verifies that a missing "provider" field
// defaults to "anthropic".
func TestHandleDelivery_DefaultProvider(t *testing.T) {
	mgr, kp, fakeK8s := newTestTokenManager(t)

	token := []byte("mytoken")
	sealed, _ := agentcrypto.SealedBoxSeal(token, &kp.Public)

	// Omit "provider" in payload.
	payload, _ := json.Marshal(map[string]string{
		"token_wrapped": base64.StdEncoding.EncodeToString(sealed),
	})

	reply := mgr.HandleDelivery(context.Background(), payload)
	var resp TokenDeliveryReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true, got error=%q", resp.Error)
	}

	secret, _ := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-tokens-testuser", metav1.GetOptions{},
	)
	if string(secret.Data["anthropic"]) != string(token) {
		t.Errorf("expected anthropic key in Secret; got %q", secret.Data["anthropic"])
	}
}

// TestHandleDelivery_MultiProvider verifies that multiple providers can be written
// independently to the same Secret.
func TestHandleDelivery_MultiProvider(t *testing.T) {
	mgr, kp, fakeK8s := newTestTokenManager(t)

	deliverToken := func(provider, token string) {
		t.Helper()
		sealed, _ := agentcrypto.SealedBoxSeal([]byte(token), &kp.Public)
		payload, _ := json.Marshal(TokenDeliveryRequest{
			Provider:     provider,
			TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
		})
		reply := mgr.HandleDelivery(context.Background(), payload)
		var resp TokenDeliveryReply
		if err := json.Unmarshal(reply, &resp); err != nil || !resp.OK {
			t.Fatalf("deliver %s failed: %v / %q", provider, err, resp.Error)
		}
	}

	deliverToken("anthropic", "anthropic-token")
	deliverToken("google", "google-token")

	secret, _ := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-tokens-testuser", metav1.GetOptions{},
	)
	if string(secret.Data["anthropic"]) != "anthropic-token" {
		t.Errorf("anthropic token = %q", secret.Data["anthropic"])
	}
	if string(secret.Data["google"]) != "google-token" {
		t.Errorf("google token = %q", secret.Data["google"])
	}
}

// TestHandleDelivery_BadPayload verifies that malformed JSON returns decrypt_failed.
func TestHandleDelivery_BadPayload(t *testing.T) {
	mgr, _, _ := newTestTokenManager(t)

	reply := mgr.HandleDelivery(context.Background(), []byte("{invalid json"))
	var resp TokenDeliveryReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp.OK {
		t.Error("expected ok=false for bad JSON payload")
	}
}

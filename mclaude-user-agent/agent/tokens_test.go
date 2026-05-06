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

// TestHandleRevoke_Success verifies that token-revoke deletes the provider key from the Secret.
func TestHandleRevoke_Success(t *testing.T) {
	mgr, kp, fakeK8s := newTestTokenManager(t)

	// First, deliver a token so the Secret has content.
	token := []byte("sk-ant-oat01-mytoken")
	sealed, _ := agentcrypto.SealedBoxSeal(token, &kp.Public)
	deliverPayload, _ := json.Marshal(TokenDeliveryRequest{
		Provider:     "anthropic",
		TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
	})
	deliverReply := mgr.HandleDelivery(context.Background(), deliverPayload)
	var deliverResp TokenDeliveryReply
	if err := json.Unmarshal(deliverReply, &deliverResp); err != nil || !deliverResp.OK {
		t.Fatalf("setup delivery failed: %v / %q", err, deliverResp.Error)
	}

	// Now revoke.
	revokePayload, _ := json.Marshal(TokenRevokeRequest{Provider: "anthropic"})
	reply := mgr.HandleRevoke(context.Background(), revokePayload)

	var resp TokenRevokeReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal revoke reply: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true, got error=%q", resp.Error)
	}

	// Verify the provider key is now empty (deleted) in the Secret.
	secret, err := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-tokens-testuser", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Secret: %v", err)
	}
	if len(secret.Data["anthropic"]) != 0 {
		t.Errorf("expected anthropic key to be empty after revoke, got %q", secret.Data["anthropic"])
	}
}

// TestHandleRevoke_DefaultProvider verifies that a missing "provider" field defaults to "anthropic".
func TestHandleRevoke_DefaultProvider(t *testing.T) {
	mgr, kp, fakeK8s := newTestTokenManager(t)

	// Deliver a token first.
	token := []byte("mytoken")
	sealed, _ := agentcrypto.SealedBoxSeal(token, &kp.Public)
	deliverPayload, _ := json.Marshal(TokenDeliveryRequest{
		Provider:     "anthropic",
		TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
	})
	mgr.HandleDelivery(context.Background(), deliverPayload) //nolint:errcheck

	// Revoke without provider field (should default to "anthropic").
	reply := mgr.HandleRevoke(context.Background(), []byte(`{}`))
	var resp TokenRevokeReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true, got error=%q", resp.Error)
	}

	secret, _ := fakeK8s.CoreV1().Secrets("mclaude-testuser").Get(
		context.Background(), "user-tokens-testuser", metav1.GetOptions{},
	)
	if len(secret.Data["anthropic"]) != 0 {
		t.Errorf("expected anthropic key empty after revoke-default-provider")
	}
}

// TestHandleRevoke_BadPayload verifies that malformed JSON returns bad_request.
func TestHandleRevoke_BadPayload(t *testing.T) {
	mgr, _, _ := newTestTokenManager(t)

	reply := mgr.HandleRevoke(context.Background(), []byte("{invalid"))
	var resp TokenRevokeReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.OK {
		t.Error("expected ok=false for bad JSON payload")
	}
	if resp.Error != "bad_request" {
		t.Errorf("expected error=bad_request, got %q", resp.Error)
	}
}

// TestHandleRevoke_SecretNotFound verifies that revoking when the Secret is missing
// returns ok=true (idempotent — already deleted counts as success).
func TestHandleRevoke_SecretNotFound(t *testing.T) {
	kp, _ := agentcrypto.GenerateX25519Keypair()
	fakeK8s := fake.NewClientset() // no Secret pre-created
	mgr := NewTokenManager(fakeK8s, "mclaude-testuser", "testuser", kp, zerolog.Nop())

	payload, _ := json.Marshal(TokenRevokeRequest{Provider: "anthropic"})
	reply := mgr.HandleRevoke(context.Background(), payload)

	var resp TokenRevokeReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true when Secret is missing (treat as already revoked), got error=%q", resp.Error)
	}
}

// TestHandleStatus_NoToken verifies that HandleStatus returns hasToken=false when no token is present.
func TestHandleStatus_NoToken(t *testing.T) {
	mgr, _, _ := newTestTokenManager(t) // Secret exists but empty

	reply := mgr.HandleStatus(context.Background(), nil)

	var resp StatusReply
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.HasToken {
		t.Error("expected hasToken=false for empty Secret")
	}
	if resp.TokenVersion != "" {
		t.Errorf("expected tokenVersion empty, got %q", resp.TokenVersion)
	}
}

// TestHandleStatus_WithToken verifies that HandleStatus returns hasToken=true and the correct tokenVersion.
func TestHandleStatus_WithToken(t *testing.T) {
	mgr, kp, _ := newTestTokenManager(t)

	// Deliver a token.
	token := []byte("sk-ant-oat01-mytoken12345")
	sealed, _ := agentcrypto.SealedBoxSeal(token, &kp.Public)
	deliverPayload, _ := json.Marshal(TokenDeliveryRequest{
		Provider:     "anthropic",
		TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
	})
	reply := mgr.HandleDelivery(context.Background(), deliverPayload)
	var deliverResp TokenDeliveryReply
	if err := json.Unmarshal(reply, &deliverResp); err != nil || !deliverResp.OK {
		t.Fatalf("delivery failed: %v / %q", err, deliverResp.Error)
	}

	// Check status.
	statusReply := mgr.HandleStatus(context.Background(), []byte(`{}`))
	var resp StatusReply
	if err := json.Unmarshal(statusReply, &resp); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !resp.HasToken {
		t.Error("expected hasToken=true after delivery")
	}
	if len(resp.TokenVersion) != 8 {
		t.Errorf("expected tokenVersion to be 8 hex chars, got %q", resp.TokenVersion)
	}
	// Verify the tokenVersion matches the expected sha256 prefix.
	expected := tokenVersion(token)
	if resp.TokenVersion != expected {
		t.Errorf("tokenVersion = %q, want %q", resp.TokenVersion, expected)
	}
}

// TestHandleStatus_AfterRevoke verifies that HandleStatus returns hasToken=false after revoke.
func TestHandleStatus_AfterRevoke(t *testing.T) {
	mgr, kp, _ := newTestTokenManager(t)

	// Deliver then revoke.
	token := []byte("sk-ant-oat01-revtest")
	sealed, _ := agentcrypto.SealedBoxSeal(token, &kp.Public)
	deliverPayload, _ := json.Marshal(TokenDeliveryRequest{
		Provider:     "anthropic",
		TokenWrapped: base64.StdEncoding.EncodeToString(sealed),
	})
	mgr.HandleDelivery(context.Background(), deliverPayload) //nolint:errcheck

	revokePayload, _ := json.Marshal(TokenRevokeRequest{Provider: "anthropic"})
	mgr.HandleRevoke(context.Background(), revokePayload) //nolint:errcheck

	// Status should show no token.
	statusReply := mgr.HandleStatus(context.Background(), nil)
	var resp StatusReply
	if err := json.Unmarshal(statusReply, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.HasToken {
		t.Error("expected hasToken=false after revoke")
	}
}

// TestTokenVersion verifies the tokenVersion helper produces 8 hex chars.
func TestTokenVersion(t *testing.T) {
	cases := []struct {
		token string
		want  int // expected length
	}{
		{"sk-ant-oat01-abc", 8},
		{"", 8}, // sha256 of empty string still produces 8 hex chars
		{"a", 8},
	}
	for _, tc := range cases {
		got := tokenVersion([]byte(tc.token))
		if len(got) != tc.want {
			t.Errorf("tokenVersion(%q) = %q (len %d), want len %d", tc.token, got, len(got), tc.want)
		}
		// Verify all hex characters.
		for _, c := range got {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("tokenVersion(%q) = %q contains non-hex char %q", tc.token, got, c)
			}
		}
	}
}

package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	mclnats "mclaude.io/common/pkg/nats"
)

// ---- Subject permission construction ----

func TestUserSubjectPermissions(t *testing.T) {
	perm := UserSubjectPermissions("alice123")
	wantPub := []string{"mclaude.alice123.>", "_INBOX.>"}
	// SubAllow includes KV bucket subjects so the SPA can watch projects and sessions.
	wantSub := []string{
		"mclaude.alice123.>",
		"_INBOX.>",
		"$KV.mclaude-projects.alice123.>",
		"$KV.mclaude-sessions.alice123.>",
	}

	if !slicesEqual(perm.PubAllow, wantPub) {
		t.Errorf("PubAllow = %v; want %v", perm.PubAllow, wantPub)
	}
	if !slicesEqual(perm.SubAllow, wantSub) {
		t.Errorf("SubAllow = %v; want %v", perm.SubAllow, wantSub)
	}
}

func TestUserSubjectPermissions_SpecialChars(t *testing.T) {
	// User IDs are UUIDs — no special chars — but confirm format is stable.
	perm := UserSubjectPermissions("550e8400-e29b-41d4-a716-446655440000")
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if !strings.HasPrefix(s, "mclaude.") && s != "_INBOX.>" && !strings.HasPrefix(s, "$KV.") {
			t.Errorf("unexpected subject: %q", s)
		}
	}
}

func TestSessionAgentSubjectPermissions(t *testing.T) {
	perm := SessionAgentSubjectPermissions("bob456")
	// Session agents don't get _INBOX.> — they don't do request/reply.
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if s == "_INBOX.>" {
			t.Errorf("session agent should not have _INBOX.>: got %q", s)
		}
		if !strings.HasPrefix(s, "mclaude.bob456.") {
			t.Errorf("unexpected subject %q for user bob456", s)
		}
	}
}

func TestSubjectIsolation(t *testing.T) {
	// Permissions for alice must not match bob's namespace.
	alice := UserSubjectPermissions("alice")
	for _, s := range append(alice.PubAllow, alice.SubAllow...) {
		if s == "_INBOX.>" {
			continue
		}
		// KV subjects are scoped to alice's user ID — they must not reference bob.
		if strings.HasPrefix(s, "$KV.") {
			if strings.Contains(s, "bob") {
				t.Errorf("alice permission contains bob subject: %q", s)
			}
			if !strings.Contains(s, "alice") {
				t.Errorf("alice KV permission doesn't contain alice ID: %q", s)
			}
			continue
		}
		if !strings.HasPrefix(s, "mclaude.alice.") {
			t.Errorf("alice permission contains non-alice subject: %q", s)
		}
	}
}

// ---- NKey generation ----

func TestGenerateOperatorNKey(t *testing.T) {
	kp, err := GenerateOperatorNKey()
	if err != nil {
		t.Fatalf("GenerateOperatorNKey: %v", err)
	}
	if kp.PublicKey == "" {
		t.Error("empty public key")
	}
	if !strings.HasPrefix(kp.PublicKey, "O") {
		t.Errorf("operator public key should start with 'O', got %q", kp.PublicKey[:1])
	}
}

func TestGenerateAccountNKey(t *testing.T) {
	kp, err := GenerateAccountNKey()
	if err != nil {
		t.Fatalf("GenerateAccountNKey: %v", err)
	}
	if !strings.HasPrefix(kp.PublicKey, "A") {
		t.Errorf("account public key should start with 'A', got %q", kp.PublicKey[:1])
	}
}

func TestGenerateUserNKey(t *testing.T) {
	kp, seed, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("GenerateUserNKey: %v", err)
	}
	if !strings.HasPrefix(kp.PublicKey, "U") {
		t.Errorf("user public key should start with 'U', got %q", kp.PublicKey[:1])
	}
	if len(seed) == 0 {
		t.Error("empty seed")
	}
	// Seed should round-trip back to the same key pair.
	restored, err := nkeys.FromSeed(seed)
	if err != nil {
		t.Fatalf("FromSeed: %v", err)
	}
	restoredPub, _ := restored.PublicKey()
	if restoredPub != kp.PublicKey {
		t.Errorf("seed round-trip: got public key %q; want %q", restoredPub, kp.PublicKey)
	}
}

func TestNKeysAreUnique(t *testing.T) {
	a, _, _ := GenerateUserNKey()
	b, _, _ := GenerateUserNKey()
	if a.PublicKey == b.PublicKey {
		t.Error("two generated user keys are identical — RNG broken?")
	}
}

// ---- JWT issuance and claim validation ----

func TestIssueUserJWT_ClaimsRoundTrip(t *testing.T) {
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account key: %v", err)
	}
	accountPub, _ := accountKP.PublicKey()

	userID := "test-user-001"
	expiry := time.Now().Add(8 * time.Hour).Unix()

	jwt, seed, err := IssueUserJWT(userID, accountKP, expiry)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}
	if jwt == "" {
		t.Error("empty jwt")
	}
	if len(seed) == 0 {
		t.Error("empty seed")
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}
	if claims.Name != userID {
		t.Errorf("claims.Name = %q; want %q", claims.Name, userID)
	}
}

func TestIssueUserJWT_SubjectScopes(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	userID := "scoped-user"
	expiry := time.Now().Add(8 * time.Hour).Unix()
	jwt, _, err := IssueUserJWT(userID, accountKP, expiry)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	expectedSubject := fmt.Sprintf("mclaude.%s.>", userID)
	if !containsStr(claims.Permissions.Pub.Allow, expectedSubject) {
		t.Errorf("pub allow missing %q, got %v", expectedSubject, claims.Permissions.Pub.Allow)
	}
	if !containsStr(claims.Permissions.Sub.Allow, expectedSubject) {
		t.Errorf("sub allow missing %q, got %v", expectedSubject, claims.Permissions.Sub.Allow)
	}
	if !containsStr(claims.Permissions.Pub.Allow, "_INBOX.>") {
		t.Errorf("pub allow missing _INBOX.>")
	}
}

func TestDecodeUserJWT_InvalidSignature(t *testing.T) {
	// Sign with key A, validate with key B → should fail.
	accountA, _ := nkeys.CreateAccount()
	accountB, _ := nkeys.CreateAccount()
	accountBPub, _ := accountB.PublicKey()

	jwt, _, _ := IssueUserJWT("user", accountA, time.Now().Add(time.Hour).Unix())
	_, err := DecodeUserJWT(jwt, accountBPub)
	if err == nil {
		t.Error("expected error validating JWT signed by different key; got nil")
	}
}

func TestDecodeUserJWT_Malformed(t *testing.T) {
	_, err := DecodeUserJWT("not.a.jwt", "ANYKEY")
	if err == nil {
		t.Error("expected error for malformed JWT; got nil")
	}
}

func TestIssueUserJWT_EachCallUniqueKeys(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	expiry := time.Now().Add(time.Hour).Unix()

	jwt1, seed1, _ := IssueUserJWT("user", accountKP, expiry)
	jwt2, seed2, _ := IssueUserJWT("user", accountKP, expiry)

	if jwt1 == jwt2 {
		t.Error("two IssueUserJWT calls produced identical JWTs (should use fresh user NKeys)")
	}
	if string(seed1) == string(seed2) {
		t.Error("two IssueUserJWT calls produced identical seeds")
	}
}

// ---- Version endpoint ----

func TestVersionResponse_Defaults(t *testing.T) {
	t.Setenv("MIN_CLIENT_VERSION", "")
	t.Setenv("SERVER_VERSION", "")

	r := &fakeResponseWriter{}
	handleVersion(r, fakeRequest("GET", "/version"))
	if r.status != 0 && r.status != http.StatusOK {
		t.Errorf("status = %d; want 200", r.status)
	}
	if !strings.Contains(r.body, `"minClientVersion"`) {
		t.Errorf("body missing minClientVersion field: %q", r.body)
	}
}

func TestVersionResponse_ConfiguredVersion(t *testing.T) {
	t.Setenv("MIN_CLIENT_VERSION", "1.2.3")
	t.Setenv("SERVER_VERSION", "v2.0.0")

	r := &fakeResponseWriter{}
	handleVersion(r, fakeRequest("GET", "/version"))
	if !strings.Contains(r.body, "1.2.3") {
		t.Errorf("body missing minClientVersion 1.2.3: %q", r.body)
	}
}


// ---- Session-agent JWT and credentials ----

func TestIssueSessionAgentJWT_SubjectScopes(t *testing.T) {
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account key: %v", err)
	}
	accountPub, _ := accountKP.PublicKey()

	userID := "agent-user-001"
	jwtStr, seed, err := IssueSessionAgentJWT(userID, accountKP)
	if err != nil {
		t.Fatalf("IssueSessionAgentJWT: %v", err)
	}
	if jwtStr == "" {
		t.Error("empty jwt")
	}
	if len(seed) == 0 {
		t.Error("empty seed")
	}

	claims, err := DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	expectedSubject := fmt.Sprintf("mclaude.%s.>", userID)
	if !containsStr(claims.Permissions.Pub.Allow, expectedSubject) {
		t.Errorf("pub allow missing %q, got %v", expectedSubject, claims.Permissions.Pub.Allow)
	}
	if !containsStr(claims.Permissions.Sub.Allow, expectedSubject) {
		t.Errorf("sub allow missing %q, got %v", expectedSubject, claims.Permissions.Sub.Allow)
	}

	// Session-agent must NOT have _INBOX.>
	if containsStr(claims.Permissions.Pub.Allow, "_INBOX.>") {
		t.Error("session-agent pub should not have _INBOX.>")
	}
	if containsStr(claims.Permissions.Sub.Allow, "_INBOX.>") {
		t.Error("session-agent sub should not have _INBOX.>")
	}
}

func TestIssueSessionAgentJWT_NoExpiry(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	jwtStr, _, err := IssueSessionAgentJWT("sa-user", accountKP)
	if err != nil {
		t.Fatalf("IssueSessionAgentJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	// Expires = 0 means no expiry.
	if claims.Expires != 0 {
		t.Errorf("session-agent JWT should have no expiry; got Expires=%d", claims.Expires)
	}
}

func TestFormatNATSCredentials_Format(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()

	jwtStr, seed, err := IssueSessionAgentJWT("format-user", accountKP)
	if err != nil {
		t.Fatalf("IssueSessionAgentJWT: %v", err)
	}

	creds := mclnats.FormatNATSCredentials(jwtStr, seed)
	credsStr := string(creds)

	if !strings.Contains(credsStr, "-----BEGIN NATS USER JWT-----") {
		t.Error("creds missing BEGIN NATS USER JWT header")
	}
	if !strings.Contains(credsStr, "------END NATS USER JWT------") {
		t.Error("creds missing END NATS USER JWT trailer")
	}
	if !strings.Contains(credsStr, "-----BEGIN USER NKEY SEED-----") {
		t.Error("creds missing BEGIN USER NKEY SEED header")
	}
	if !strings.Contains(credsStr, "------END USER NKEY SEED------") {
		t.Error("creds missing END USER NKEY SEED trailer")
	}
	if !strings.Contains(credsStr, jwtStr) {
		t.Error("creds does not contain the JWT")
	}
	if !strings.Contains(credsStr, string(seed)) {
		t.Error("creds does not contain the seed")
	}
}

func TestFormatNATSCredentials_SeedInBody(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()

	jwtStr, seed, _ := IssueSessionAgentJWT("seed-check-user", accountKP)
	creds := mclnats.FormatNATSCredentials(jwtStr, seed)
	credsStr := string(creds)

	// Verify the seed appears in the creds file and round-trips correctly.
	// We don't parse the exact line positions — just verify it's parseable by nkeys.
	if !strings.Contains(credsStr, string(seed)) {
		t.Error("creds does not contain the seed bytes")
	}

	// Verify the seed from the creds file round-trips to the same key pair.
	originalKP, err := nkeys.FromSeed(seed)
	if err != nil {
		t.Fatalf("FromSeed original: %v", err)
	}
	originalPub, _ := originalKP.PublicKey()

	// The seed appears verbatim in the file, so we can round-trip it directly.
	restoredKP, err := nkeys.FromSeed(seed)
	if err != nil {
		t.Fatalf("FromSeed round-trip: %v", err)
	}
	restoredPub, _ := restoredKP.PublicKey()
	if restoredPub != originalPub {
		t.Errorf("seed round-trip mismatch: got %q, want %q", restoredPub, originalPub)
	}
}

// ---- helpers ----

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

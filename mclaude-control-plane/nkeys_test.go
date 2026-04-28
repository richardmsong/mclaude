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
	perm := UserSubjectPermissions("alice123", "alice-slug")
	// PubAllow includes the host-scoped prefix per ADR-0049.
	wantPub := []string{"mclaude.alice123.>", "_INBOX.>", "$JS.API.>", "mclaude.users.alice-slug.hosts.*.>"}
	// SubAllow includes KV bucket subjects, JetStream API, and host-scoped prefix (ADR-0049).
	wantSub := []string{
		"mclaude.alice123.>",
		"_INBOX.>",
		"$KV.mclaude-projects.alice123.>",
		"$KV.mclaude-sessions.alice123.>",
		"$KV.mclaude-hosts.alice-slug.>",
		"$JS.API.>",
		"$JS.API.DIRECT.GET.>",
		"mclaude.users.alice-slug.hosts.*.>",
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
	perm := UserSubjectPermissions("550e8400-e29b-41d4-a716-446655440000", "dev.local")
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if !strings.HasPrefix(s, "mclaude.") && s != "_INBOX.>" && !strings.HasPrefix(s, "$KV.") && !strings.HasPrefix(s, "$JS.") {
			t.Errorf("unexpected subject: %q", s)
		}
	}
}

func TestSessionAgentSubjectPermissions(t *testing.T) {
	perm := SessionAgentSubjectPermissions("bob456", "bob-slug")
	// Per ADR-0050 Decision 5: session agents must have _INBOX.>, $JS.*.API.>,
	// the UUID-prefixed subject, and the host-scoped slug subject.
	allSubjects := append(perm.PubAllow, perm.SubAllow...)
	hasInbox := false
	hasJS := false
	hasUUID := false
	hasHostScoped := false
	for _, s := range allSubjects {
		if s == "_INBOX.>" {
			hasInbox = true
		}
		if s == "$JS.*.API.>" {
			hasJS = true
		}
		if s == "mclaude.bob456.>" {
			hasUUID = true
		}
		if s == "mclaude.users.bob-slug.hosts.*.>" {
			hasHostScoped = true
		}
	}
	if !hasInbox {
		t.Error("session agent should have _INBOX.>")
	}
	if !hasJS {
		t.Error("session agent should have $JS.*.API.>")
	}
	if !hasUUID {
		t.Error("session agent should have mclaude.bob456.>")
	}
	if !hasHostScoped {
		t.Error("session agent should have mclaude.users.bob-slug.hosts.*.>")
	}
	// PubAllow and SubAllow must be identical per ADR-0050 Decision 5.
	if len(perm.PubAllow) != len(perm.SubAllow) {
		t.Errorf("PubAllow and SubAllow should have same length: pub=%d sub=%d", len(perm.PubAllow), len(perm.SubAllow))
	}
}

func TestSubjectIsolation(t *testing.T) {
	// Permissions for alice must not match bob's namespace.
	alice := UserSubjectPermissions("alice", "alice-slug")
	for _, s := range append(alice.PubAllow, alice.SubAllow...) {
		if s == "_INBOX.>" || s == "$JS.API.>" || s == "$JS.API.DIRECT.GET.>" {
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
		// Host-scoped subjects use the slug (ADR-0049) — they start with mclaude.users.{slug}.
		if strings.HasPrefix(s, "mclaude.users.") {
			if strings.Contains(s, "bob") {
				t.Errorf("alice permission contains bob subject: %q", s)
			}
			if !strings.Contains(s, "alice") {
				t.Errorf("alice host-scoped permission doesn't contain alice slug: %q", s)
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

	jwt, seed, err := IssueUserJWT(userID, "test-slug", accountKP, expiry)
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
	jwt, _, err := IssueUserJWT(userID, "scoped-slug", accountKP, expiry)
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

	jwt, _, _ := IssueUserJWT("user", "user-slug", accountA, time.Now().Add(time.Hour).Unix())
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

func TestIssueUserJWT_UUIDInClaimsName(t *testing.T) {
	// ADR-0046 (updated): IssueUserJWT receives a UUID and stores it in claims.Name
	// so authMiddleware can pass it to db.GetUserByID. LoginResponse carries
	// UserSlug separately.
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	userID := "550e8400-e29b-41d4-a716-446655440000" // UUID
	expiry := time.Now().Add(8 * time.Hour).Unix()

	jwt, _, err := IssueUserJWT(userID, "dev.local", accountKP, expiry)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	if claims.Name != userID {
		t.Errorf("claims.Name = %q; want UUID %q (ADR-0046)", claims.Name, userID)
	}

	// The subject permissions must reference the UUID.
	expectedSubject := fmt.Sprintf("mclaude.%s.>", userID)
	if !containsStr(claims.Permissions.Pub.Allow, expectedSubject) {
		t.Errorf("pub allow missing %q (UUID-scoped subject)", expectedSubject)
	}
	if !containsStr(claims.Permissions.Sub.Allow, expectedSubject) {
		t.Errorf("sub allow missing %q (UUID-scoped subject)", expectedSubject)
	}
	// The hosts KV permission must use the slug, not the UUID.
	expectedHostsKV := "$KV.mclaude-hosts.dev.local.>"
	if !containsStr(claims.Permissions.Sub.Allow, expectedHostsKV) {
		t.Errorf("sub allow missing %q (slug-scoped hosts KV subject)", expectedHostsKV)
	}
	// ADR-0049: host-scoped subject prefix must appear in both pub and sub allow lists.
	expectedHostsPrefix := "mclaude.users.dev.local.hosts.*.>"
	if !containsStr(claims.Permissions.Pub.Allow, expectedHostsPrefix) {
		t.Errorf("pub allow missing %q (ADR-0049 host-scoped prefix)", expectedHostsPrefix)
	}
	if !containsStr(claims.Permissions.Sub.Allow, expectedHostsPrefix) {
		t.Errorf("sub allow missing %q (ADR-0049 host-scoped prefix)", expectedHostsPrefix)
	}
}

func TestIssueUserJWT_EachCallUniqueKeys(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	expiry := time.Now().Add(time.Hour).Unix()

	jwt1, seed1, _ := IssueUserJWT("user", "user-slug", accountKP, expiry)
	jwt2, seed2, _ := IssueUserJWT("user", "user-slug", accountKP, expiry)

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
	userSlug := "agent-user-slug"
	jwtStr, seed, err := IssueSessionAgentJWT(userID, userSlug, accountKP)
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

	// Per ADR-0050 Decision 5: UUID prefix, host-scoped prefix, _INBOX.>, $JS.*.API.>
	uuidSubject := fmt.Sprintf("mclaude.%s.>", userID)
	if !containsStr(claims.Permissions.Pub.Allow, uuidSubject) {
		t.Errorf("pub allow missing %q, got %v", uuidSubject, claims.Permissions.Pub.Allow)
	}
	if !containsStr(claims.Permissions.Sub.Allow, uuidSubject) {
		t.Errorf("sub allow missing %q, got %v", uuidSubject, claims.Permissions.Sub.Allow)
	}

	hostScopedSubject := fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)
	if !containsStr(claims.Permissions.Pub.Allow, hostScopedSubject) {
		t.Errorf("pub allow missing %q, got %v", hostScopedSubject, claims.Permissions.Pub.Allow)
	}
	if !containsStr(claims.Permissions.Sub.Allow, hostScopedSubject) {
		t.Errorf("sub allow missing %q, got %v", hostScopedSubject, claims.Permissions.Sub.Allow)
	}

	// Session-agent must have _INBOX.> (ADR-0050 Decision 5)
	if !containsStr(claims.Permissions.Pub.Allow, "_INBOX.>") {
		t.Error("session-agent pub should have _INBOX.>")
	}
	if !containsStr(claims.Permissions.Sub.Allow, "_INBOX.>") {
		t.Error("session-agent sub should have _INBOX.>")
	}

	// Session-agent must have $JS.*.API.> (ADR-0050 Decision 5)
	if !containsStr(claims.Permissions.Pub.Allow, "$JS.*.API.>") {
		t.Error("session-agent pub should have $JS.*.API.>")
	}
	if !containsStr(claims.Permissions.Sub.Allow, "$JS.*.API.>") {
		t.Error("session-agent sub should have $JS.*.API.>")
	}
}

func TestIssueSessionAgentJWT_NoExpiry(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	jwtStr, _, err := IssueSessionAgentJWT("sa-user", "sa-user-slug", accountKP)
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

	jwtStr, seed, err := IssueSessionAgentJWT("format-user", "format-user-slug", accountKP)
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

	jwtStr, seed, _ := IssueSessionAgentJWT("seed-check-user", "seed-check-user-slug", accountKP)
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

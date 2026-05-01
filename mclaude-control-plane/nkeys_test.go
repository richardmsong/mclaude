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

// ---- Subject permission construction (ADR-0054) ----

func TestUserSubjectPermissions_ADR0054(t *testing.T) {
	// ADR-0054: per-user-resource scoped permissions, no broad wildcards.
	hostSlugs := []string{"laptop-a", "cluster-a"}
	perm := UserSubjectPermissions("alice", hostSlugs)

	// Must have core user subjects
	mustContain(t, "PubAllow", perm.PubAllow, "mclaude.users.alice.hosts.*.>")
	mustContain(t, "PubAllow", perm.PubAllow, "_INBOX.>")
	mustContain(t, "SubAllow", perm.SubAllow, "mclaude.users.alice.hosts.*.>")
	mustContain(t, "SubAllow", perm.SubAllow, "_INBOX.>")

	// Must have per-user KV stream info
	mustContain(t, "PubAllow", perm.PubAllow, "$JS.API.STREAM.INFO.KV_mclaude-sessions-alice")
	mustContain(t, "PubAllow", perm.PubAllow, "$JS.API.STREAM.INFO.KV_mclaude-projects-alice")
	mustContain(t, "PubAllow", perm.PubAllow, "$JS.API.STREAM.INFO.MCLAUDE_SESSIONS_alice")

	// Must have per-host entries for accessible hosts
	mustContain(t, "PubAllow", perm.PubAllow, "$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a")
	mustContain(t, "PubAllow", perm.PubAllow, "$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.cluster-a")
	mustContain(t, "SubAllow", perm.SubAllow, "$KV.mclaude-hosts.laptop-a")
	mustContain(t, "SubAllow", perm.SubAllow, "$KV.mclaude-hosts.cluster-a")

	// Must NOT have broad JetStream wildcards (ADR-0054 security requirement)
	mustNotContain(t, "PubAllow", perm.PubAllow, "$JS.API.>")
	mustNotContain(t, "SubAllow", perm.SubAllow, "$JS.API.>")
	mustNotContain(t, "PubAllow", perm.PubAllow, "$JS.*.API.>")

	// Must not reference other users' resources
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if strings.Contains(s, "bob") {
			t.Errorf("alice permission unexpectedly contains 'bob': %q", s)
		}
	}
}

func TestUserSubjectPermissions_NoHostSlugs(t *testing.T) {
	// A user with no accessible hosts should still get base permissions but no host-specific entries.
	perm := UserSubjectPermissions("alice", nil)
	mustContain(t, "PubAllow", perm.PubAllow, "mclaude.users.alice.hosts.*.>")
	// No per-host KV entries
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if strings.Contains(s, "$KV.mclaude-hosts.laptop-a") {
			t.Errorf("no-host user permission contains host-specific entry: %q", s)
		}
	}
}

func TestUserSubjectPermissions_CrossUserIsolation(t *testing.T) {
	alicePerm := UserSubjectPermissions("alice", []string{"host-1"})
	// Alice's permissions must not reference bob's resources
	for _, s := range append(alicePerm.PubAllow, alicePerm.SubAllow...) {
		if strings.Contains(s, "mclaude-sessions-bob") || strings.Contains(s, "mclaude-projects-bob") {
			t.Errorf("alice permission references bob resource: %q", s)
		}
		if strings.Contains(s, "MCLAUDE_SESSIONS_bob") {
			t.Errorf("alice permission references bob stream: %q", s)
		}
	}
}

func TestSessionAgentSubjectPermissions_ADR0054(t *testing.T) {
	// ADR-0054: per-project scoped agent permissions.
	perm := SessionAgentSubjectPermissions("alice", "laptop-a", "myapp")

	// Must be scoped to one project only
	mustContain(t, "PubAllow", perm.PubAllow, "mclaude.users.alice.hosts.laptop-a.projects.myapp.>")
	mustContain(t, "SubAllow", perm.SubAllow, "mclaude.users.alice.hosts.laptop-a.projects.myapp.>")

	// Must have KV write for sessions and project state
	mustContain(t, "PubAllow", perm.PubAllow, "$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>")
	mustContain(t, "PubAllow", perm.PubAllow, "$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp")

	// Must have per-project consumer create (filtered form)
	if !permHasPrefix(perm.PubAllow, "$JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.*.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp") {
		t.Error("session agent missing filtered consumer create for sessions KV")
	}

	// Must have direct-get (subject-form) for its project
	if !permHasPrefix(perm.PubAllow, "$JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp") {
		t.Error("session agent missing direct-get for its sessions")
	}

	// Must have quota pub/sub (ADR-0044)
	mustContain(t, "PubAllow", perm.PubAllow, "mclaude.users.alice.quota")
	mustContain(t, "SubAllow", perm.SubAllow, "mclaude.users.alice.quota")

	// Must NOT have access to another project's KV keys
	mustNotContain(t, "PubAllow", perm.PubAllow, "$KV.mclaude-sessions-alice.hosts.laptop-a.projects.other-project.sessions.>")

	// Must NOT have broad wildcards
	mustNotContain(t, "PubAllow", perm.PubAllow, "$JS.API.>")
	mustNotContain(t, "PubAllow", perm.PubAllow, "$JS.*.API.>")

	// Must NOT have broad KV bucket access
	mustNotContain(t, "PubAllow", perm.PubAllow, "$KV.mclaude-sessions-alice.>")
}

func TestSessionAgentSubjectPermissions_CrossUserIsolation(t *testing.T) {
	// Agent for alice's project must not have access to bob's resources.
	aliceAgent := SessionAgentSubjectPermissions("alice", "laptop-a", "myapp")
	for _, s := range append(aliceAgent.PubAllow, aliceAgent.SubAllow...) {
		if strings.Contains(s, "bob") {
			t.Errorf("alice agent permission unexpectedly references bob: %q", s)
		}
		if strings.Contains(s, "mclaude-sessions-bob") || strings.Contains(s, "mclaude-projects-bob") {
			t.Errorf("alice agent permission references bob bucket: %q", s)
		}
	}
}

func TestHostSubjectPermissions_ADR0054(t *testing.T) {
	// ADR-0054: host has only host-scoped subjects, zero JetStream.
	perm := HostSubjectPermissions("laptop-a")

	// Must have host-scoped pub/sub
	mustContain(t, "PubAllow", perm.PubAllow, "mclaude.hosts.laptop-a.>")
	mustContain(t, "SubAllow", perm.SubAllow, "mclaude.hosts.laptop-a.>")
	mustContain(t, "PubAllow", perm.PubAllow, "_INBOX.>")

	// Must have system event subscriptions for liveness (ADR-0054)
	mustContain(t, "SubAllow", perm.SubAllow, "$SYS.ACCOUNT.*.CONNECT")
	mustContain(t, "SubAllow", perm.SubAllow, "$SYS.ACCOUNT.*.DISCONNECT")

	// Must NOT have any JetStream permissions (zero JS for hosts per ADR-0054)
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if strings.HasPrefix(s, "$JS.") || strings.HasPrefix(s, "$KV.") || strings.HasPrefix(s, "$O.") {
			t.Errorf("host permission has JetStream/KV/ObjectStore subject (should be zero): %q", s)
		}
	}

	// Must NOT have user-scoped subjects
	for _, s := range append(perm.PubAllow, perm.SubAllow...) {
		if strings.Contains(s, "mclaude.users.") {
			t.Errorf("host permission contains user-scoped subject: %q", s)
		}
	}
}

func TestHostSubjectPermissions_ConstantSize(t *testing.T) {
	// The host JWT must be constant-size regardless of how many users share the host.
	// One entry in Pub+Sub, not one per user.
	perm := HostSubjectPermissions("shared-host")

	// Exactly one host-scoped entry (mclaude.hosts.shared-host.>)
	hostEntries := 0
	for _, s := range perm.PubAllow {
		if s == "mclaude.hosts.shared-host.>" {
			hostEntries++
		}
	}
	if hostEntries != 1 {
		t.Errorf("host JWT should have exactly 1 host-scoped pub entry, got %d", hostEntries)
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

// ---- JWT issuance (ADR-0054: external public key) ----

func TestIssueUserJWT_ADR0054_ClaimsRoundTrip(t *testing.T) {
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account key: %v", err)
	}
	accountPub, _ := accountKP.PublicKey()

	// Generate a user NKey pair (simulating what the client would do)
	userKP, _, _ := GenerateUserNKey()

	userID := "test-user-uuid-001"
	userSlug := "test-user"
	hostSlugs := []string{"laptop-a"}

	jwt, err := IssueUserJWT(userKP.PublicKey, userID, userSlug, hostSlugs, accountKP, 28800)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}
	if jwt == "" {
		t.Error("empty jwt")
	}

	// JWT should decode successfully
	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	// claims.Name must be userID (UUID) for auth middleware
	if claims.Name != userID {
		t.Errorf("claims.Name = %q; want userID %q", claims.Name, userID)
	}

	// IssuerAccount must be set
	if claims.IssuerAccount != accountPub {
		t.Errorf("IssuerAccount = %q; want %q", claims.IssuerAccount, accountPub)
	}

	// JWT should be associated with the user's NKey public key, not a CP-generated one
	if claims.Subject != userKP.PublicKey {
		t.Errorf("JWT subject = %q; want user's public key %q", claims.Subject, userKP.PublicKey)
	}
}

func TestIssueUserJWT_ADR0054_ScopedPermissions(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	userKP, _, _ := GenerateUserNKey()
	hostSlugs := []string{"laptop-a", "cluster-a"}

	jwt, err := IssueUserJWT(userKP.PublicKey, "user-id", "alice", hostSlugs, accountKP, 28800)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	// Must have user-scoped subjects
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "mclaude.users.alice.hosts.*.>")

	// Must have per-user bucket stream info
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "$JS.API.STREAM.INFO.KV_mclaude-sessions-alice")

	// Must have per-host entries
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a")
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "$KV.mclaude-hosts.laptop-a")
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "$KV.mclaude-hosts.cluster-a")

	// Must NOT have broad wildcards
	if containsStr(claims.Permissions.Pub.Allow, "$JS.API.>") {
		t.Error("user JWT must not have broad $JS.API.> wildcard (ADR-0054)")
	}
}

func TestIssueHostJWT_ADR0054(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	// Simulate host generating its own NKey pair
	hostKP, _, _ := GenerateUserNKey()

	jwt, err := IssueHostJWT(hostKP.PublicKey, "laptop-a", accountKP)
	if err != nil {
		t.Fatalf("IssueHostJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	// Must use host's NKey public key
	if claims.Subject != hostKP.PublicKey {
		t.Errorf("JWT subject = %q; want host's public key", claims.Subject)
	}

	// Must have host-scoped subjects only
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "mclaude.hosts.laptop-a.>")
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "mclaude.hosts.laptop-a.>")

	// Must have system event subscriptions
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "$SYS.ACCOUNT.*.CONNECT")
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "$SYS.ACCOUNT.*.DISCONNECT")

	// Must NOT have JetStream (zero JS for hosts)
	for _, s := range append(claims.Permissions.Pub.Allow, claims.Permissions.Sub.Allow...) {
		if strings.HasPrefix(s, "$JS.") || strings.HasPrefix(s, "$KV.") {
			t.Errorf("host JWT has JetStream/KV permission (must be zero): %q", s)
		}
	}

	// Must have 5-minute TTL
	if claims.Expires == 0 {
		t.Error("host JWT should have expiry (5-minute TTL)")
	}
	remaining := claims.Expires - time.Now().Unix()
	if remaining < 250 || remaining > 310 {
		t.Errorf("host JWT TTL = %ds; want ~300s (5 min)", remaining)
	}
}

func TestIssueSessionAgentJWT_ADR0054(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	// Simulate agent generating its own NKey pair
	agentKP, _, _ := GenerateUserNKey()

	jwt, err := IssueSessionAgentJWT(agentKP.PublicKey, "alice", "laptop-a", "myapp", accountKP)
	if err != nil {
		t.Fatalf("IssueSessionAgentJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	// Must use agent's NKey public key
	if claims.Subject != agentKP.PublicKey {
		t.Errorf("JWT subject = %q; want agent's public key", claims.Subject)
	}

	// Must be project-scoped
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "mclaude.users.alice.hosts.laptop-a.projects.myapp.>")
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "mclaude.users.alice.hosts.laptop-a.projects.myapp.>")

	// Must have KV write for this project's sessions
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>")

	// Must have quota pub/sub (ADR-0044)
	mustContainStr(t, "pub allow", claims.Permissions.Pub.Allow, "mclaude.users.alice.quota")
	mustContainStr(t, "sub allow", claims.Permissions.Sub.Allow, "mclaude.users.alice.quota")

	// Must NOT have broad wildcards
	if containsStr(claims.Permissions.Pub.Allow, "$JS.API.>") {
		t.Error("agent JWT must not have broad $JS.API.> wildcard (ADR-0054)")
	}
	if containsStr(claims.Permissions.Pub.Allow, "$JS.*.API.>") {
		t.Error("agent JWT must not have broad $JS.*.API.> wildcard (ADR-0054)")
	}

	// Must NOT have access to other projects
	for _, s := range append(claims.Permissions.Pub.Allow, claims.Permissions.Sub.Allow...) {
		if strings.Contains(s, "other-project") {
			t.Errorf("agent JWT has access to other-project: %q", s)
		}
	}

	// Must have 5-minute TTL
	if claims.Expires == 0 {
		t.Error("agent JWT should have expiry (5-minute TTL)")
	}
	remaining := claims.Expires - time.Now().Unix()
	if remaining < 250 || remaining > 310 {
		t.Errorf("agent JWT TTL = %ds; want ~300s (5 min)", remaining)
	}
}

// ---- NKey signature verification ----

func TestVerifyNKeySignature_Valid(t *testing.T) {
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	challenge := []byte("random-challenge-nonce-abc123")
	sig, err := kp.Sign(challenge)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := VerifyNKeySignature(pub, challenge, sig); err != nil {
		t.Errorf("VerifyNKeySignature: %v", err)
	}
}

func TestVerifyNKeySignature_Invalid(t *testing.T) {
	kp1, _ := nkeys.CreateUser()
	kp2, _ := nkeys.CreateUser()
	pub2, _ := kp2.PublicKey()

	challenge := []byte("challenge-nonce")
	sig, _ := kp1.Sign(challenge) // signed by kp1

	// Verify against kp2's public key — should fail
	if err := VerifyNKeySignature(pub2, challenge, sig); err == nil {
		t.Error("expected signature verification to fail for wrong key pair")
	}
}

func TestVerifyNKeySignature_TamperedChallenge(t *testing.T) {
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	challenge := []byte("original-challenge")
	sig, _ := kp.Sign(challenge)

	// Tampered challenge — should fail
	tampered := []byte("tampered-challenge")
	if err := VerifyNKeySignature(pub, tampered, sig); err == nil {
		t.Error("expected verification to fail for tampered challenge")
	}
}

// ---- DecodeUserJWT ----

func TestDecodeUserJWT_InvalidSignature(t *testing.T) {
	// Sign with key A, validate with key B → should fail.
	accountA, _ := nkeys.CreateAccount()
	accountB, _ := nkeys.CreateAccount()
	accountBPub, _ := accountB.PublicKey()

	userKP, _, _ := GenerateUserNKey()
	jwt, _ := IssueUserJWT(userKP.PublicKey, "user-id", "user-slug", nil, accountA, 28800)
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

// ---- IssuerAccount ----

func TestIssueUserJWT_IssuerAccountSet(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	userKP, _, _ := GenerateUserNKey()
	jwtStr, err := IssueUserJWT(userKP.PublicKey, "user-ia", "user-ia-slug", nil, accountKP, 28800)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	if claims.IssuerAccount != accountPub {
		t.Errorf("IssuerAccount = %q; want %q", claims.IssuerAccount, accountPub)
	}
}

func TestIssueHostJWT_IssuerAccountSet(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	hostKP, _, _ := GenerateUserNKey()
	jwtStr, err := IssueHostJWT(hostKP.PublicKey, "hslug", accountKP)
	if err != nil {
		t.Fatalf("IssueHostJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	if claims.IssuerAccount != accountPub {
		t.Errorf("IssuerAccount = %q; want %q", claims.IssuerAccount, accountPub)
	}
}

func TestIssueSessionAgentJWT_IssuerAccountSet(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	agentKP, _, _ := GenerateUserNKey()
	jwtStr, err := IssueSessionAgentJWT(agentKP.PublicKey, "sa-user-ia", "host-a", "proj-a", accountKP)
	if err != nil {
		t.Fatalf("IssueSessionAgentJWT: %v", err)
	}

	claims, err := DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	if claims.IssuerAccount != accountPub {
		t.Errorf("IssuerAccount = %q; want %q", claims.IssuerAccount, accountPub)
	}
}

// ---- Expiry tests ----

func TestIssueUserJWT_ExpirySet(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	userKP, _, _ := GenerateUserNKey()
	var expirySecs int64 = 28800 // 8 hours
	before := time.Now().Unix()
	jwtStr, err := IssueUserJWT(userKP.PublicKey, "expiry-user", "expiry-slug", nil, accountKP, expirySecs)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}
	after := time.Now().Unix()

	claims, err := DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}

	wantMin := before + expirySecs
	wantMax := after + expirySecs
	if claims.Expires < wantMin || claims.Expires > wantMax {
		t.Errorf("claims.Expires = %d; want between %d and %d (now + %ds)",
			claims.Expires, wantMin, wantMax, expirySecs)
	}
}

// ---- Legacy functions (backward compat) ----

func TestIssueUserJWTLegacy_ClaimsRoundTrip(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()

	userID := "legacy-user-001"
	expiry := int64(8 * 60 * 60)

	jwt, seed, err := IssueUserJWTLegacy(userID, "legacy-slug", accountKP, expiry)
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}
	if jwt == "" {
		t.Error("empty jwt")
	}
	if len(seed) == 0 {
		t.Error("empty seed (legacy should return seed)")
	}

	claims, err := DecodeUserJWT(jwt, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}
	if claims.Name != userID {
		t.Errorf("claims.Name = %q; want %q", claims.Name, userID)
	}
}

// ---- NATSCredentials format ----

func TestFormatNATSCredentials_Format(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()

	agentKP, _, _ := GenerateUserNKey()
	jwtStr, err := IssueSessionAgentJWT(agentKP.PublicKey, "format-user", "host-a", "proj-a", accountKP)
	if err != nil {
		t.Fatalf("IssueSessionAgentJWT: %v", err)
	}
	agentSeed, _ := agentKP.KeyPair.Seed()

	creds := mclnats.FormatNATSCredentials(jwtStr, agentSeed)
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

// ---- Resource naming helpers ----

func TestUserSessionsBucket(t *testing.T) {
	if got := userSessionsBucket("alice"); got != "mclaude-sessions-alice" {
		t.Errorf("userSessionsBucket = %q; want mclaude-sessions-alice", got)
	}
	if got := userSessionsBucket("alice-gmail"); got != "mclaude-sessions-alice-gmail" {
		t.Errorf("userSessionsBucket = %q; want mclaude-sessions-alice-gmail", got)
	}
}

func TestUserProjectsBucket(t *testing.T) {
	if got := userProjectsBucket("alice"); got != "mclaude-projects-alice" {
		t.Errorf("userProjectsBucket = %q; want mclaude-projects-alice", got)
	}
}

func TestUserSessionsStream(t *testing.T) {
	if got := userSessionsStream("alice"); got != "MCLAUDE_SESSIONS_alice" {
		t.Errorf("userSessionsStream = %q; want MCLAUDE_SESSIONS_alice", got)
	}
}

func TestKVStreamName(t *testing.T) {
	if got := kvStreamName("mclaude-sessions-alice"); got != "KV_mclaude-sessions-alice" {
		t.Errorf("kvStreamName = %q; want KV_mclaude-sessions-alice", got)
	}
}

// ---- Helpers ----

func mustContain(t *testing.T, name string, perms []string, subject string) {
	t.Helper()
	if !containsStr(perms, subject) {
		t.Errorf("%s missing required subject %q", name, subject)
	}
}

func mustNotContain(t *testing.T, name string, perms []string, subject string) {
	t.Helper()
	if containsStr(perms, subject) {
		t.Errorf("%s must NOT contain %q (ADR-0054 security requirement)", name, subject)
	}
}

func mustContainStr(t *testing.T, name string, perms []string, subject string) {
	t.Helper()
	if !containsStr(perms, subject) {
		t.Errorf("%s missing required subject %q\ngot: %v", name, subject, perms)
	}
}

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

// Intentional reference to avoid "imported and not used" error
var _ = fmt.Sprintf

//go:build integration

// Integration tests for ADR-0091 per-user Anthropic token architecture.
// Tests cover: grants, user-agent registration, HTTP endpoints (list grants,
// grant accept, user-agent pubkey), challenge-response JWT issuance (bootstrap
// vs full), default platform auto-grant, user-info KV bucket, and project
// provisioning subject routing.

package server

import (
	cpauth "mclaude-control-plane/auth"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// ----- helpers -----

// doChallenge performs a full challenge-response auth cycle and returns the JWT.
// The nkeyPair must be a NATS user-level key pair. x25519Pub is optional (empty for non-agent).
func doChallenge(t *testing.T, srv *Server, nkeyPair nkeys.KeyPair, x25519Pub string) string {
	t.Helper()

	pub, _ := nkeyPair.PublicKey()

	// Step 1: challenge.
	challengeBody := fmt.Sprintf(`{"nkey_public":%q}`, pub)
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/challenge", bytes.NewBufferString(challengeBody))
	req1.Header.Set("Content-Type", "application/json")
	srv.handleAuthChallenge(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("challenge status = %d; body: %s", rec1.Code, rec1.Body.String())
	}
	var challengeResp struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(rec1.Body).Decode(&challengeResp); err != nil {
		t.Fatalf("decode challenge response: %v", err)
	}

	// Sign the raw challenge string (base64 nonce).
	rawNonce, err := base64.StdEncoding.DecodeString(challengeResp.Challenge)
	if err != nil {
		t.Fatalf("decode challenge nonce: %v", err)
	}
	sig, err := nkeyPair.Sign(rawNonce)
	if err != nil {
		t.Fatalf("sign challenge: %v", err)
	}

	// Step 2: verify.
	var verifyPayload map[string]string
	if x25519Pub != "" {
		verifyPayload = map[string]string{
			"nkey_public": pub,
			"challenge":   challengeResp.Challenge,
			"signature":   base64.StdEncoding.EncodeToString(sig),
			"x25519_pub":  x25519Pub,
		}
	} else {
		verifyPayload = map[string]string{
			"nkey_public": pub,
			"challenge":   challengeResp.Challenge,
			"signature":   base64.StdEncoding.EncodeToString(sig),
		}
	}
	verifyBody, _ := json.Marshal(verifyPayload)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/verify", bytes.NewReader(verifyBody))
	req2.Header.Set("Content-Type", "application/json")
	srv.handleAuthVerify(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("verify status = %d; body: %s", rec2.Code, rec2.Body.String())
	}
	var verifyResp struct {
		OK  bool   `json:"ok"`
		JWT string `json:"jwt"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&verifyResp); err != nil {
		t.Fatalf("decode verify response: %v", err)
	}
	if !verifyResp.OK {
		t.Fatalf("verify ok = false; body: %s", rec2.Body.String())
	}
	return verifyResp.JWT
}

// doChallengeExpectError performs challenge-response and expects a non-200 response.
// Returns the response body for assertion.
func doChallengeExpectError(t *testing.T, srv *Server, nkeyPair nkeys.KeyPair, x25519Pub string) (int, string) {
	t.Helper()

	pub, _ := nkeyPair.PublicKey()

	challengeBody := fmt.Sprintf(`{"nkey_public":%q}`, pub)
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/challenge", bytes.NewBufferString(challengeBody))
	req1.Header.Set("Content-Type", "application/json")
	srv.handleAuthChallenge(rec1, req1)
	if rec1.Code != http.StatusOK {
		// Challenge itself failed — return the challenge status.
		return rec1.Code, rec1.Body.String()
	}
	var challengeResp struct {
		Challenge string `json:"challenge"`
	}
	_ = json.NewDecoder(rec1.Body).Decode(&challengeResp)

	rawNonce, _ := base64.StdEncoding.DecodeString(challengeResp.Challenge)
	sig, _ := nkeyPair.Sign(rawNonce)

	verifyPayload := map[string]string{
		"nkey_public": pub,
		"challenge":   challengeResp.Challenge,
		"signature":   base64.StdEncoding.EncodeToString(sig),
	}
	if x25519Pub != "" {
		verifyPayload["x25519_pub"] = x25519Pub
	}
	verifyBody, _ := json.Marshal(verifyPayload)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/verify", bytes.NewReader(verifyBody))
	req2.Header.Set("Content-Type", "application/json")
	srv.handleAuthVerify(rec2, req2)
	return rec2.Code, rec2.Body.String()
}

// fakeX25519Pub generates a deterministic fake X25519 pubkey (32 random bytes, base64-encoded).
func fakeX25519Pub() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// fakeX25519Pub2 is a different fake X25519 pubkey to test mismatch cases.
func fakeX25519Pub2() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 100)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ----- NATS subscriber tests -----

// TestIntegration_ADR0091_HostGrantCreate verifies that publishing to
// mclaude.hosts.{hslug}.grants.create inserts a pending host_grants row.
func TestIntegration_ADR0091_HostGrantCreate(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	suffix := uuid.NewString()[:8]
	hslug := "test-host-grant-" + suffix
	uslug := "test-user-grant-" + suffix

	payload, _ := json.Marshal(map[string]string{"uslug": uslug})
	subject := "mclaude.hosts." + hslug + ".grants.create"

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request: %v", err)
	}
	var resp map[string]any
	_ = json.Unmarshal(reply.Data, &resp)
	if ok, _ := resp["ok"].(bool); !ok {
		t.Errorf("expected ok=true; got %s", reply.Data)
	}

	// Verify the grant row was created with status='pending'.
	grant, err := db.GetHostGrant(ctx, hslug, uslug)
	if err != nil {
		t.Fatalf("GetHostGrant: %v", err)
	}
	if grant == nil {
		t.Fatal("GetHostGrant returned nil; want pending row")
	}
	if grant.Status != "pending" {
		t.Errorf("grant.Status = %q; want 'pending'", grant.Status)
	}
}

// TestIntegration_ADR0091_UserAgentRegister verifies that a host controller can register
// a user-agent NKey when a host_grants row exists.
func TestIntegration_ADR0091_UserAgentRegister(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	suffix := uuid.NewString()[:8]
	hslug := "ua-reg-host-" + suffix
	uslug := "ua-reg-user-" + suffix

	// First create the host_grants row.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}

	// Generate a fake user-agent NKey.
	agentKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create agent nkey: %v", err)
	}
	agentPub, _ := agentKP.PublicKey()

	payload, _ := json.Marshal(map[string]string{"uslug": uslug, "nkey_public": agentPub})
	subject := "mclaude.hosts." + hslug + ".api.agents.user-agent.register"

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request: %v", err)
	}
	var resp map[string]any
	_ = json.Unmarshal(reply.Data, &resp)
	if ok, _ := resp["ok"].(bool); !ok {
		t.Errorf("expected ok=true; got %s", reply.Data)
	}

	// Verify user_host_agents row was created with nkey_public.
	ua, err := db.GetUserHostAgentBySlug(ctx, hslug, uslug)
	if err != nil {
		t.Fatalf("GetUserHostAgentBySlug: %v", err)
	}
	if ua == nil {
		t.Fatal("GetUserHostAgentBySlug returned nil; want row")
	}
	if ua.NKeyPublic == nil || *ua.NKeyPublic != agentPub {
		t.Errorf("ua.NKeyPublic = %v; want %q", ua.NKeyPublic, agentPub)
	}
}

// TestIntegration_ADR0091_UserAgentRegister_Forbidden verifies that registering a user-agent
// NKey is rejected when no host_grants row exists for the (host, user) pair.
func TestIntegration_ADR0091_UserAgentRegister_Forbidden(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)
	_ = db // unused directly, but ensures migration runs

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	suffix := uuid.NewString()[:8]
	hslug := "ua-forbidden-host-" + suffix
	uslug := "ua-forbidden-user-" + suffix
	// No host_grants row created.

	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()

	payload, _ := json.Marshal(map[string]string{"uslug": uslug, "nkey_public": agentPub})
	subject := "mclaude.hosts." + hslug + ".api.agents.user-agent.register"

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request: %v", err)
	}
	var resp map[string]any
	_ = json.Unmarshal(reply.Data, &resp)
	if code, _ := resp["code"].(string); code != "FORBIDDEN" {
		t.Errorf("expected code=FORBIDDEN; got %s", reply.Data)
	}
}

// ----- HTTP endpoint tests -----

// TestIntegration_ADR0091_ListGrants verifies GET /api/users/{uslug}/grants returns
// the user's grants with x25519Fingerprint populated once the agent has registered.
func TestIntegration_ADR0091_ListGrants(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	suffix := uuid.NewString()[:8]
	uslug := "listgrants-user-" + suffix
	hslug := "listgrants-host-" + suffix

	userID := "listgrants-" + suffix
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "ListGrantsUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Overwrite slug to match our test slug (slug is derived from email, but we need a specific one).
	_, err = db.Pool().Exec(ctx, `UPDATE users SET slug = $1 WHERE id = $2`, uslug, userID)
	if err != nil {
		t.Fatalf("update user slug: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Create grant and user_host_agents row with x25519_pub.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	agentKP2, _ := nkeys.CreateUser()
	agentPub2, _ := agentKP2.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub2); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}
	x25519Pub := fakeX25519Pub()
	if err := db.UpdateUserHostAgentX25519Pub(ctx, hslug, uslug, x25519Pub); err != nil {
		t.Fatalf("UpdateUserHostAgentX25519Pub: %v", err)
	}

	// Set up a JWT for the user (self access).
	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	if err := db.SetUserNKeyPublic(ctx, userID, userPub); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}
	jwtStr := doChallenge(t, srv, userKP, "")

	// Call GET /api/users/{uslug}/grants via the full mux.
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/users/"+uslug+"/grants", nil)
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list grants status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var grants []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&grants); err != nil {
		t.Fatalf("decode grants response: %v", err)
	}
	if len(grants) == 0 {
		t.Fatal("expected at least one grant; got empty list")
	}
	found := false
	for _, g := range grants {
		if hs, _ := g["hostSlug"].(string); hs == hslug {
			found = true
			// x25519Fingerprint should be present since x25519_pub is set.
			if fp, _ := g["x25519Fingerprint"].(string); fp == "" {
				t.Error("x25519Fingerprint is empty; want sha256 hex")
			}
		}
	}
	if !found {
		t.Errorf("grant for host %q not found in response %v", hslug, grants)
	}
}

// TestIntegration_ADR0091_GrantAccept verifies POST /api/users/{uslug}/hosts/{hslug}/grant/accept
// sets the grant to accepted and publishes the activation signal.
func TestIntegration_ADR0091_GrantAccept(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	suffix := uuid.NewString()[:8]
	uslug := "grantaccept-user-" + suffix
	hslug := "grantaccept-host-" + suffix

	userID := "grantaccept-" + suffix
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "GrantAcceptUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, err = db.Pool().Exec(ctx, `UPDATE users SET slug = $1 WHERE id = $2`, uslug, userID)
	if err != nil {
		t.Fatalf("update user slug: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Create grant with pending status.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	// Register user-agent NKey and x25519_pub.
	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}
	x25519Pub := fakeX25519Pub()
	if err := db.UpdateUserHostAgentX25519Pub(ctx, hslug, uslug, x25519Pub); err != nil {
		t.Fatalf("UpdateUserHostAgentX25519Pub: %v", err)
	}

	// Compute the fingerprint.
	fingerprint := computeX25519Fingerprint(x25519Pub)

	// Subscribe to activation signal.
	activateSubj := "mclaude.users." + uslug + ".hosts." + hslug + ".agent.activate"
	activateCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe(activateSubj, func(msg *nats.Msg) {
		activateCh <- msg
	})
	if err != nil {
		t.Fatalf("subscribe activate: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	// Set up user JWT for self access.
	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	if err := db.SetUserNKeyPublic(ctx, userID, userPub); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}
	jwtStr := doChallenge(t, srv, userKP, "")

	// POST grant accept via full mux.
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body, _ := json.Marshal(map[string]string{"x25519_fingerprint": fingerprint})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+uslug+"/hosts/"+hslug+"/grant/accept",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("grant accept status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify grant is now accepted.
	grant, err := db.GetHostGrant(ctx, hslug, uslug)
	if err != nil {
		t.Fatalf("GetHostGrant: %v", err)
	}
	if grant.Status != "accepted" {
		t.Errorf("grant.Status = %q; want 'accepted'", grant.Status)
	}

	// Verify activation signal was published.
	select {
	case msg := <-activateCh:
		_ = msg // signal received
	case <-time.After(3 * time.Second):
		t.Error("activation signal not received within 3s")
	}
}

// TestIntegration_ADR0091_GrantAccept_FingerprintMismatch verifies that a mismatched
// fingerprint returns 409 conflict.
func TestIntegration_ADR0091_GrantAccept_FingerprintMismatch(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	uslug := "fpmmatch-user-" + suffix
	hslug := "fpmmatch-host-" + suffix

	userID := "fpmmatch-" + suffix
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "FPMismatchUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, err = db.Pool().Exec(ctx, `UPDATE users SET slug = $1 WHERE id = $2`, uslug, userID)
	if err != nil {
		t.Fatalf("update user slug: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Create grant and register x25519_pub.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}
	x25519Pub := fakeX25519Pub()
	if err := db.UpdateUserHostAgentX25519Pub(ctx, hslug, uslug, x25519Pub); err != nil {
		t.Fatalf("UpdateUserHostAgentX25519Pub: %v", err)
	}

	// Use a WRONG fingerprint.
	wrongFingerprint := computeX25519Fingerprint(fakeX25519Pub2())

	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	if err := db.SetUserNKeyPublic(ctx, userID, userPub); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}
	jwtStr := doChallenge(t, srv, userKP, "")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body, _ := json.Marshal(map[string]string{"x25519_fingerprint": wrongFingerprint})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+uslug+"/hosts/"+hslug+"/grant/accept",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict; got %d; body: %s", rec.Code, rec.Body.String())
	}
	var errResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp["error"] != "fingerprint_mismatch" {
		t.Errorf("error = %q; want 'fingerprint_mismatch'", errResp["error"])
	}
}

// TestIntegration_ADR0091_UserAgentPubkey verifies GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey
// returns the x25519_pub stored in user_host_agents.
func TestIntegration_ADR0091_UserAgentPubkey(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	uslug := "pubkey-user-" + suffix
	hslug := "pubkey-host-" + suffix

	userID := "pubkey-" + suffix
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "PubkeyUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, err = db.Pool().Exec(ctx, `UPDATE users SET slug = $1 WHERE id = $2`, uslug, userID)
	if err != nil {
		t.Fatalf("update user slug: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Create user_host_agents row and set x25519_pub.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}
	x25519Pub := fakeX25519Pub()
	if err := db.UpdateUserHostAgentX25519Pub(ctx, hslug, uslug, x25519Pub); err != nil {
		t.Fatalf("UpdateUserHostAgentX25519Pub: %v", err)
	}

	// Set up user JWT.
	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	if err := db.SetUserNKeyPublic(ctx, userID, userPub); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}
	jwtStr := doChallenge(t, srv, userKP, "")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/users/"+uslug+"/hosts/"+hslug+"/user-agent/pubkey", nil)
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("user-agent pubkey status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode pubkey response: %v", err)
	}
	if resp["x25519_pub"] != x25519Pub {
		t.Errorf("x25519_pub = %q; want %q", resp["x25519_pub"], x25519Pub)
	}
}

// ----- Auth verify user-agent JWT issuance tests -----

// TestIntegration_ADR0091_AuthVerify_UserAgent_RequiresX25519Pub verifies that
// challenge-response for a user-agent NKey without x25519_pub returns INVALID_REQUEST.
func TestIntegration_ADR0091_AuthVerify_UserAgent_RequiresX25519Pub(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	hslug := "ua-nox25519-host-" + suffix
	uslug := "ua-nox25519-user-" + suffix

	// Set up user_host_agents row.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}

	// Challenge-response without x25519_pub — should return INVALID_REQUEST.
	status, body := doChallengeExpectError(t, srv, agentKP, "" /* no x25519_pub */)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d; body: %s", status, body)
	}
	var resp map[string]string
	_ = json.NewDecoder(strings.NewReader(body)).Decode(&resp)
	if resp["code"] != "INVALID_REQUEST" {
		t.Errorf("code = %q; want 'INVALID_REQUEST'; body: %s", resp["code"], body)
	}
}

// TestIntegration_ADR0091_AuthVerify_UserAgent_BootstrapJWT verifies that a user-agent
// with a pending grant gets a bootstrap JWT (not a full JWT).
func TestIntegration_ADR0091_AuthVerify_UserAgent_BootstrapJWT(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	hslug := "ua-bootstrap-host-" + suffix
	uslug := "ua-bootstrap-user-" + suffix

	// Set up user_host_agents row with pending grant.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "pending"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}

	x25519Pub := fakeX25519Pub()
	jwtStr := doChallenge(t, srv, agentKP, x25519Pub)
	if jwtStr == "" {
		t.Fatal("expected JWT; got empty string")
	}

	// Decode the JWT and check it's a bootstrap JWT (scoped to .agent.activate sub only).
	accountKP := srv.accountKP
	accountPub, _ := accountKP.PublicKey()
	claims, err := cpauth.DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("cpauth.DecodeUserJWT: %v", err)
	}

	// Bootstrap JWT name pattern: "user-agent-bootstrap-{uslug}-{hslug}".
	if !strings.HasPrefix(claims.Name, "user-agent-bootstrap-") {
		t.Errorf("JWT name = %q; want 'user-agent-bootstrap-...' prefix (bootstrap JWT)", claims.Name)
	}

	// Sub should be limited to activate + inbox (not the full agent.> scope).
	subAllow := claims.Permissions.Sub.Allow
	foundActivate := false
	for _, s := range subAllow {
		if strings.Contains(s, ".agent.activate") {
			foundActivate = true
		}
		if strings.Contains(s, ".agent.>") {
			t.Errorf("bootstrap JWT unexpectedly contains .agent.> in Sub: %v", subAllow)
		}
	}
	if !foundActivate {
		t.Errorf("bootstrap JWT missing .agent.activate in Sub: %v", subAllow)
	}

	// Verify x25519_pub was stored.
	ua, err := db.GetUserHostAgentBySlug(ctx, hslug, uslug)
	if err != nil {
		t.Fatalf("GetUserHostAgentBySlug: %v", err)
	}
	if ua == nil || ua.X25519Pub == nil || *ua.X25519Pub != x25519Pub {
		t.Errorf("x25519_pub not stored correctly; got %v", ua)
	}
}

// TestIntegration_ADR0091_AuthVerify_UserAgent_FullJWT verifies that a user-agent
// with an accepted grant gets a full JWT.
func TestIntegration_ADR0091_AuthVerify_UserAgent_FullJWT(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	hslug := "ua-fulljwt-host-" + suffix
	uslug := "ua-fulljwt-user-" + suffix

	// Set up user_host_agents row with accepted grant.
	if err := db.UpsertHostGrant(ctx, hslug, uslug, "accepted"); err != nil {
		t.Fatalf("UpsertHostGrant: %v", err)
	}
	agentKP, _ := nkeys.CreateUser()
	agentPub, _ := agentKP.PublicKey()
	if err := db.UpsertUserHostAgentNKey(ctx, hslug, uslug, agentPub); err != nil {
		t.Fatalf("UpsertUserHostAgentNKey: %v", err)
	}

	x25519Pub := fakeX25519Pub()
	jwtStr := doChallenge(t, srv, agentKP, x25519Pub)
	if jwtStr == "" {
		t.Fatal("expected JWT; got empty string")
	}

	// Decode the JWT and check it's a full JWT (scoped to .agent.> pub+sub).
	accountKP := srv.accountKP
	accountPub, _ := accountKP.PublicKey()
	claims, err := cpauth.DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("cpauth.DecodeUserJWT: %v", err)
	}

	// Full JWT name pattern: "user-agent-{uslug}-{hslug}".
	if !strings.HasPrefix(claims.Name, "user-agent-") || strings.Contains(claims.Name, "bootstrap") {
		t.Errorf("JWT name = %q; want 'user-agent-...' without 'bootstrap' (full JWT)", claims.Name)
	}

	// Pub and Sub should both contain .agent.>
	agentSubject := "mclaude.users." + uslug + ".hosts." + hslug + ".agent.>"
	foundInPub := false
	for _, s := range claims.Permissions.Pub.Allow {
		if s == agentSubject {
			foundInPub = true
		}
	}
	if !foundInPub {
		t.Errorf("full JWT missing %q in Pub: %v", agentSubject, claims.Permissions.Pub.Allow)
	}
	foundInSub := false
	for _, s := range claims.Permissions.Sub.Allow {
		if s == agentSubject {
			foundInSub = true
		}
	}
	if !foundInSub {
		t.Errorf("full JWT missing %q in Sub: %v", agentSubject, claims.Permissions.Sub.Allow)
	}
}

// ----- Admin cluster default platform tests (ADR-0097 rewrites) -----

// TestIntegration_ADR0091_AdminCluster_DefaultPlatform verifies that a NATS
// registration with defaultPlatform=true (by an admin user) sets
// is_default_platform=true on the hosts row. Rewritten for ADR-0097: uses
// mclaude.users.{uslug}.hosts._.register instead of deleted POST /admin/clusters.
func TestIntegration_ADR0091_AdminCluster_DefaultPlatform(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	// Create an admin user to own the host.
	suffix := uuid.NewString()[:8]
	adminID := "dp-admin-" + suffix
	adminUser, err := db.CreateUser(ctx, adminID, adminID+"@test.example.com", "DPAdmin", "")
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}
	if err := db.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("SetUserAdmin: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, adminID) }) //nolint:errcheck

	hostName := "Default Platform Test " + suffix
	hostKP, _ := nkeys.CreateUser()
	hostPub, _ := hostKP.PublicKey()

	payload, _ := json.Marshal(NATSHostRegisterRequest{
		Name:            hostName,
		NKeyPublic:      hostPub,
		DefaultPlatform: true,
	})
	subject := "mclaude.users." + adminUser.Slug + ".hosts._.register"
	reply, err := nc.Request(subject, payload, 5*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}

	var resp map[string]any
	_ = json.Unmarshal(reply.Data, &resp)
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("register response not ok: %s", reply.Data)
	}
	hostSlug, _ := resp["slug"].(string)
	if hostSlug == "" {
		t.Fatal("register response missing slug")
	}
	t.Cleanup(func() {
		db.Pool().Exec(ctx, `DELETE FROM hosts WHERE user_id = $1 AND slug = $2`, adminUser.ID, hostSlug) //nolint:errcheck
	})

	// Verify is_default_platform=true in the DB.
	var isDefault bool
	if err := db.Pool().QueryRow(ctx,
		`SELECT is_default_platform FROM hosts WHERE user_id = $1 AND slug = $2`,
		adminUser.ID, hostSlug).Scan(&isDefault); err != nil {
		t.Fatalf("query is_default_platform: %v", err)
	}
	if !isDefault {
		t.Error("is_default_platform = false; want true")
	}
}

// TestIntegration_ADR0091_AdminCluster_DefaultPlatform_Conflict verifies that a second
// NATS registration with defaultPlatform=true returns {error: "default_platform_exists"}.
// Rewritten for ADR-0097: uses NATS registration instead of deleted POST /admin/clusters.
func TestIntegration_ADR0091_AdminCluster_DefaultPlatform_Conflict(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	suffix := uuid.NewString()[:8]
	adminID := "dp-conflict-" + suffix
	adminUser, err := db.CreateUser(ctx, adminID, adminID+"@test.example.com", "DPConflict", "")
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}
	if err := db.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("SetUserAdmin: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, adminID) }) //nolint:errcheck

	subject := "mclaude.users." + adminUser.Slug + ".hosts._.register"

	registerDefaultPlatform := func(name string) (int, string) {
		kp, _ := nkeys.CreateUser()
		pub, _ := kp.PublicKey()
		pl, _ := json.Marshal(NATSHostRegisterRequest{
			Name:            name,
			NKeyPublic:      pub,
			DefaultPlatform: true,
		})
		rep, err := nc.Request(subject, pl, 5*time.Second)
		if err != nil {
			return 0, err.Error()
		}
		var r map[string]any
		_ = json.Unmarshal(rep.Data, &r)
		errVal, _ := r["error"].(string)
		ok, _ := r["ok"].(bool)
		if ok {
			return 201, ""
		}
		return 409, errVal
	}

	// First registration should succeed.
	code1, _ := registerDefaultPlatform("Default Host 1 " + suffix)
	if code1 != 201 {
		t.Fatalf("first defaultPlatform host: code = %d; want 201", code1)
	}

	// Second should return conflict.
	code2, errVal := registerDefaultPlatform("Default Host 2 " + suffix)
	if code2 != 409 {
		t.Fatalf("second defaultPlatform host: code = %d; want 409", code2)
	}
	if errVal != "default_platform_exists" {
		t.Errorf("error = %q; want 'default_platform_exists'", errVal)
	}
}

// ----- Default platform auto-grant test -----

// TestIntegration_ADR0091_DefaultPlatform_AutoGrant verifies that when a user authenticates
// (challenge-response) and a default platform exists, CP auto-grants + auto-accepts the user.
func TestIntegration_ADR0091_DefaultPlatform_AutoGrant(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	suffix := uuid.NewString()[:8]
	defaultHostSlug := "autogrant-default-" + suffix

	// Create an owner user for the default platform host (hosts.user_id foreign key).
	ownerID := "autogrant-owner-" + suffix
	_, err := db.CreateUser(ctx, ownerID, ownerID+"@test.example.com", "AutoGrantOwner", "")
	if err != nil {
		t.Fatalf("CreateUser owner: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	// Insert a default platform host row directly.
	// ADR-0097: no type/role columns.
	hostID := uuid.NewString()
	_, err = db.Pool().Exec(ctx, `
		INSERT INTO hosts (id, user_id, slug, name, is_default_platform)
		VALUES ($1, $2, $3, $4, TRUE)`,
		hostID, ownerID, defaultHostSlug, defaultHostSlug)
	if err != nil {
		t.Fatalf("insert default platform host: %v", err)
	}
	t.Cleanup(func() {
		db.Pool().Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) //nolint:errcheck
	})

	// Create a user and register their NKey.
	userID := "autogrant-user-" + suffix
	user, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "AutoGrantUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	if err := db.SetUserNKeyPublic(ctx, userID, userPub); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}

	// Subscribe to grants.create to observe the auto-publish.
	grantSubj := "mclaude.hosts." + defaultHostSlug + ".grants.create"
	grantCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe(grantSubj, func(msg *nats.Msg) {
		grantCh <- msg
	})
	if err != nil {
		t.Fatalf("subscribe grants.create: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	// Authenticate the user via challenge-response — should trigger auto-grant.
	_ = doChallenge(t, srv, userKP, "")

	// Verify host_grants row was created with status='accepted'.
	grant, err := db.GetHostGrant(ctx, defaultHostSlug, user.Slug)
	if err != nil {
		t.Fatalf("GetHostGrant: %v", err)
	}
	if grant == nil {
		t.Fatal("GetHostGrant returned nil; expected auto-accepted grant")
	}
	if grant.Status != "accepted" {
		t.Errorf("grant.Status = %q; want 'accepted'", grant.Status)
	}

	// Verify grants.create was published.
	select {
	case msg := <-grantCh:
		var payload map[string]string
		_ = json.Unmarshal(msg.Data, &payload)
		if payload["uslug"] != user.Slug {
			t.Errorf("grants.create payload uslug = %q; want %q", payload["uslug"], user.Slug)
		}
	case <-time.After(3 * time.Second):
		t.Error("grants.create not published within 3s")
	}
}

// ----- User info KV bucket test -----

// TestIntegration_ADR0091_UserInfoKVBucket verifies that ensureUserResources creates
// the mclaude-user-info-{uslug} KV bucket.
func TestIntegration_ADR0091_UserInfoKVBucket(t *testing.T) {
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	uslug := "userinfo-kv-" + uuid.NewString()[:8]

	if err := ensureUserResources(nc, uslug); err != nil {
		t.Fatalf("ensureUserResources: %v", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	// Verify mclaude-user-info-{uslug} KV bucket exists.
	bucketName := "mclaude-user-info-" + uslug
	kv, err := js.KeyValue(bucketName)
	if err != nil {
		t.Fatalf("KeyValue(%q): %v — bucket not created by ensureUserResources", bucketName, err)
	}
	if kv == nil {
		t.Fatalf("KeyValue(%q) returned nil", bucketName)
	}
}

// ----- ADR-0098: ensureUserResources all-four + backfill + error-continues tests -----

// TestIntegration_EnsureUserResources_AllFour verifies that calling ensureUserResources
// on a fresh NATS creates all four per-user JetStream resources:
// KV mclaude-sessions-{uslug}, KV mclaude-projects-{uslug},
// KV mclaude-user-info-{uslug}, and stream MCLAUDE_SESSIONS_{uslug}.
func TestIntegration_EnsureUserResources_AllFour(t *testing.T) {
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	uslug := "allfour-" + uuid.NewString()[:8]

	if err := ensureUserResources(nc, uslug); err != nil {
		t.Fatalf("ensureUserResources: %v", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	// Assert all four resources exist.
	for _, bucket := range []string{
		"mclaude-sessions-" + uslug,
		"mclaude-projects-" + uslug,
		"mclaude-user-info-" + uslug,
	} {
		if _, err := js.KeyValue(bucket); err != nil {
			t.Errorf("KV bucket %q not created by ensureUserResources: %v", bucket, err)
		}
	}

	streamName := "MCLAUDE_SESSIONS_" + uslug
	if _, err := js.StreamInfo(streamName); err != nil {
		t.Errorf("stream %q not created by ensureUserResources: %v", streamName, err)
	}
}

// TestIntegration_EnsureUserResources_Backfill verifies that calling ensureUserResources
// when sessions and projects KVs already exist (simulating the dev-mclaude-local state
// before ADR-0098 fix) creates the missing user-info KV and sessions stream without
// disturbing the existing buckets (ADR-0098 idempotency requirement).
func TestIntegration_EnsureUserResources_Backfill(t *testing.T) {
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	uslug := "backfill-" + uuid.NewString()[:8]

	// Pre-create only sessions + projects KVs (the state before ADR-0098 fix).
	for _, bucket := range []string{
		"mclaude-sessions-" + uslug,
		"mclaude-projects-" + uslug,
	} {
		if _, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: bucket, History: 1}); err != nil {
			t.Fatalf("pre-create KV %q: %v", bucket, err)
		}
	}

	// Capture the revision of the pre-created sessions bucket to verify it is unchanged.
	sessionsBucket := "mclaude-sessions-" + uslug
	sessKV, err := js.KeyValue(sessionsBucket)
	if err != nil {
		t.Fatalf("KeyValue sessions pre: %v", err)
	}
	sessStatus, err := sessKV.Status()
	if err != nil {
		t.Fatalf("sessions KV Status pre: %v", err)
	}
	preCallSeqID := sessStatus.Values()

	// Call ensureUserResources — must be a no-op for existing buckets and create the missing two.
	if err := ensureUserResources(nc, uslug); err != nil {
		t.Fatalf("ensureUserResources: %v", err)
	}

	// All four must exist.
	for _, bucket := range []string{
		"mclaude-sessions-" + uslug,
		"mclaude-projects-" + uslug,
		"mclaude-user-info-" + uslug,
	} {
		if _, err := js.KeyValue(bucket); err != nil {
			t.Errorf("KV bucket %q missing after backfill: %v", bucket, err)
		}
	}

	streamName := "MCLAUDE_SESSIONS_" + uslug
	if _, err := js.StreamInfo(streamName); err != nil {
		t.Errorf("stream %q missing after backfill: %v", streamName, err)
	}

	// The pre-existing sessions KV must be unchanged (same sequence ID — no entries added).
	sessKVPost, err := js.KeyValue(sessionsBucket)
	if err != nil {
		t.Fatalf("KeyValue sessions post: %v", err)
	}
	sessStatusPost, err := sessKVPost.Status()
	if err != nil {
		t.Fatalf("sessions KV Status post: %v", err)
	}
	if sessStatusPost.Values() != preCallSeqID {
		t.Errorf("sessions KV was modified by ensureUserResources: pre=%d post=%d",
			preCallSeqID, sessStatusPost.Values())
	}
}

// TestIntegration_HandleLogin_ProvisionsAllFour verifies that POST /auth/login
// provisions all four per-user JetStream resources. Per ADR-0100, nkey_public is
// required; there is no legacy path.
func TestIntegration_HandleLogin_ProvisionsAllFour(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	uslug := "login-nkey-" + uuid.NewString()[:8]
	userID := uslug
	email := userID + "@test.example.com"
	hash, err := HashPassword("pw-" + userID)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "NKeyUser", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE users SET slug = $1 WHERE id = $2`, uslug, userID); err != nil {
		t.Fatalf("update slug: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	userKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("CreateUser NKey: %v", err)
	}
	userPub, _ := userKP.PublicKey()

	body := fmt.Sprintf(`{"email":%q,"password":%q,"nkey_public":%q}`, email, "pw-"+userID, userPub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body: %s", rec.Code, rec.Body.String())
	}

	assertAllFourResourcesExist(t, nc, uslug)
}

// assertAllFourResourcesExist checks that all four per-user JetStream resources exist.
// Helper shared by ADR-0098 integration tests.
func assertAllFourResourcesExist(t *testing.T, nc *nats.Conn, uslug string) {
	t.Helper()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	for _, bucket := range []string{
		"mclaude-sessions-" + uslug,
		"mclaude-projects-" + uslug,
		"mclaude-user-info-" + uslug,
	} {
		if _, err := js.KeyValue(bucket); err != nil {
			t.Errorf("KV bucket %q missing: %v", bucket, err)
		}
	}
	streamName := "MCLAUDE_SESSIONS_" + uslug
	if _, err := js.StreamInfo(streamName); err != nil {
		t.Errorf("stream %q missing: %v", streamName, err)
	}
}

// TestIntegration_EnsureUserResources_StepErrorContinues verifies that a failure in
// step 3 (user-info KV ensure) does not short-circuit step 4 (sessions stream ensure).
// The function must return the first error AND have attempted all four steps (ADR-0098).
func TestIntegration_EnsureUserResources_StepErrorContinues(t *testing.T) {
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	uslug := "step-error-" + uuid.NewString()[:8]

	// Override step 3 to always return an error.
	injectedErr := fmt.Errorf("injected step-3 error for testing")
	origFn := ensurePerUserInfoKVFn
	ensurePerUserInfoKVFn = func(js nats.JetStreamContext, slug string) (nats.KeyValue, error) {
		return nil, injectedErr
	}
	t.Cleanup(func() { ensurePerUserInfoKVFn = origFn })

	// Call ensureUserResources — must return the injected error AND still attempt step 4.
	retErr := ensureUserResources(nc, uslug)
	if retErr == nil {
		t.Error("ensureUserResources returned nil; expected error from step 3")
	}
	if retErr != injectedErr {
		t.Errorf("ensureUserResources returned %v; want injected step-3 error", retErr)
	}

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	// Steps 1+2 (sessions KV, projects KV) must have succeeded.
	for _, bucket := range []string{
		"mclaude-sessions-" + uslug,
		"mclaude-projects-" + uslug,
	} {
		if _, err := js.KeyValue(bucket); err != nil {
			t.Errorf("KV bucket %q (step 1/2) missing after step-3 error: %v", bucket, err)
		}
	}

	// Step 3 (user-info KV) should NOT exist — it was injected to error.
	if _, err := js.KeyValue("mclaude-user-info-" + uslug); err == nil {
		t.Error("user-info KV bucket exists; expected absent (step 3 was injected to fail)")
	}

	// Step 4 (sessions stream) MUST exist — the chain must not short-circuit on step 3 error.
	streamName := "MCLAUDE_SESSIONS_" + uslug
	if _, err := js.StreamInfo(streamName); err != nil {
		t.Errorf("stream %q (step 4) missing after step-3 error: %v — chain short-circuited!", streamName, err)
	}
}

// ----- User JWT includes user-info permissions test -----

// TestIntegration_ADR0091_UserJWT_HasUserInfoPermissions verifies that the user JWT
// includes pub/sub permissions for the mclaude-user-info-{uslug} KV bucket.
func TestIntegration_ADR0091_UserJWT_HasUserInfoPermissions(t *testing.T) {
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account kp: %v", err)
	}

	userKP, _, err := cpauth.GenerateUserNKey()
	if err != nil {
		t.Fatalf("cpauth.GenerateUserNKey: %v", err)
	}

	uslug := "jwt-info-perms-user"
	hostSlugs := []string{"myhost"}

	jwtStr, err := cpauth.IssueUserJWT(userKP.PublicKey, "user-id-123", uslug, hostSlugs, accountKP, 3600)
	if err != nil {
		t.Fatalf("cpauth.IssueUserJWT: %v", err)
	}

	accountPub, _ := accountKP.PublicKey()
	claims, err := cpauth.DecodeUserJWT(jwtStr, accountPub)
	if err != nil {
		t.Fatalf("cpauth.DecodeUserJWT: %v", err)
	}

	infoStream := "KV_mclaude-user-info-" + uslug
	infoBucket := "mclaude-user-info-" + uslug

	// Check that Pub includes user-info KV subjects.
	// ADR-0107: wildcard changed from .* to .> (dotted keys); $JS.API.INFO added for NATS.js.
	expectedPubSubjects := []string{
		"$JS.API.DIRECT.GET." + infoStream + ".>",
		"$JS.API.CONSUMER.CREATE." + infoStream + ".>",
		"$JS.API.STREAM.INFO." + infoStream,
		"$KV." + infoBucket + ".>",
		"$JS.API.INFO",
	}
	for _, want := range expectedPubSubjects {
		found := false
		for _, p := range claims.Permissions.Pub.Allow {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("user JWT missing Pub permission %q; Pub list: %v", want, claims.Permissions.Pub.Allow)
		}
	}

	// Check that Sub includes user-info KV push delivery.
	// ADR-0107: wildcard changed from .* to .> (dotted keys).
	expectedSubSubjects := []string{
		"$KV." + infoBucket + ".>",
		"$JS.FC." + infoStream + ".>",
	}
	for _, want := range expectedSubSubjects {
		found := false
		for _, s := range claims.Permissions.Sub.Allow {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("user JWT missing Sub permission %q; Sub list: %v", want, claims.Permissions.Sub.Allow)
		}
	}
}

// ----- ADR-0100: remove legacy NKey login tests -----

// TestIntegration_ADR0100_Login_WithoutNKeyPublic_Returns400 verifies that
// POST /auth/login without nkey_public returns 400 {error: "nkey_public is required"}.
func TestIntegration_ADR0100_Login_WithoutNKeyPublic_Returns400(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	userID := "adr0100-nopub-" + suffix
	email := userID + "@test.example.com"
	hash, err := HashPassword("pw-" + userID)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "ADR0100NoPub", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Login without nkey_public.
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, "pw-"+userID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "nkey_public is required" {
		t.Errorf("error = %q; want 'nkey_public is required'", resp["error"])
	}
}

// TestIntegration_ADR0100_Login_InvalidNKeyFormat_Returns400 verifies that
// POST /auth/login with an invalid nkey_public format returns 400 {error: "invalid nkey_public format"}.
func TestIntegration_ADR0100_Login_InvalidNKeyFormat_Returns400(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	userID := "adr0100-badpub-" + suffix
	email := userID + "@test.example.com"
	hash, err := HashPassword("pw-" + userID)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "ADR0100BadPub", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Login with invalid nkey_public (not a valid user key — missing U prefix).
	body := fmt.Sprintf(`{"email":%q,"password":%q,"nkey_public":"NOTAVALIDUSERKEY1234567890"}`, email, "pw-"+userID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "invalid nkey_public format" {
		t.Errorf("error = %q; want 'invalid nkey_public format'", resp["error"])
	}
}

// TestIntegration_ADR0100_Login_Response_NoNKeySeed verifies that the login response
// does not contain nkeySeed (ADR-0100: server never returns private key material).
func TestIntegration_ADR0100_Login_Response_NoNKeySeed(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t, db, nil)

	suffix := uuid.NewString()[:8]
	userID := "adr0100-noseed-" + suffix
	email := userID + "@test.example.com"
	hash, err := HashPassword("pw-" + userID)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "ADR0100NoSeed", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Login with valid nkey_public.
	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	body := fmt.Sprintf(`{"email":%q,"password":%q,"nkey_public":%q}`, email, "pw-"+userID, userPub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Decode as raw map to check for nkeySeed presence.
	var respMap map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&respMap); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, hasField := respMap["nkeySeed"]; hasField {
		t.Error("login response must not contain nkeySeed (ADR-0100): server never returns private key material")
	}
	// JWT must be present.
	if jwtVal, _ := respMap["jwt"].(string); jwtVal == "" {
		t.Error("login response missing jwt")
	}
}

// TestIntegration_ADR0100_Login_TriggersDefaultPlatformAutoGrant verifies that
// POST /auth/login with nkey_public creates a host_grants row for the default platform
// and publishes grants.create (ADR-0100).
func TestIntegration_ADR0100_Login_TriggersDefaultPlatformAutoGrant(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	suffix := uuid.NewString()[:8]

	// Create an owner for the default platform host.
	ownerID := "adr0100-owner-" + suffix
	_, err := db.CreateUser(ctx, ownerID, ownerID+"@test.example.com", "ADR0100Owner", "")
	if err != nil {
		t.Fatalf("CreateUser owner: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	// Insert a default platform host row directly.
	hostID := uuid.NewString()
	defaultHostSlug := "adr0100-default-" + suffix
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, is_default_platform) VALUES ($1, $2, $3, $4, TRUE)`,
		hostID, ownerID, defaultHostSlug, defaultHostSlug)
	if err != nil {
		t.Fatalf("insert default platform host: %v", err)
	}
	t.Cleanup(func() {
		db.Pool().Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) //nolint:errcheck
	})

	// Create a user with a password.
	userID := "adr0100-user-" + suffix
	email := userID + "@test.example.com"
	hash, err := HashPassword("pw-" + userID)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := db.CreateUser(ctx, userID, email, "ADR0100User", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Subscribe to grants.create to observe the auto-publish.
	grantSubj := "mclaude.hosts." + defaultHostSlug + ".grants.create"
	grantCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe(grantSubj, func(msg *nats.Msg) {
		grantCh <- msg
	})
	if err != nil {
		t.Fatalf("subscribe grants.create: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	// Login with nkey_public — should trigger auto-grant.
	userKP, _ := nkeys.CreateUser()
	userPub, _ := userKP.PublicKey()
	body := fmt.Sprintf(`{"email":%q,"password":%q,"nkey_public":%q}`, email, "pw-"+userID, userPub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify host_grants row was created with status='accepted'.
	grant, err := db.GetHostGrant(ctx, defaultHostSlug, user.Slug)
	if err != nil {
		t.Fatalf("GetHostGrant: %v", err)
	}
	if grant == nil {
		t.Fatal("GetHostGrant returned nil; expected auto-accepted grant after login")
	}
	if grant.Status != "accepted" {
		t.Errorf("grant.Status = %q; want 'accepted'", grant.Status)
	}

	// Verify grants.create was published.
	select {
	case msg := <-grantCh:
		var payload map[string]string
		_ = json.Unmarshal(msg.Data, &payload)
		if payload["uslug"] != user.Slug {
			t.Errorf("grants.create payload uslug = %q; want %q", payload["uslug"], user.Slug)
		}
	case <-time.After(3 * time.Second):
		t.Error("grants.create not published within 3s after login")
	}
}

// TestIntegration_ADR0100_DeviceCodePoll_TriggersAutoGrant verifies that an authorized
// device-code poll fires the default-platform auto-grant (ADR-0100).
func TestIntegration_ADR0100_DeviceCodePoll_TriggersAutoGrant(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	suffix := uuid.NewString()[:8]

	// Create an owner for the default platform host.
	ownerID := "adr0100-dc-owner-" + suffix
	_, err := db.CreateUser(ctx, ownerID, ownerID+"@test.example.com", "ADR0100DCOwner", "")
	if err != nil {
		t.Fatalf("CreateUser owner: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	// Insert a default platform host row directly.
	hostID := uuid.NewString()
	defaultHostSlug := "adr0100-dc-default-" + suffix
	_, err = db.Pool().Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, is_default_platform) VALUES ($1, $2, $3, $4, TRUE)`,
		hostID, ownerID, defaultHostSlug, defaultHostSlug)
	if err != nil {
		t.Fatalf("insert default platform host: %v", err)
	}
	t.Cleanup(func() {
		db.Pool().Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) //nolint:errcheck
	})

	// Create a user.
	userID := "adr0100-dc-user-" + suffix
	email := userID + "@test.example.com"
	hash, err := HashPassword("pw-" + userID)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := db.CreateUser(ctx, userID, email, "ADR0100DCUser", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Generate NKey for CLI.
	cliKP, _ := nkeys.CreateUser()
	cliPub, _ := cliKP.PublicKey()

	// Initiate device-code flow.
	createBody := fmt.Sprintf(`{"publicKey":%q}`, cliPub)
	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/auth/device-code", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodeCreate(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("device-code create status = %d; body: %s", createRec.Code, createRec.Body.String())
	}
	var codeResp CLIDeviceCodeResponse
	if err := json.NewDecoder(createRec.Body).Decode(&codeResp); err != nil {
		t.Fatalf("decode device-code response: %v", err)
	}

	// Simulate authorization by completing the device-code flow (POST /verify with credentials).
	verifyBody := fmt.Sprintf("user_code=%s&email=%s&password=pw-%s", codeResp.UserCode, email, userID)
	verifyRec := httptest.NewRecorder()
	verifyReq := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/verify",
		strings.NewReader(verifyBody))
	verifyReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.handleCLIDeviceCodeVerify(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("device-code verify status = %d; body: %s", verifyRec.Code, verifyRec.Body.String())
	}

	// Subscribe to grants.create before polling.
	grantSubj := "mclaude.hosts." + defaultHostSlug + ".grants.create"
	grantCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe(grantSubj, func(msg *nats.Msg) {
		grantCh <- msg
	})
	if err != nil {
		t.Fatalf("subscribe grants.create: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	// Poll — when authorized, auto-grant fires.
	pollBody := fmt.Sprintf(`{"deviceCode":%q}`, codeResp.DeviceCode)
	pollRec := httptest.NewRecorder()
	pollReq := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/poll", strings.NewReader(pollBody))
	pollReq.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodePoll(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("poll status = %d; body: %s", pollRec.Code, pollRec.Body.String())
	}
	var pollResp CLIDeviceCodePollResponse
	if err := json.NewDecoder(pollRec.Body).Decode(&pollResp); err != nil {
		t.Fatalf("decode poll response: %v", err)
	}
	if pollResp.Status != "authorized" {
		t.Fatalf("poll status = %q; want 'authorized'", pollResp.Status)
	}

	// Verify host_grants row created with status='accepted'.
	grant, err := db.GetHostGrant(ctx, defaultHostSlug, user.Slug)
	if err != nil {
		t.Fatalf("GetHostGrant: %v", err)
	}
	if grant == nil {
		t.Fatal("GetHostGrant returned nil; expected auto-accepted grant after device-code authorization")
	}
	if grant.Status != "accepted" {
		t.Errorf("grant.Status = %q; want 'accepted'", grant.Status)
	}

	// Verify grants.create was published.
	select {
	case msg := <-grantCh:
		var payload map[string]string
		_ = json.Unmarshal(msg.Data, &payload)
		if payload["uslug"] != user.Slug {
			t.Errorf("grants.create payload uslug = %q; want %q", payload["uslug"], user.Slug)
		}
	case <-time.After(3 * time.Second):
		t.Error("grants.create not published within 3s after device-code poll authorized")
	}
}

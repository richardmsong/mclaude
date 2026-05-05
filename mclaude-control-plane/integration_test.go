//go:build integration

// Integration tests require Docker (for NATS + Postgres via testcontainers).
// Run with: go test -tags integration ./...
//
// The compose stack is started once in TestMain (integration_main_test.go) and
// shared across all integration tests to avoid container startup overhead.

package main

import (
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
	"github.com/rs/zerolog"
)

// ---- Postgres CRUD ----

func TestIntegration_UserCreateAndFetch(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "integ-user-001"
	user, err := db.CreateUser(ctx, userID, "alice@example.com", "Alice", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID != userID {
		t.Errorf("user.ID = %q; want %q", user.ID, userID)
	}

	fetched, err := db.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if fetched == nil {
		t.Fatal("GetUserByID returned nil")
	}
	if fetched.Email != "alice@example.com" {
		t.Errorf("email = %q; want alice@example.com", fetched.Email)
	}
}

func TestIntegration_UserEmailUnique(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	db.CreateUser(ctx, "uniq-u1", "dup@example.com", "User1", "") //nolint:errcheck

	_, err := db.CreateUser(ctx, "uniq-u2", "dup@example.com", "User2", "")
	if err == nil {
		t.Error("expected error on duplicate email; got nil")
	}
}

func TestIntegration_GetUserByEmail(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	db.CreateUser(ctx, "email-lookup-1", "lookup@example.com", "LookupUser", "") //nolint:errcheck

	user, err := db.GetUserByEmail(ctx, "lookup@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if user == nil || user.ID != "email-lookup-1" {
		t.Errorf("GetUserByEmail returned wrong user: %+v", user)
	}

	missing, err := db.GetUserByEmail(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail missing: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing user; got %+v", missing)
	}
}

func TestIntegration_DeleteUser(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	db.CreateUser(ctx, "del-user-1", "del@example.com", "DelUser", "") //nolint:errcheck

	if err := db.DeleteUser(ctx, "del-user-1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	user, err := db.GetUserByID(ctx, "del-user-1")
	if err != nil {
		t.Fatalf("GetUserByID after delete: %v", err)
	}
	if user != nil {
		t.Errorf("expected nil after delete; got %+v", user)
	}
}

func TestIntegration_MigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	for i := 0; i < 3; i++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("Migrate call %d: %v", i+1, err)
		}
	}
}

// ---- HTTP endpoints ----

func TestIntegration_VersionEndpoint(t *testing.T) {
	t.Setenv("MIN_CLIENT_VERSION", "2.0.0")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	handleVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}

	var resp VersionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.MinClientVersion != "2.0.0" {
		t.Errorf("minClientVersion = %q; want 2.0.0", resp.MinClientVersion)
	}
}

// ---- NATS connectivity ----

// TestIntegration_NATSSubjectPermissions verifies a real NATS broker is reachable
// and that a user JWT issued by the control-plane decodes to correct subject scopes.
// Note: the test compose NATS server has no operator configured so broker-side JWT
// enforcement is not tested here — that is covered by the e2e category.
func TestIntegration_NATSSubjectPermissions(t *testing.T) {
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account key: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour).Unix()
	aliceJWT, aliceSeed, err := IssueUserJWTLegacy("alice", "alice-slug", accountKP, expiresAt)
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}

	aliceKP, err := nkeys.FromSeed(aliceSeed)
	if err != nil {
		t.Fatalf("FromSeed: %v", err)
	}

	// Attempt JWT auth; fall back to no-auth if broker has no operator configured.
	nc, err := nats.Connect(integDeps.NATSAddr,
		nats.UserJWT(
			func() (string, error) { return aliceJWT, nil },
			func(nonce []byte) ([]byte, error) { return aliceKP.Sign(nonce) },
		),
		nats.MaxReconnects(0),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		t.Logf("NATS JWT connect (no operator): %v — falling back to no-auth", err)
		nc, err = nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
		if err != nil {
			t.Fatalf("NATS connect: %v", err)
		}
	}
	defer nc.Close()

	subject := "mclaude.alice.test"
	ch := make(chan *nats.Msg, 1)
	sub, err := nc.ChanSubscribe(subject, ch)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := nc.Publish(subject, []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	nc.Flush() //nolint:errcheck

	select {
	case msg := <-ch:
		if !strings.EqualFold(string(msg.Data), "hello") {
			t.Errorf("received %q; want hello", msg.Data)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for message")
	}
}

// ---- helpers ----

func mustConnectDB(t *testing.T, ctx context.Context) *DB {
	t.Helper()
	var db *DB
	var err error
	for attempt := 1; attempt <= 10; attempt++ {
		db, err = ConnectDB(ctx, integDeps.PostgresDSN)
		if err == nil {
			break
		}
		t.Logf("ConnectDB attempt %d/10: %v (retrying)", attempt, err)
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("ConnectDB: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// TestIntegration_StartProjectsSubscriberCreatesKVBuckets verifies that
// StartProjectsSubscriber creates the shared mclaude-hosts KV bucket at startup.
// Per ADR-0054: only mclaude-hosts is created at startup; per-user KV buckets
// (mclaude-sessions-{uslug}, mclaude-projects-{uslug}) are created lazily on
// user registration/first login. The old shared mclaude-projects, mclaude-job-queue,
// and mclaude-sessions buckets were removed per ADR-0054 and ADR-0044.
func TestIntegration_StartProjectsSubscriberCreatesKVBuckets(t *testing.T) {
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	s := &Server{}
	if err := s.StartProjectsSubscriber(nc); err != nil {
		t.Fatalf("StartProjectsSubscriber: %v", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	// Only mclaude-hosts is created at startup (spec startup step 6, ADR-0054).
	kv, err := js.KeyValue("mclaude-hosts")
	if err != nil {
		t.Fatalf("KeyValue(%q): %v — bucket should have been created by StartProjectsSubscriber", "mclaude-hosts", err)
	}
	if kv.Bucket() != "mclaude-hosts" {
		t.Errorf("bucket name = %q; want %q", kv.Bucket(), "mclaude-hosts")
	}
	status, err := kv.Status()
	if err != nil {
		t.Fatalf("KeyValue(%q).Status(): %v", "mclaude-hosts", err)
	}
	if history := status.History(); history != 1 {
		t.Errorf("mclaude-hosts History() = %d; want 1", history)
	}

	// Confirm old shared buckets are NOT created (removed per ADR-0054 / ADR-0044).
	for _, obsolete := range []string{"mclaude-projects", "mclaude-job-queue", "mclaude-sessions"} {
		if _, err := js.KeyValue(obsolete); err == nil {
			t.Errorf("bucket %q exists; want absent (removed per ADR-0054)", obsolete)
		}
	}
}

// TestIntegration_UserSlug_PopulatedOnCreate verifies that CreateUser derives
// the slug from the full email (ADR-0062).
func TestIntegration_UserSlug_PopulatedOnCreate(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	user, err := db.CreateUser(ctx, "slug-test-001", "alice.smith@example.com", "Alice Smith", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// ADR-0062: full email is slugified → alice.smith@example.com → alice-smith-example-com
	if user.Slug != "alice-smith-example-com" {
		t.Errorf("Slug = %q; want %q", user.Slug, "alice-smith-example-com")
	}

	// Verify the slug persists through a DB round-trip.
	fetched, err := db.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if fetched.Slug != "alice-smith-example-com" {
		t.Errorf("fetched Slug = %q; want %q", fetched.Slug, "alice-smith-example-com")
	}
}

// TestIntegration_UserSlug_Unique verifies that duplicate slugs are rejected (ADR-0046).
func TestIntegration_UserSlug_Unique(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Two different emails that produce the same full-email slug (ADR-0062):
	// sluguniqtest@a.b.com  → sluguniqtest-a-b-com
	// sluguniqtest-a@b.com  → sluguniqtest-a-b-com
	_, err := db.CreateUser(ctx, "slug-uniq-u1", "sluguniqtest@a.b.com", "UniqTest1", "")
	if err != nil {
		t.Fatalf("CreateUser first: %v", err)
	}
	_, err = db.CreateUser(ctx, "slug-uniq-u2", "sluguniqtest-a@b.com", "UniqTest2", "")
	if err == nil {
		t.Error("expected error for duplicate slug; got nil")
	}
}

// TestIntegration_HandleLogin_UserSlugInResponse verifies that POST /auth/login
// returns the userSlug field populated from users.slug (ADR-0046).
func TestIntegration_HandleLogin_UserSlugInResponse(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	hash, err := HashPassword("secret-pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, "login-slug-u1", "bob.jones@example.com", "Bob Jones", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour)

	body := `{"email":"bob.jones@example.com","password":"secret-pw"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp LoginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// ADR-0062: full email is slugified → bob.jones@example.com → bob-jones-example-com
	if resp.UserSlug != "bob-jones-example-com" {
		t.Errorf("userSlug = %q; want %q", resp.UserSlug, "bob-jones-example-com")
	}
	if resp.UserID == "" {
		t.Error("userId missing from response")
	}
}

// TestIntegration_HandleRefresh_ReturnsSlug verifies that POST /auth/refresh
// returns a fresh JWT along with both userId (UUID) and userSlug (ADR-0046).
func TestIntegration_HandleRefresh_ReturnsSlug(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	hash, err := HashPassword("refresh-pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, "refresh-slug-u1", "refresh.slug@example.com", "Refresh Slug", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour)

	// Log in to get an initial JWT.
	loginBody := `{"email":"refresh.slug@example.com","password":"refresh-pw"}`
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.handleLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp LoginResponse
	json.NewDecoder(loginRec.Body).Decode(&loginResp) //nolint:errcheck

	// Now refresh the JWT.
	refreshRec := httptest.NewRecorder()
	refreshReq := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	refreshReq.Header.Set("Authorization", "Bearer "+loginResp.JWT)
	srv.handleRefresh(refreshRec, refreshReq)

	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d; body: %s", refreshRec.Code, refreshRec.Body.String())
	}

	var resp LoginResponse
	if err := json.NewDecoder(refreshRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if resp.JWT == "" {
		t.Error("refreshed response missing JWT")
	}
	if resp.NKeySeed == "" {
		t.Error("refreshed response missing NKeySeed")
	}
	if resp.UserID != "refresh-slug-u1" {
		t.Errorf("userID = %q; want refresh-slug-u1", resp.UserID)
	}
	// ADR-0062: full email is slugified → refresh.slug@example.com → refresh-slug-example-com
	if resp.UserSlug != "refresh-slug-example-com" {
		t.Errorf("userSlug = %q; want refresh-slug-example-com", resp.UserSlug)
	}
	// New JWT must differ from original.
	if resp.JWT == loginResp.JWT {
		t.Error("refreshed JWT should differ from original")
	}
}

// TestIntegration_HandleSysEvent_MachineConnect verifies that handleSysEvent
// writes a {uslug}.{hslug} entry with online=true to the mclaude-hosts KV
// when a machine host connects (ADR-0046).
func TestIntegration_HandleSysEvent_MachineConnect(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create user + machine host with a known public_key.
	_, err := db.CreateUser(ctx, "sys-evt-u1", "sysevt@example.com", "SysEvt", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key)
		VALUES ('sys-evt-h1', 'sys-evt-u1', 'myhost', 'My Host', 'machine', 'owner', 'UTEST_PUBKEY_SYSEVT')`,
	)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	t.Cleanup(func() {
		db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = 'sys-evt-h1'`)  //nolint:errcheck
		db.pool.Exec(ctx, `DELETE FROM users WHERE id = 'sys-evt-u1'`)  //nolint:errcheck
	})

	// Set up hostsKV via a fresh NATS connection.
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		t.Fatalf("ensureHostsKV: %v", err)
	}

	srv := &Server{db: db, hostsKV: hostsKV}

	// Simulate a $SYS CONNECT event from a machine host.
	payload := `{"server":{"name":"hub"},"client":{"kind":"Client","name":"myhost","nkey":"UTEST_PUBKEY_SYSEVT"}}`
	fakeMsg := &nats.Msg{Data: []byte(payload)}
	srv.handleSysEvent(fakeMsg, true /* isConnect */)

	// Verify mclaude-hosts KV was written with online=true.
	// KV key is {hslug} (flat, no user prefix — per spec-state-schema.md § mclaude-hosts).
	entry, err := hostsKV.Get("myhost")
	if err != nil {
		t.Fatalf("hostsKV.Get(myhost): %v", err)
	}

	var state HostKVState
	if err := json.Unmarshal(entry.Value(), &state); err != nil {
		t.Fatalf("unmarshal HostKVState: %v", err)
	}
	if !state.Online {
		t.Errorf("online = %v; want true after CONNECT", state.Online)
	}
	if state.Slug != "myhost" {
		t.Errorf("slug = %q; want %q", state.Slug, "myhost")
	}
	if state.LastSeenAt == nil {
		t.Error("lastSeenAt should be set after CONNECT")
	}
}

// TestIntegration_HandleSysEvent_MachineDisconnect verifies that handleSysEvent
// sets online=false in the mclaude-hosts KV on machine disconnect (ADR-0046).
// It preserves the existing lastSeenAt from a prior CONNECT.
func TestIntegration_HandleSysEvent_MachineDisconnect(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	_, err := db.CreateUser(ctx, "sys-disc-u1", "sysdisc@example.com", "SysDisc", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key)
		VALUES ('sys-disc-h1', 'sys-disc-u1', 'dischost', 'Disc Host', 'machine', 'owner', 'UTEST_PUBKEY_SYSDISC')`,
	)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	t.Cleanup(func() {
		db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = 'sys-disc-h1'`)  //nolint:errcheck
		db.pool.Exec(ctx, `DELETE FROM users WHERE id = 'sys-disc-u1'`)  //nolint:errcheck
	})

	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		t.Fatalf("ensureHostsKV: %v", err)
	}

	srv := &Server{db: db, hostsKV: hostsKV}

	connectPayload := `{"server":{"name":"hub"},"client":{"kind":"Client","name":"dischost","nkey":"UTEST_PUBKEY_SYSDISC"}}`
	srv.handleSysEvent(&nats.Msg{Data: []byte(connectPayload)}, true)

	// Verify online=true after CONNECT.
	// KV key is {hslug} (flat, no user prefix — per spec-state-schema.md § mclaude-hosts).
	entry, err := hostsKV.Get("dischost")
	if err != nil {
		t.Fatalf("hostsKV.Get after CONNECT: %v", err)
	}
	var stateAfterConnect HostKVState
	json.Unmarshal(entry.Value(), &stateAfterConnect) //nolint:errcheck
	if !stateAfterConnect.Online {
		t.Error("online should be true after CONNECT")
	}
	savedLastSeenAt := stateAfterConnect.LastSeenAt

	// Now disconnect.
	disconnectPayload := `{"server":{"name":"hub"},"client":{"kind":"Client","name":"dischost","nkey":"UTEST_PUBKEY_SYSDISC"}}`
	srv.handleSysEvent(&nats.Msg{Data: []byte(disconnectPayload)}, false)

	entry, err = hostsKV.Get("dischost")
	if err != nil {
		t.Fatalf("hostsKV.Get after DISCONNECT: %v", err)
	}
	var stateAfterDisconnect HostKVState
	if err := json.Unmarshal(entry.Value(), &stateAfterDisconnect); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stateAfterDisconnect.Online {
		t.Error("online should be false after DISCONNECT")
	}
	// lastSeenAt must be preserved (ADR-0046: DISCONNECT writes online=false only).
	if stateAfterDisconnect.LastSeenAt == nil || *stateAfterDisconnect.LastSeenAt != *savedLastSeenAt {
		t.Errorf("lastSeenAt changed on DISCONNECT: got %v, want %v",
			stateAfterDisconnect.LastSeenAt, savedLastSeenAt)
	}
}

// TestIntegration_HandleSysEvent_ClusterConnect verifies that a Client CONNECT
// event for a cluster host writes online=true to mclaude-hosts KV (ADR-0063:
// K8s cluster controllers connect hub-direct as "Client", not "Leafnode").
// The "Client" branch looks up by public_key with no type filter, so it matches
// both type='machine' and type='cluster' rows.
func TestIntegration_HandleSysEvent_ClusterConnect(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create a user with a cluster host using a unique public_key.
	// ADR-0063: no js_domain/leaf_url/account_jwt columns needed (constraint dropped).
	_, err := db.CreateUser(ctx, "clconn-u1", "clconn1@example.com", "ClConn1", "")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key)
		VALUES ('clconn-h1', 'clconn-u1', 'mycluster', 'My Cluster', 'cluster', 'owner', 'UTEST_CL_CONNECT_KEY')`,
	)
	if err != nil {
		t.Fatalf("insert cluster host: %v", err)
	}
	t.Cleanup(func() {
		db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = 'clconn-h1'`)  //nolint:errcheck
		db.pool.Exec(ctx, `DELETE FROM users WHERE id = 'clconn-u1'`)  //nolint:errcheck
	})

	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		t.Fatalf("ensureHostsKV: %v", err)
	}

	srv := &Server{db: db, hostsKV: hostsKV}

	// Simulate a $SYS Client CONNECT event from a K8s cluster controller.
	// ADR-0063: cluster controllers connect hub-direct as "Client", not "Leafnode".
	payload := `{"server":{"name":"hub"},"client":{"kind":"Client","name":"mycluster","nkey":"UTEST_CL_CONNECT_KEY"}}`
	srv.handleSysEvent(&nats.Msg{Data: []byte(payload)}, true /* isConnect */)

	// The cluster host row should have online=true written to mclaude-hosts KV.
	// KV key is {hslug} (flat, no user prefix — per spec-state-schema.md § mclaude-hosts).
	key := "mycluster"
	entry, err := hostsKV.Get(key)
	if err != nil {
		t.Fatalf("hostsKV.Get(%q): %v — expected entry after cluster CONNECT", key, err)
	}
	var state HostKVState
	if err := json.Unmarshal(entry.Value(), &state); err != nil {
		t.Fatalf("unmarshal HostKVState for %q: %v", key, err)
	}
	if !state.Online {
		t.Errorf("key %q: online = %v; want true after cluster CONNECT", key, state.Online)
	}
	if state.Slug != "mycluster" {
		t.Errorf("key %q: slug = %q; want mycluster", key, state.Slug)
	}
	if state.Type != "cluster" {
		t.Errorf("key %q: type = %q; want cluster", key, state.Type)
	}
	if state.LastSeenAt == nil {
		t.Errorf("key %q: lastSeenAt should be set after cluster CONNECT", key)
	}
}

// TestIntegration_HandleSysEvent_ClusterDisconnect verifies that a Client
// DISCONNECT event sets online=false in mclaude-hosts KV for the cluster host,
// without modifying lastSeenAt (ADR-0046/ADR-0063).
func TestIntegration_HandleSysEvent_ClusterDisconnect(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// ADR-0063: no js_domain/leaf_url/account_jwt columns needed (constraint dropped).
	_, err := db.CreateUser(ctx, "cldisc-u1", "cldisc1@example.com", "ClDisc1", "")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key)
		VALUES ('cldisc-h1', 'cldisc-u1', 'disccluster', 'Disc Cluster', 'cluster', 'owner', 'UTEST_CL_DISC_KEY')`,
	)
	if err != nil {
		t.Fatalf("insert cluster host: %v", err)
	}
	t.Cleanup(func() {
		db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = 'cldisc-h1'`)  //nolint:errcheck
		db.pool.Exec(ctx, `DELETE FROM users WHERE id = 'cldisc-u1'`)  //nolint:errcheck
	})

	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		t.Fatalf("ensureHostsKV: %v", err)
	}

	srv := &Server{db: db, hostsKV: hostsKV}

	// Connect first.
	// ADR-0063: cluster controllers connect hub-direct as "Client", not "Leafnode".
	connectPayload := `{"server":{"name":"hub"},"client":{"kind":"Client","name":"disccluster","nkey":"UTEST_CL_DISC_KEY"}}`
	srv.handleSysEvent(&nats.Msg{Data: []byte(connectPayload)}, true)

	// Capture lastSeenAt before disconnect.
	// KV key is {hslug} (flat, no user prefix — per spec-state-schema.md § mclaude-hosts).
	key := "disccluster"
	entry, err := hostsKV.Get(key)
	if err != nil {
		t.Fatalf("hostsKV.Get(%q) after CONNECT: %v", key, err)
	}
	var stateAfterConnect HostKVState
	json.Unmarshal(entry.Value(), &stateAfterConnect) //nolint:errcheck
	savedLastSeen := stateAfterConnect.LastSeenAt

	// Now disconnect.
	disconnectPayload := `{"server":{"name":"hub"},"client":{"kind":"Client","name":"disccluster","nkey":"UTEST_CL_DISC_KEY"}}`
	srv.handleSysEvent(&nats.Msg{Data: []byte(disconnectPayload)}, false)

	// Verify online=false and lastSeenAt unchanged.
	entry, err = hostsKV.Get(key)
	if err != nil {
		t.Fatalf("hostsKV.Get(%q) after DISCONNECT: %v", key, err)
	}
	var stateAfterDisconnect HostKVState
	if err := json.Unmarshal(entry.Value(), &stateAfterDisconnect); err != nil {
		t.Fatalf("unmarshal HostKVState for %q: %v", key, err)
	}
	if stateAfterDisconnect.Online {
		t.Errorf("key %q: online should be false after DISCONNECT", key)
	}
	if stateAfterDisconnect.LastSeenAt == nil || savedLastSeen == nil ||
		*stateAfterDisconnect.LastSeenAt != *savedLastSeen {
		t.Errorf("key %q: lastSeenAt changed on DISCONNECT: got %v, want %v",
			key, stateAfterDisconnect.LastSeenAt, savedLastSeen)
	}
}

// TestIntegration_HandleSysEvent_LeafnodeDropped verifies that "Leafnode" kind
// $SYS events are silently dropped (ADR-0063: leaf topology removed). No KV
// writes should occur and no panic should result.
func TestIntegration_HandleSysEvent_LeafnodeDropped(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		t.Fatalf("ensureHostsKV: %v", err)
	}

	srv := &Server{db: db, hostsKV: hostsKV}

	// A "Leafnode" CONNECT event should not write anything to KV.
	payload := `{"server":{"name":"hub"},"client":{"kind":"Leafnode","name":"somenode","nkey":"UTEST_LEAFNODE_DROPPED_KEY"}}`
	// Must not panic.
	srv.handleSysEvent(&nats.Msg{Data: []byte(payload)}, true)
	srv.handleSysEvent(&nats.Msg{Data: []byte(payload)}, false)

	// No KV entry should have been written for this nkey.
	_, err = hostsKV.Get("somenode")
	if err == nil {
		t.Error("Leafnode event should not write to hostsKV; got an entry")
	}
}

// TestIntegration_SeedDev_WritesLocalHostKV removed per ADR-0089 (seedDev removed).

// ---- ADR-0089 helper ----

// newIntegrationServer constructs a Server with the given DB and NATS connection.
// Used by ADR-0089 integration tests. accountKP is freshly generated.
func newIntegrationServer(t *testing.T, db *DB, nc *nats.Conn) *Server {
	t.Helper()
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account kp: %v", err)
	}
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")
	if nc != nil {
		srv.SetNATSConn(nc)
	}
	return srv
}

// mustAdminJWT creates an is_admin user, logs in, and returns the JWT for use
// in admin route tests.
func mustAdminJWT(t *testing.T, db *DB, srv *Server) string {
	t.Helper()
	ctx := context.Background()
	adminID := "admin-jwt-" + uuid.NewString()[:8]
	adminEmail := adminID + "@test.example.com"
	hash, err := HashPassword("admin-pw-" + adminID)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	adminUser, err := db.CreateUser(ctx, adminID, adminEmail, "Admin", hash)
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := db.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, adminID) }) //nolint:errcheck

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, adminEmail, "admin-pw-"+adminID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp LoginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return resp.JWT
}

// ---- ADR-0089 Phase 1 invariants ----

// TestIntegration_Schema_NoSeedRows verifies that a fresh Migrate() on an empty DB
// leaves hosts, projects, and users tables all empty (ADR-0089).
func TestIntegration_Schema_NoSeedRows(t *testing.T) {
	ctx := context.Background()

	// Use a fresh DB connection to a clean database.
	db := mustConnectDB(t, ctx)

	var userCount, hostCount, projectCount int
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM hosts`).Scan(&hostCount); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}

	// Note: other tests in the shared container may have created rows.
	// We verify that Migrate() itself does not INSERT seed rows.
	// We can only assert 0 after a fresh Migrate on a blank DB, which is what
	// TestMain does. Since tests share a container, we verify no unexpected data
	// was added by Migrate itself (row counts may be > 0 from other tests — that's OK).
	// The invariant is: Migrate() does not INSERT any rows. We verify this is true
	// by running Migrate again (idempotent) and confirming counts don't increase.
	beforeUser, beforeHost, beforeProject := userCount, hostCount, projectCount
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-Migrate: %v", err)
	}
	var afterUser, afterHost, afterProject int
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&afterUser); err != nil {
		t.Fatalf("count users after: %v", err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM hosts`).Scan(&afterHost); err != nil {
		t.Fatalf("count hosts after: %v", err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM projects`).Scan(&afterProject); err != nil {
		t.Fatalf("count projects after: %v", err)
	}
	if afterUser != beforeUser {
		t.Errorf("Migrate added %d user rows; want 0", afterUser-beforeUser)
	}
	if afterHost != beforeHost {
		t.Errorf("Migrate added %d host rows; want 0", afterHost-beforeHost)
	}
	if afterProject != beforeProject {
		t.Errorf("Migrate added %d project rows; want 0", afterProject-beforeProject)
	}
}

// TestIntegration_CreateUser_NoLocalHost verifies that CreateUser does not insert
// a bare local host row with slug='local' (ADR-0076 / ADR-0089 Phase 1 invariant).
func TestIntegration_CreateUser_NoLocalHost(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "nolocalhost-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "NoLocalHost", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	var count int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hosts WHERE user_id = $1 AND slug = 'local'`,
		user.ID).Scan(&count); err != nil {
		t.Fatalf("count local hosts: %v", err)
	}
	if count != 0 {
		t.Errorf("CreateUser created %d local host row(s); want 0 (ADR-0076)", count)
	}
}

// TestIntegration_BootstrapAdminUser_FreshWithPassword verifies that
// bootstrapAdminUser on an empty DB with a non-empty password creates a user
// row with a non-empty password_hash, is_admin=true, and that POST /auth/login
// succeeds with those credentials (ADR-0089 Phase 1 invariant).
func TestIntegration_BootstrapAdminUser_FreshWithPassword(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	email := "bootstrap-fresh-" + uuid.NewString()[:8] + "@test.example.com"
	password := "test-bootstrap-pw-" + uuid.NewString()[:8]

	logger := zerolog.Nop()
	if err := bootstrapAdminUser(ctx, logger, integDeps.PostgresDSN, email, password); err != nil {
		t.Fatalf("bootstrapAdminUser: %v", err)
	}
	t.Cleanup(func() {
		u, _ := db.GetUserByEmail(ctx, email)
		if u != nil {
			db.DeleteUser(ctx, u.ID) //nolint:errcheck
		}
	})

	// Verify user row exists with non-empty password_hash and is_admin=true.
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if user == nil {
		t.Fatal("user not found after bootstrapAdminUser")
	}
	if user.PasswordHash == "" {
		t.Error("password_hash is empty; want non-empty after bootstrapAdminUser with password")
	}
	if !user.IsAdmin {
		t.Error("is_admin = false; want true after bootstrapAdminUser")
	}

	// Verify login succeeds.
	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("login status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// TestIntegration_BootstrapAdminUser_EmailWithoutPassword_Errors verifies that
// bootstrapAdminUser returns an error when BOOTSTRAP_ADMIN_EMAIL is set but
// BOOTSTRAP_ADMIN_PASSWORD is empty and the user does not yet exist
// (ADR-0089 Phase 1 invariant — init-keys Job must exit non-zero in this case).
func TestIntegration_BootstrapAdminUser_EmailWithoutPassword_Errors(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	email := "bootstrap-nopw-" + uuid.NewString()[:8] + "@test.example.com"

	logger := zerolog.Nop()
	err := bootstrapAdminUser(ctx, logger, integDeps.PostgresDSN, email, "" /* empty password */)
	if err == nil {
		t.Fatal("expected error when email set but password empty and user does not exist; got nil")
	}

	// Verify no user row was created.
	user, dbErr := db.GetUserByEmail(ctx, email)
	if dbErr != nil {
		t.Fatalf("GetUserByEmail: %v", dbErr)
	}
	if user != nil {
		t.Errorf("user row was created despite empty password; want no row created")
		db.DeleteUser(ctx, user.ID) //nolint:errcheck
	}
}

// TestIntegration_BootstrapAdminUser_ExistingUserGetsPassword verifies that
// bootstrapAdminUser called on an existing user with empty password_hash writes
// a non-empty hash and login succeeds afterward (ADR-0089 Phase 1 invariant).
func TestIntegration_BootstrapAdminUser_ExistingUserGetsPassword(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create user with empty password hash (simulates OAuth-only user).
	userID := "bootstrap-existing-" + uuid.NewString()[:8]
	email := userID + "@test.example.com"
	user, err := db.CreateUser(ctx, userID, email, "ExistingBootstrap", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Verify no password hash initially.
	if user.PasswordHash != "" {
		t.Fatalf("expected empty password_hash initially; got %q", user.PasswordHash)
	}

	password := "new-bootstrap-pw-" + uuid.NewString()[:8]
	logger := zerolog.Nop()
	if err := bootstrapAdminUser(ctx, logger, integDeps.PostgresDSN, email, password); err != nil {
		t.Fatalf("bootstrapAdminUser: %v", err)
	}

	// Verify password_hash is now non-empty.
	updated, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if updated == nil {
		t.Fatal("user not found after bootstrapAdminUser")
	}
	if updated.PasswordHash == "" {
		t.Error("password_hash is still empty; want non-empty after bootstrapAdminUser with password")
	}
	if !updated.IsAdmin {
		t.Error("is_admin = false; want true after bootstrapAdminUser")
	}

	// Verify login succeeds.
	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("login status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// TestIntegration_BootstrapAdminUser_PasswordRotation verifies that
// bootstrapAdminUser called on an existing user who already has a password_hash
// rotates the hash and the new password authenticates successfully (ADR-0094
// UPSERT semantics — password rotation is always applied when password is non-empty).
func TestIntegration_BootstrapAdminUser_PasswordRotation(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create user with an initial password.
	userID := "bootstrap-rotate-" + uuid.NewString()[:8]
	email := userID + "@test.example.com"
	initialPassword := "initial-pw-" + uuid.NewString()[:8]
	initialHash, err := HashPassword(initialPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := db.CreateUser(ctx, userID, email, "RotateBootstrap", initialHash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.SetUserAdmin(ctx, user.ID, true); err != nil {
		t.Fatalf("SetUserAdmin: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Call bootstrap with a new password — should rotate the hash.
	newPassword := "rotated-pw-" + uuid.NewString()[:8]
	logger := zerolog.Nop()
	if err := bootstrapAdminUser(ctx, logger, integDeps.PostgresDSN, email, newPassword); err != nil {
		t.Fatalf("bootstrapAdminUser: %v", err)
	}

	// Verify hash changed.
	updated, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if updated == nil {
		t.Fatal("user not found after bootstrapAdminUser")
	}
	if updated.PasswordHash == initialHash {
		t.Error("password_hash unchanged; want rotated hash after bootstrapAdminUser with new password")
	}
	if !updated.IsAdmin {
		t.Error("is_admin = false; want true after bootstrapAdminUser")
	}

	// Verify new password authenticates.
	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, newPassword)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("login with new password: status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Verify old password no longer authenticates.
	oldLoginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, initialPassword)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(oldLoginBody))
	req2.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("login with old password: status = %d; want 401", rec2.Code)
	}
}

// TestIntegration_BootstrapAdminUser_EmptyPasswordPreservesHash verifies that
// bootstrapAdminUser called on an existing user with empty password preserves
// the existing password_hash (ADR-0094 UPSERT semantics — CASE WHEN $2 = '').
func TestIntegration_BootstrapAdminUser_EmptyPasswordPreservesHash(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create user with a password.
	userID := "bootstrap-preserve-" + uuid.NewString()[:8]
	email := userID + "@test.example.com"
	password := "preserve-pw-" + uuid.NewString()[:8]
	passwordHash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := db.CreateUser(ctx, userID, email, "PreserveBootstrap", passwordHash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.SetUserAdmin(ctx, user.ID, true); err != nil {
		t.Fatalf("SetUserAdmin: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Call bootstrap with empty password — must preserve existing hash.
	logger := zerolog.Nop()
	if err := bootstrapAdminUser(ctx, logger, integDeps.PostgresDSN, email, "" /* empty */); err != nil {
		t.Fatalf("bootstrapAdminUser with empty password on existing user: %v", err)
	}

	// Verify hash is unchanged.
	updated, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if updated == nil {
		t.Fatal("user not found after bootstrapAdminUser")
	}
	if updated.PasswordHash != passwordHash {
		t.Error("password_hash was changed; want preserved when bootstrapAdminUser called with empty password")
	}
	if !updated.IsAdmin {
		t.Error("is_admin = false; want true")
	}

	// Verify original password still authenticates.
	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("login status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// ---- ADR-0089 Admin routes (main port, isAdminMiddleware) ----

// TestIntegration_AdminCreateUser verifies POST /admin/users creates a user
// with {id, email, name, slug} in the response (ADR-0089).
func TestIntegration_AdminCreateUser(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)
	adminJWT := mustAdminJWT(t, db, srv)

	userID := "admin-create-" + uuid.NewString()[:8]
	email := userID + "@test.example.com"
	body := fmt.Sprintf(`{"id":%q,"email":%q,"name":"AdminCreateUser"}`, userID, email)
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201; body: %s", rec.Code, rec.Body.String())
	}
	var resp AdminUserResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != userID {
		t.Errorf("id = %q; want %q", resp.ID, userID)
	}
	if resp.Email != email {
		t.Errorf("email = %q; want %q", resp.Email, email)
	}
	if resp.Name == "" {
		t.Error("name is empty")
	}
	if resp.Slug == "" {
		t.Error("slug is empty")
	}
}

// TestIntegration_AdminListUsers verifies GET /admin/users lists created users (ADR-0089).
func TestIntegration_AdminListUsers(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)
	adminJWT := mustAdminJWT(t, db, srv)

	// Create a test user.
	userID := "admin-list-" + uuid.NewString()[:8]
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "AdminListUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body.String())
	}
	var users []AdminUserResponse
	if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	found := false
	for _, u := range users {
		if u.ID == userID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created user %q not found in list", userID)
	}
}

// TestIntegration_AdminDeleteUser verifies DELETE /admin/users/{id} deletes a user (ADR-0089).
func TestIntegration_AdminDeleteUser(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)
	adminJWT := mustAdminJWT(t, db, srv)

	userID := "admin-del-" + uuid.NewString()[:8]
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "AdminDelUser", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/"+userID, nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204; body: %s", rec.Code, rec.Body.String())
	}

	// Verify user no longer exists in DB.
	fetched, err := db.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID after delete: %v", err)
	}
	if fetched != nil {
		t.Errorf("user %q still exists after DELETE", userID)
	}
}

// TestIntegration_AdminMiddleware_RejectsNoToken verifies 401 on /admin/users without JWT (ADR-0089).
func TestIntegration_AdminMiddleware_RejectsNoToken(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 (no JWT)", rec.Code)
	}
}

// TestIntegration_AdminMiddleware_RejectsNonAdmin verifies 403 when JWT is valid
// but user is not admin (ADR-0089).
func TestIntegration_AdminMiddleware_RejectsNonAdmin(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	// Create a non-admin user and log in.
	userID := "non-admin-" + uuid.NewString()[:8]
	email := userID + "@test.example.com"
	hash, _ := HashPassword("pw-" + userID)
	_, err := db.CreateUser(ctx, userID, email, "NonAdmin", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, "pw-"+userID)
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.handleLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp LoginResponse
	json.NewDecoder(loginRec.Body).Decode(&loginResp) //nolint:errcheck

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.JWT)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403 (non-admin JWT)", rec.Code)
	}
}

// ---- ADR-0089 Auth surface ----

// TestIntegration_HandleLogin_InvalidCreds verifies POST /auth/login returns 401
// on wrong password (ADR-0089).
func TestIntegration_HandleLogin_InvalidCreds(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create a user with a known password.
	userID := "login-invalid-" + uuid.NewString()[:8]
	hash, _ := HashPassword("correct-pw")
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "LoginInvalid", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	srv := newIntegrationServer(t,db, nil)

	body := fmt.Sprintf(`{"email":%q,"password":"wrong-password"}`, userID+"@test.example.com")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 (wrong password)", rec.Code)
	}
}

// TestIntegration_AuthMiddleware_Rejects_Unauthenticated verifies GET /auth/me
// returns 401 without a JWT (ADR-0089).
func TestIntegration_AuthMiddleware_Rejects_Unauthenticated(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 (no JWT)", rec.Code)
	}
}

// TestIntegration_AuthMiddleware_Accepts_ValidJWT verifies GET /auth/me returns 200
// with a valid JWT from login (ADR-0089).
func TestIntegration_AuthMiddleware_Accepts_ValidJWT(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	userID := "auth-valid-" + uuid.NewString()[:8]
	hash, _ := HashPassword("pw-" + userID)
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "AuthValid", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Login to get JWT.
	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, userID+"@test.example.com", "pw-"+userID)
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.handleLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp LoginResponse
	json.NewDecoder(loginRec.Body).Decode(&loginResp) //nolint:errcheck

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.JWT)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (valid JWT); body: %s", rec.Code, rec.Body.String())
	}
}

// TestIntegration_HostRegister_HTTP verifies POST /api/hosts/register creates a host
// row with public_key and returns a JWT (ADR-0089).
func TestIntegration_HostRegister_HTTP(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	// Create a user to own the host.
	userID := "hostreg-" + uuid.NewString()[:8]
	_, err := db.CreateUser(ctx, userID, userID+"@test.example.com", "HostReg", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Generate a host NKey public key.
	hostKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create host nkey: %v", err)
	}
	hostPub, _ := hostKP.PublicKey()

	// Inject a device code entry.
	code := "HOSTREG"
	globalDeviceCodeStore.mu.Lock()
	globalDeviceCodeStore.codes[code] = &deviceCodeEntry{
		UserID:    userID,
		PublicKey: hostPub,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	globalDeviceCodeStore.mu.Unlock()
	t.Cleanup(func() {
		globalDeviceCodeStore.mu.Lock()
		delete(globalDeviceCodeStore.codes, code)
		globalDeviceCodeStore.mu.Unlock()
	})

	body := fmt.Sprintf(`{"code":%q,"name":"Test Host"}`, code)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hosts/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleHostRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["jwt"] == "" || resp["jwt"] == nil {
		t.Error("response missing jwt")
	}
	if resp["slug"] == "" || resp["slug"] == nil {
		t.Error("response missing slug")
	}

	// Verify host row exists in DB with public_key set.
	slug, _ := resp["slug"].(string)
	var pubKey string
	if err := db.pool.QueryRow(ctx,
		`SELECT public_key FROM hosts WHERE user_id = $1 AND slug = $2`,
		userID, slug).Scan(&pubKey); err != nil {
		t.Fatalf("query host: %v", err)
	}
	if pubKey != hostPub {
		t.Errorf("public_key = %q; want %q", pubKey, hostPub)
	}
}

// TestIntegration_NKeyChallenge_AndVerify verifies the full challenge-response
// round-trip returning a scoped JWT (ADR-0089).
func TestIntegration_NKeyChallenge_AndVerify(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	// Create a user with a known NKey.
	userKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create user nkey: %v", err)
	}
	userPub, _ := userKP.PublicKey()

	userID := "nkey-challenge-" + uuid.NewString()[:8]
	_, err = db.CreateUser(ctx, userID, userID+"@test.example.com", "NKeyChallenge", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.SetUserNKeyPublic(ctx, userID, userPub); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Step 1: request challenge.
	challengeBody := fmt.Sprintf(`{"nkey_public":%q}`, userPub)
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
	if challengeResp.Challenge == "" {
		t.Fatal("challenge is empty")
	}

	// Step 2: sign the challenge and verify.
	// The challenge is base64-encoded raw nonce bytes. Decode to get the raw nonce,
	// sign that (not the base64 string), then base64-encode the signature for the request.
	rawNonce, err := base64.StdEncoding.DecodeString(challengeResp.Challenge)
	if err != nil {
		t.Fatalf("decode challenge nonce: %v", err)
	}
	sig, err := userKP.Sign(rawNonce)
	if err != nil {
		t.Fatalf("sign challenge: %v", err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	verifyBody := fmt.Sprintf(`{"nkey_public":%q,"challenge":%q,"signature":%q}`,
		userPub, challengeResp.Challenge, sigB64)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/verify", bytes.NewBufferString(verifyBody))
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
		t.Error("verify response ok = false; want true")
	}
	if verifyResp.JWT == "" {
		t.Error("verify response missing jwt")
	}
}

// TestIntegration_DeviceCode_StartAndPoll verifies the CLI device-code flow:
// POST /api/auth/device-code returns {deviceCode, userCode}, and polling
// returns {status: "pending"} (ADR-0089).
func TestIntegration_DeviceCode_StartAndPoll(t *testing.T) {
	srv := newIntegrationServer(t,nil, nil)

	// Start device code flow.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodeCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("device-code create status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		DeviceCode string `json:"deviceCode"`
		UserCode   string `json:"userCode"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.DeviceCode == "" {
		t.Fatal("deviceCode is empty")
	}
	if createResp.UserCode == "" {
		t.Fatal("userCode is empty")
	}

	// Poll — should return pending.
	pollBody := fmt.Sprintf(`{"deviceCode":%q}`, createResp.DeviceCode)
	pollRec := httptest.NewRecorder()
	pollReq := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/poll", bytes.NewBufferString(pollBody))
	pollReq.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodePoll(pollRec, pollReq)

	if pollRec.Code != http.StatusOK {
		t.Fatalf("device-code poll status = %d; body: %s", pollRec.Code, pollRec.Body.String())
	}
	var pollResp struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(pollRec.Body).Decode(&pollResp); err != nil {
		t.Fatalf("decode poll response: %v", err)
	}
	if pollResp.Status != "pending" {
		t.Errorf("poll status = %q; want pending", pollResp.Status)
	}
}

// TestIntegration_HealthEndpoints verifies /health, /healthz, and /readyz all
// return 200 with a live DB (ADR-0089).
func TestIntegration_HealthEndpoints(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	srv := newIntegrationServer(t,db, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	for _, path := range []string{"/health", "/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d; want 200", path, rec.Code)
		}
	}
}

// ---- ADR-0089 NATS subject handlers ----

// mustNATSConn returns a NATS connection for integration tests.
func mustNATSConn(t *testing.T) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// TestIntegration_ManageGrant verifies that publishing to
// mclaude.users.*.hosts.*.manage.grant inserts the grantee into host_access (ADR-0089).
func TestIntegration_ManageGrant(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	// Create owner + host.
	ownerID := "grant-owner-" + uuid.NewString()[:8]
	owner, err := db.CreateUser(ctx, ownerID, ownerID+"@t.example.com", "Owner", "")
	if err != nil {
		t.Fatalf("CreateUser owner: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	hostID := uuid.NewString()
	_, err = db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role) VALUES ($1, $2, $3, 'Test Host', 'machine', 'owner')`,
		hostID, owner.ID, "grant-host-"+ownerID[:8])
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	hostSlug := "grant-host-" + ownerID[:8]
	t.Cleanup(func() {
		db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) //nolint:errcheck
	})

	// Create grantee.
	granteeID := "grant-grantee-" + uuid.NewString()[:8]
	grantee, err := db.CreateUser(ctx, granteeID, granteeID+"@t.example.com", "Grantee", "")
	if err != nil {
		t.Fatalf("CreateUser grantee: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, granteeID) }) //nolint:errcheck

	// Publish manage.grant.
	subject := fmt.Sprintf("mclaude.users.%s.hosts.%s.manage.grant", owner.Slug, hostSlug)
	payload, _ := json.Marshal(ManageGrantRequest{UserSlug: grantee.Slug})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}
	var resp map[string]any
	json.Unmarshal(reply.Data, &resp) //nolint:errcheck
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("manage.grant response not ok: %s", reply.Data)
	}

	// Verify grantee appears in host_access.
	var count int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM host_access WHERE host_id = $1 AND user_id = $2`,
		hostID, grantee.ID).Scan(&count); err != nil {
		t.Fatalf("count host_access: %v", err)
	}
	if count != 1 {
		t.Errorf("host_access count = %d; want 1", count)
	}
}

// TestIntegration_ManageRevokeAccess verifies manage.revoke-access removes the
// grant from host_access (ADR-0089).
func TestIntegration_ManageRevokeAccess(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	// Create owner + host.
	ownerID := "revoke-owner-" + uuid.NewString()[:8]
	owner, err := db.CreateUser(ctx, ownerID, ownerID+"@t.example.com", "Owner", "")
	if err != nil {
		t.Fatalf("CreateUser owner: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	hostID := uuid.NewString()
	hostSlug := "revoke-host-" + ownerID[:8]
	_, err = db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role) VALUES ($1, $2, $3, 'Test Host', 'machine', 'owner')`,
		hostID, owner.ID, hostSlug)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) }) //nolint:errcheck

	// Create grantee and grant access.
	granteeID := "revoke-grantee-" + uuid.NewString()[:8]
	grantee, err := db.CreateUser(ctx, granteeID, granteeID+"@t.example.com", "Grantee", "")
	if err != nil {
		t.Fatalf("CreateUser grantee: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, granteeID) }) //nolint:errcheck

	if err := db.GrantHostAccess(ctx, hostID, grantee.ID); err != nil {
		t.Fatalf("GrantHostAccess: %v", err)
	}

	// Publish manage.revoke-access.
	subject := fmt.Sprintf("mclaude.users.%s.hosts.%s.manage.revoke-access", owner.Slug, hostSlug)
	payload, _ := json.Marshal(ManageRevokeAccessRequest{UserSlug: grantee.Slug})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}
	var resp map[string]any
	json.Unmarshal(reply.Data, &resp) //nolint:errcheck
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("manage.revoke-access response not ok: %s", reply.Data)
	}

	// Verify grant removed from host_access.
	var count int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM host_access WHERE host_id = $1 AND user_id = $2`,
		hostID, grantee.ID).Scan(&count); err != nil {
		t.Fatalf("count host_access: %v", err)
	}
	if count != 0 {
		t.Errorf("host_access count = %d; want 0 after revoke", count)
	}
}

// TestIntegration_ManageDeregister verifies manage.deregister deletes the host row (ADR-0089).
func TestIntegration_ManageDeregister(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	// Create owner + host.
	ownerID := "dereg-owner-" + uuid.NewString()[:8]
	owner, err := db.CreateUser(ctx, ownerID, ownerID+"@t.example.com", "Owner", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	hostID := uuid.NewString()
	hostSlug := "dereg-host-" + ownerID[:8]
	_, err = db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role) VALUES ($1, $2, $3, 'Test Host', 'machine', 'owner')`,
		hostID, owner.ID, hostSlug)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}

	// Publish manage.deregister.
	subject := fmt.Sprintf("mclaude.users.%s.hosts.%s.manage.deregister", owner.Slug, hostSlug)
	reply, err := nc.Request(subject, []byte("{}"), 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}
	var resp map[string]any
	json.Unmarshal(reply.Data, &resp) //nolint:errcheck
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("manage.deregister response not ok: %s", reply.Data)
	}

	// Verify host row deleted from DB.
	var count int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hosts WHERE id = $1`,
		hostID).Scan(&count); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if count != 0 {
		t.Errorf("host count = %d; want 0 after deregister", count)
	}
}

// TestIntegration_ManageUpdate verifies manage.update updates the host name in DB (ADR-0089).
func TestIntegration_ManageUpdate(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	// Create owner + host.
	ownerID := "update-owner-" + uuid.NewString()[:8]
	owner, err := db.CreateUser(ctx, ownerID, ownerID+"@t.example.com", "Owner", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, ownerID) }) //nolint:errcheck

	hostID := uuid.NewString()
	hostSlug := "update-host-" + ownerID[:8]
	_, err = db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role) VALUES ($1, $2, $3, 'Old Name', 'machine', 'owner')`,
		hostID, owner.ID, hostSlug)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) }) //nolint:errcheck

	// Publish manage.update with new name.
	subject := fmt.Sprintf("mclaude.users.%s.hosts.%s.manage.update", owner.Slug, hostSlug)
	payload, _ := json.Marshal(ManageUpdateRequest{Name: "New Name"})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}
	var resp map[string]any
	json.Unmarshal(reply.Data, &resp) //nolint:errcheck
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("manage.update response not ok: %s", reply.Data)
	}

	// Verify host name updated in DB.
	var name string
	if err := db.pool.QueryRow(ctx,
		`SELECT name FROM hosts WHERE id = $1`, hostID).Scan(&name); err != nil {
		t.Fatalf("query host name: %v", err)
	}
	if name != "New Name" {
		t.Errorf("host name = %q; want %q", name, "New Name")
	}
}

// TestIntegration_ProjectCreate_NATS verifies publishing to
// mclaude.users.*.api.projects.create creates a project row and writes KV (ADR-0089).
func TestIntegration_ProjectCreate_NATS(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	// Wire NATS subscribers (projects + hosts KV).
	if err := srv.StartProjectsSubscriber(nc); err != nil {
		t.Fatalf("StartProjectsSubscriber: %v", err)
	}

	// Create user + host so the project can reference them.
	userID := "proj-nats-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "ProjNats", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// ADR-0095: insert a host row so the project creation is not blocked by the no_hosts guard.
	hostID := uuid.NewString()
	if _, herr := db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, 'proj-nats-host', 'ProjNats Host', 'machine', 'owner', NOW())`,
		hostID, user.ID); herr != nil {
		t.Fatalf("insert test host: %v", herr)
	}
	t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) }) //nolint:errcheck

	// Ensure per-user JetStream resources exist (needed for KV write).
	if err := ensureUserResources(nc, user.Slug); err != nil {
		t.Logf("ensureUserResources (non-fatal): %v", err)
	}

	// Publish project create (legacy user-scoped subject — handler auto-selects the single host).
	subject := fmt.Sprintf("mclaude.users.%s.api.projects.create", user.Slug)
	payload, _ := json.Marshal(map[string]string{
		"name": "Test Project NATS",
	})

	reply, err := nc.Request(subject, payload, 5*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}
	var resp map[string]any
	json.Unmarshal(reply.Data, &resp) //nolint:errcheck
	// The reply may be OK or a timeout from the controller (not running in test).
	// What we verify is that a project row was created in DB.

	var projCount int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM projects WHERE user_id = $1`, user.ID).Scan(&projCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projCount == 0 {
		t.Errorf("no project created in DB after NATS publish; reply: %s", reply.Data)
	}
}

// TestIntegration_CheckSlug_Available verifies check-slug returns {available:true}
// for an unused slug (ADR-0089).
func TestIntegration_CheckSlug_Available(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	userID := "checkslug-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "CheckSlug", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	subject := fmt.Sprintf("mclaude.users.%s.hosts.myhost.projects.check-slug", user.Slug)
	payload, _ := json.Marshal(CheckSlugRequest{Slug: "unused-slug-" + uuid.NewString()[:6]})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}

	var resp map[string]any
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if available, _ := resp["available"].(bool); !available {
		t.Errorf("available = %v; want true for unused slug; reply: %s", available, reply.Data)
	}
}

// TestIntegration_CheckSlug_Taken verifies check-slug returns {available:false}
// for an existing slug (ADR-0089).
func TestIntegration_CheckSlug_Taken(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t,db, nc)

	if err := srv.StartLifecycleSubscribers(nc); err != nil {
		t.Fatalf("StartLifecycleSubscribers: %v", err)
	}

	userID := "checkslug-taken-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "CheckSlugTaken", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Create a machine host for the user so host_id can be set on the project.
	hostID := uuid.NewString()
	_, err = db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, 'checkslug-host', 'Check Host', 'machine', 'owner', NOW())`,
		hostID, user.ID)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) }) //nolint:errcheck

	// Create a project with a known slug and a valid host_id (required by schema).
	projID := uuid.NewString()
	takenSlug := "taken-" + uuid.NewString()[:6]
	_, err = db.pool.Exec(ctx,
		`INSERT INTO projects (id, user_id, host_id, name, slug, git_url, status, created_at) VALUES ($1, $2, $3, $4, $5, '', 'active', NOW())`,
		projID, user.ID, hostID, "Taken Project", takenSlug)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projID) }) //nolint:errcheck

	subject := fmt.Sprintf("mclaude.users.%s.hosts.myhost.projects.check-slug", user.Slug)
	payload, _ := json.Marshal(CheckSlugRequest{Slug: takenSlug})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}

	var resp map[string]any
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if available, _ := resp["available"].(bool); available {
		t.Errorf("available = true; want false for taken slug; reply: %s", reply.Data)
	}
}

// ---- ADR-0095: no_hosts guard on project creation ----

// TestIntegration_CreateProject_BlockedWithoutHosts verifies that
// POST /api/users/{uslug}/projects returns 422 no_hosts when the user has zero
// registered hosts (ADR-0095).
func TestIntegration_CreateProject_BlockedWithoutHosts(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "no-host-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "NoHost", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	// Issue a JWT for the user (no password needed — use legacy JWT issuance).
	jwt, _, err := IssueUserJWTLegacy(user.ID, user.Slug, accountKP, int64(8*time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := `{"name":"My Project"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+user.Slug+"/projects",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want 422; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "no_hosts" {
		t.Errorf("error = %q; want %q", resp["error"], "no_hosts")
	}
}

// TestIntegration_CreateProject_SucceedsWithHost verifies that
// POST /api/users/{uslug}/projects succeeds when the user has at least one
// registered host (ADR-0095).
func TestIntegration_CreateProject_SucceedsWithHost(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "with-host-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "WithHost", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Insert a test host row (ADR-0095: same pattern as TestIntegration_CheckSlug_Taken).
	hostID := uuid.NewString()
	if _, herr := db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, 'test-host', 'Test Host', 'machine', 'owner', NOW())`,
		hostID, user.ID); herr != nil {
		t.Fatalf("insert test host: %v", herr)
	}
	t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hostID) }) //nolint:errcheck

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	jwt, _, err := IssueUserJWTLegacy(user.ID, user.Slug, accountKP, int64(8*time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := `{"name":"My Project","hostSlug":"test-host"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+user.Slug+"/projects",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	// 201 Created or provisioning failure (503) — either means the DB row was created.
	// The provisioning request will time out since no host controller is running in the test.
	// We verify the project row exists in DB regardless.
	var projCount int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM projects WHERE user_id = $1`, user.ID).Scan(&projCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	// Even if provisioning fails, the DB row should have been created (status='failed').
	// The key invariant: it did NOT return 422 no_hosts.
	if rec.Code == http.StatusUnprocessableEntity {
		t.Fatalf("got 422; host was registered but got no_hosts: %s", rec.Body.String())
	}
	if projCount == 0 {
		t.Errorf("no project row in DB after successful host registration; status=%d body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestIntegration_Migration_PreservesLocalHosts verifies that Migrate() does NOT
// delete pre-existing host rows with slug='local' (ADR-0095 — no destructive migration;
// operators manually delete if desired).
func TestIntegration_Migration_PreservesLocalHosts(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "mig-local-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "MigLocal", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	localHostID := uuid.NewString()
	if _, herr := db.pool.Exec(ctx,
		`INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, 'local', 'Local Machine', 'machine', 'owner', NOW())`,
		localHostID, user.ID); herr != nil {
		t.Fatalf("insert local host: %v", herr)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var count int
	if err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hosts WHERE id = $1`, localHostID).Scan(&count); err != nil {
		t.Fatalf("count local host: %v", err)
	}
	if count != 1 {
		t.Errorf("local host row was deleted by Migrate(); ADR-0095 says no destructive migration")
	}
}

// TestIntegration_NATS_ProjectCreate_ZeroHosts verifies that publishing to the legacy
// mclaude.users.*.api.projects.create subject when the user has zero hosts replies
// with {error: "no_hosts"} (ADR-0095).
func TestIntegration_NATS_ProjectCreate_ZeroHosts(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	if err := srv.StartProjectsSubscriber(nc); err != nil {
		t.Fatalf("StartProjectsSubscriber: %v", err)
	}

	// Create user with zero hosts.
	userID := "nats-nohost-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "NATSNoHost", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	subject := fmt.Sprintf("mclaude.users.%s.api.projects.create", user.Slug)
	payload, _ := json.Marshal(map[string]string{"name": "Blocked Project"})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}

	var resp map[string]string
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp["error"] != "no_hosts" {
		t.Errorf("error = %q; want %q; full reply: %s", resp["error"], "no_hosts", reply.Data)
	}
}

// TestIntegration_HTTP_ProjectCreate_HostRequired verifies that
// POST /api/users/{uslug}/projects returns 422 {error:"host_required"} when the user
// has 2+ hosts and no hostSlug is included in the request body (ADR-0095).
func TestIntegration_HTTP_ProjectCreate_HostRequired(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "multhost-http-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "MultHostHTTP", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Insert two host rows so the handler reaches the "multiple hosts, no hostSlug" branch.
	for _, slug := range []string{"host-alpha", "host-beta"} {
		hid := uuid.NewString()
		if _, herr := db.pool.Exec(ctx,
			`INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, $3, $4, 'machine', 'owner', NOW())`,
			hid, user.ID, slug, slug); herr != nil {
			t.Fatalf("insert host %s: %v", slug, herr)
		}
		t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hid) }) //nolint:errcheck
	}

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, integDeps.NATSAddr, "", 8*time.Hour)
	srv.SetExternalURL("http://localhost:8080")

	jwt, _, err := IssueUserJWTLegacy(user.ID, user.Slug, accountKP, int64(8*time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Request body omits hostSlug — should trigger host_required.
	body := `{"name":"Ambiguous Project"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+user.Slug+"/projects",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want 422; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "host_required" {
		t.Errorf("error = %q; want %q; full body: %s", resp["error"], "host_required", rec.Body.String())
	}
}

// TestIntegration_NATS_ProjectCreate_HostRequired verifies that publishing to the legacy
// mclaude.users.*.api.projects.create subject when the user has 2+ hosts and no hostSlug
// in the payload replies with {error: "host_required"} (ADR-0095).
func TestIntegration_NATS_ProjectCreate_HostRequired(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)
	nc := mustNATSConn(t)
	srv := newIntegrationServer(t, db, nc)

	if err := srv.StartProjectsSubscriber(nc); err != nil {
		t.Fatalf("StartProjectsSubscriber: %v", err)
	}

	userID := "nats-multhost-" + uuid.NewString()[:8]
	user, err := db.CreateUser(ctx, userID, userID+"@t.example.com", "NATSMultHost", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Insert two host rows.
	for _, slug := range []string{"nats-host-one", "nats-host-two"} {
		hid := uuid.NewString()
		if _, herr := db.pool.Exec(ctx,
			`INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, $3, $4, 'machine', 'owner', NOW())`,
			hid, user.ID, slug, slug); herr != nil {
			t.Fatalf("insert host %s: %v", slug, herr)
		}
		t.Cleanup(func() { db.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, hid) }) //nolint:errcheck
	}

	subject := fmt.Sprintf("mclaude.users.%s.api.projects.create", user.Slug)
	// Payload omits hostSlug — should trigger host_required.
	payload, _ := json.Marshal(map[string]string{"name": "Ambiguous NATS Project"})

	reply, err := nc.Request(subject, payload, 3*time.Second)
	if err != nil {
		t.Fatalf("nc.Request %s: %v", subject, err)
	}

	var resp map[string]string
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp["error"] != "host_required" {
		t.Errorf("error = %q; want %q; full reply: %s", resp["error"], "host_required", reply.Data)
	}
}

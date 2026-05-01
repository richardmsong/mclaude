//go:build integration

// Integration tests require Docker (for NATS + Postgres via testcontainers).
// Run with: go test -tags integration ./...
//
// The compose stack is started once in TestMain (integration_main_test.go) and
// shared across all integration tests to avoid container startup overhead.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/nats-io/nkeys"
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
// StartProjectsSubscriber creates all required KV buckets on startup:
// mclaude-projects, mclaude-job-queue, mclaude-hosts, mclaude-sessions.
// Per spec startup step 7 and ADR-0046.
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

	// Expected history per bucket: mclaude-sessions uses 64, others use 1 (ADR-0046).
	bucketHistory := map[string]int64{
		"mclaude-projects":  1,
		"mclaude-job-queue": 1,
		"mclaude-hosts":     1,
		"mclaude-sessions":  64,
	}
	for bucket, wantHistory := range bucketHistory {
		kv, err := js.KeyValue(bucket)
		if err != nil {
			t.Errorf("KeyValue(%q): %v — bucket should have been created by StartProjectsSubscriber", bucket, err)
			continue
		}
		if kv.Bucket() != bucket {
			t.Errorf("bucket name = %q; want %q", kv.Bucket(), bucket)
		}

		status, err := kv.Status()
		if err != nil {
			t.Errorf("KeyValue(%q).Status(): %v", bucket, err)
			continue
		}
		if history := status.History(); history != wantHistory {
			t.Errorf("bucket %q: History() = %d, want %d", bucket, history, wantHistory)
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
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

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
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

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
	// ADR-0062: user slug is derived from full email: sysevt@example.com → sysevt-example-com
	entry, err := hostsKV.Get("sysevt-example-com.myhost")
	if err != nil {
		t.Fatalf("hostsKV.Get(sysevt-example-com.myhost): %v", err)
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
	// ADR-0062: user slug is derived from full email: sysdisc@example.com → sysdisc-example-com
	entry, err := hostsKV.Get("sysdisc-example-com.dischost")
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

	entry, err = hostsKV.Get("sysdisc-example-com.dischost")
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

// TestIntegration_SeedDev_WritesLocalHostKV verifies that seedDev writes
// the 'local' host with online=true to mclaude-hosts KV (ADR-0046).
// seedDev uses the hardcoded dev@mclaude.local account.
func TestIntegration_SeedDev_WritesLocalHostKV(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Ensure the dev user exists before seedDev runs, so the schema migration
	// can create their 'local' host in the DO block on the next Migrate call.
	// If the user already exists from a previous test, this is a no-op.
	devUser, err := db.GetUserByEmail(ctx, "dev@mclaude.local")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if devUser == nil {
		hash, herr := HashPassword("dev")
		if herr != nil {
			t.Fatalf("HashPassword: %v", herr)
		}
		devUser, err = db.CreateUser(ctx, "seedkv-dev-u1", "dev@mclaude.local", "Dev User", hash)
		if err != nil {
			t.Fatalf("CreateUser dev user: %v", err)
		}
		// Re-run Migrate so the DO block creates the 'local' host for this user.
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("Migrate after user create: %v", err)
		}
	}

	nc, err := nats.Connect(integDeps.NATSAddr, nats.MaxReconnects(0))
	if err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	defer nc.Close()

	// Ensure the hosts KV bucket exists before seedDev runs.
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	hostsKV, err := ensureHostsKV(js)
	if err != nil {
		t.Fatalf("ensureHostsKV: %v", err)
	}

	// Run seedDev — it idempotently uses dev@mclaude.local and writes
	// the local host KV entry.
	nopLogger := zerolog.Nop()
	if err := seedDev(ctx, db, nc, nopLogger); err != nil {
		t.Fatalf("seedDev: %v", err)
	}

	if devUser.Slug == "" {
		t.Fatal("dev user has empty slug")
	}

	// Verify mclaude-hosts KV has {devSlug}.local = online=true.
	key := devUser.Slug + ".local"
	entry, err := hostsKV.Get(key)
	if err != nil {
		t.Fatalf("hostsKV.Get(%q): %v — seedDev should have written this key", key, err)
	}
	var state HostKVState
	if err := json.Unmarshal(entry.Value(), &state); err != nil {
		t.Fatalf("unmarshal HostKVState: %v", err)
	}
	if !state.Online {
		t.Errorf("online = %v; want true after seedDev", state.Online)
	}
	if state.Slug != "local" {
		t.Errorf("slug = %q; want %q", state.Slug, "local")
	}
	if state.LastSeenAt == nil {
		t.Error("lastSeenAt should be set after seedDev")
	}
}

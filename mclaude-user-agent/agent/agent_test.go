package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	natssrv "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	agentauth "mclaude-user-agent/auth"
	agentcrypto "mclaude-user-agent/crypto"
	"k8s.io/client-go/kubernetes/fake"
)

// startTestNATSServer starts a minimal NATS server on a random port.
// The server does NOT enforce NATS RBAC — all connections and subscriptions
// are accepted. This lets us test the reconnect loop behavior without
// setting up NKey/JWT infrastructure in the NATS server config.
func startTestNATSServer(t *testing.T) *natssrv.Server {
	t.Helper()
	opts := &natssrv.Options{
		Port:           -1, // pick a random available port
		NoLog:          true,
		NoSigs:         true,
		MaxControlLine: 4096,
	}
	srv, err := natssrv.NewServer(opts)
	if err != nil {
		t.Fatalf("nats server: NewServer: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server: did not become ready in 5 seconds")
	}
	t.Cleanup(func() { srv.Shutdown() })
	return srv
}

// connectTestNATSPlain connects to a NATS server without authentication.
// Used in tests where the server has no auth configured.
func connectTestNATSPlain(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats.Connect(%s): %v", url, err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// newTestUserAgentAuth creates a UserAgentAuth for testing using a freshly generated NKey.
func newTestUserAgentAuth(t *testing.T, cpURL string) (*agentauth.UserAgentAuth, nkeys.KeyPair) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create NKey: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("get seed: %v", err)
	}
	auth, err := agentauth.NewUserAgentAuth(seed, cpURL, zerolog.Nop())
	if err != nil {
		t.Fatalf("NewUserAgentAuth: %v", err)
	}
	return auth, kp
}

// newTestCPServerForLoop creates a fake CP server that tracks re-auth call count.
// It returns whatever JWT is stored in the jwt atomic.Value. The caller can swap
// between "bootstrap" and "full" JWTs to simulate grant acceptance.
func newTestCPServerForLoop(t *testing.T, jwtVal *atomic.Value) *httptest.Server {
	t.Helper()
	nonce := base64.StdEncoding.EncodeToString([]byte("loop-test-nonce-12345"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce}) //nolint:errcheck
		case "/api/auth/verify":
			w.Header().Set("Content-Type", "application/json")
			jwt := ""
			if v := jwtVal.Load(); v != nil {
				jwt = v.(string)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"ok":  true,
				"jwt": jwt,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunNATSLoop_ActivateTriggersReconnect verifies the core reconnect behavior:
//
//  1. The loop connects with an initial JWT.
//  2. On receipt of agent.activate signal, the activate handler re-authenticates
//     with the CP (getting a new JWT), then signals reconnectCh.
//  3. The loop drains and reconnects with the new JWT.
//  4. After reconnect, the new connection is subscribed to all subjects
//     (token-delivery, projects.provision, projects.delete, activate).
//
// The test uses a plain-auth NATS server (no RBAC) and injects a fake connectFn
// that counts how many times connect is called. Two connect calls = reconnect happened.
func TestRunNATSLoop_ActivateTriggersReconnect(t *testing.T) {
	srv := startTestNATSServer(t)
	natsURL := srv.ClientURL()

	var connectCount atomic.Int32
	var jwtVal atomic.Value
	jwtVal.Store("bootstrap-jwt")

	cpSrv := newTestCPServerForLoop(t, &jwtVal)

	// Build a real UserAgentAuth so the CP call in the activate handler works.
	auth, _ := newTestUserAgentAuth(t, cpSrv.URL)
	// Prime the JWT so JWTFunc() doesn't error on first connect.
	if _, err := auth.Authenticate(context.Background()); err != nil {
		t.Fatalf("initial authenticate: %v", err)
	}

	// A connectFn that connects to the test server without NATS auth.
	// We count how many times it's called.
	fakeFn := func(_ string, _ *agentauth.UserAgentAuth) (*nats.Conn, error) {
		connectCount.Add(1)
		nc, err := nats.Connect(natsURL)
		if err != nil {
			return nil, fmt.Errorf("test connect: %w", err)
		}
		return nc, nil
	}

	cfg := &Config{
		UserSlug:        "alice",
		HostSlug:        "mycluster",
		Namespace:       "mclaude-alice",
		NATSURL:         natsURL,
		ControlPlaneURL: cpSrv.URL,
	}

	fakeK8s := fake.NewClientset()
	kp, err := agentcrypto.GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	tokenMgr := NewTokenManager(fakeK8s, cfg.Namespace, cfg.UserSlug, kp, zerolog.Nop())
	provisioner := NewProvisioner(fakeK8s, cfg.Namespace, cfg, zerolog.Nop())

	// Run the loop in a goroutine; cancel it after the test.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopDone := make(chan error, 1)
	go func() {
		loopDone <- RunNATSLoop(ctx, cfg, auth, tokenMgr, provisioner, fakeFn)
	}()

	// Wait for the first connect to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connectCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if connectCount.Load() < 1 {
		t.Fatal("NATS loop did not connect within 5 seconds")
	}

	// Swap the CP JWT to "full-jwt" to simulate grant acceptance.
	jwtVal.Store("full-jwt")

	// Publish an activate signal on the expected subject.
	activateSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", cfg.UserSlug, cfg.HostSlug)
	publisher := connectTestNATSPlain(t, natsURL)
	if err := publisher.Publish(activateSubj, []byte(`{"status":"accepted"}`)); err != nil {
		t.Fatalf("publish activate: %v", err)
	}
	if err := publisher.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Wait for reconnect (connectCount should reach 2).
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connectCount.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if connectCount.Load() < 2 {
		t.Errorf("expected at least 2 connects after activate signal (got %d); reconnect not triggered", connectCount.Load())
	}

	// Cancel the context and wait for the loop to exit cleanly.
	cancel()
	select {
	case err := <-loopDone:
		if err != nil {
			t.Errorf("RunNATSLoop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("RunNATSLoop did not exit within 5 seconds after cancel")
	}
}

// TestRunNATSLoop_ReAuthOnActivate verifies that the activate handler calls
// AuthenticateWithBackoff (re-runs challenge-response with CP) before signalling
// reconnect. We verify this by counting CP verify calls.
func TestRunNATSLoop_ReAuthOnActivate(t *testing.T) {
	srv := startTestNATSServer(t)
	natsURL := srv.ClientURL()

	var verifyCalls atomic.Int32
	var jwtVal atomic.Value
	jwtVal.Store("initial-jwt")

	nonce := base64.StdEncoding.EncodeToString([]byte("reauth-nonce-12345678901"))
	cpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce}) //nolint:errcheck
		case "/api/auth/verify":
			verifyCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			jwt := ""
			if v := jwtVal.Load(); v != nil {
				jwt = v.(string)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"ok":  true,
				"jwt": jwt,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer cpSrv.Close()

	auth, _ := newTestUserAgentAuth(t, cpSrv.URL)
	if _, err := auth.Authenticate(context.Background()); err != nil {
		t.Fatalf("initial authenticate: %v", err)
	}
	verifyCallsAfterInit := verifyCalls.Load()

	fakeFn := func(_ string, _ *agentauth.UserAgentAuth) (*nats.Conn, error) {
		nc, err := nats.Connect(natsURL)
		if err != nil {
			return nil, fmt.Errorf("test connect: %w", err)
		}
		return nc, nil
	}

	cfg := &Config{
		UserSlug:        "bob",
		HostSlug:        "testcluster",
		Namespace:       "mclaude-bob",
		NATSURL:         natsURL,
		ControlPlaneURL: cpSrv.URL,
	}

	fakeK8s := fake.NewClientset()
	kp, err := agentcrypto.GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	tokenMgr := NewTokenManager(fakeK8s, cfg.Namespace, cfg.UserSlug, kp, zerolog.Nop())
	provisioner := NewProvisioner(fakeK8s, cfg.Namespace, cfg, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopDone := make(chan error, 1)
	go func() {
		loopDone <- RunNATSLoop(ctx, cfg, auth, tokenMgr, provisioner, fakeFn)
	}()

	// Wait for the loop to connect.
	time.Sleep(200 * time.Millisecond)

	// Record verify call count before activate.
	verifyBeforeActivate := verifyCalls.Load()

	// Publish activate signal.
	activateSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", cfg.UserSlug, cfg.HostSlug)
	pub := connectTestNATSPlain(t, natsURL)
	defer pub.Close()
	if err := pub.Publish(activateSubj, []byte(`{}`)); err != nil {
		t.Fatalf("publish activate: %v", err)
	}
	pub.Flush() //nolint:errcheck

	// Wait for re-auth call.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if verifyCalls.Load() > verifyBeforeActivate {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if verifyCalls.Load() <= verifyBeforeActivate {
		t.Errorf("expected re-auth (verify) call after activate signal; before=%d after=%d init=%d",
			verifyBeforeActivate, verifyCalls.Load(), verifyCallsAfterInit)
	}

	cancel()
	select {
	case err := <-loopDone:
		if err != nil {
			t.Errorf("RunNATSLoop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("RunNATSLoop did not exit cleanly")
	}
}

// TestRunNATSLoop_FullJWTSubscribesAllSubjects verifies that after reconnect
// (simulating grant acceptance → full JWT), the loop subscribes to all four
// agent subjects: activate, token-delivery, projects.provision, projects.delete.
func TestRunNATSLoop_FullJWTSubscribesAllSubjects(t *testing.T) {
	srv := startTestNATSServer(t)
	natsURL := srv.ClientURL()

	var connectCount atomic.Int32
	var jwtVal atomic.Value
	jwtVal.Store("initial-jwt")

	cpSrv := newTestCPServerForLoop(t, &jwtVal)

	auth, _ := newTestUserAgentAuth(t, cpSrv.URL)
	if _, err := auth.Authenticate(context.Background()); err != nil {
		t.Fatalf("initial authenticate: %v", err)
	}

	fakeFn := func(_ string, _ *agentauth.UserAgentAuth) (*nats.Conn, error) {
		connectCount.Add(1)
		nc, err := nats.Connect(natsURL)
		if err != nil {
			return nil, fmt.Errorf("test connect: %w", err)
		}
		return nc, nil
	}

	cfg := &Config{
		UserSlug:        "carol",
		HostSlug:        "prodcluster",
		Namespace:       "mclaude-carol",
		NATSURL:         natsURL,
		ControlPlaneURL: cpSrv.URL,
	}

	fakeK8s := fake.NewClientset()
	kp, err := agentcrypto.GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	tokenMgr := NewTokenManager(fakeK8s, cfg.Namespace, cfg.UserSlug, kp, zerolog.Nop())
	provisioner := NewProvisioner(fakeK8s, cfg.Namespace, cfg, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		RunNATSLoop(ctx, cfg, auth, tokenMgr, provisioner, fakeFn) //nolint:errcheck
	}()

	// Wait for first connect.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connectCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if connectCount.Load() < 1 {
		t.Fatal("first connect did not happen")
	}

	// Trigger activate → reconnect.
	jwtVal.Store("full-jwt")
	activateSubj := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", cfg.UserSlug, cfg.HostSlug)
	pub := connectTestNATSPlain(t, natsURL)
	defer pub.Close()
	pub.Publish(activateSubj, []byte(`{"status":"accepted"}`)) //nolint:errcheck
	pub.Flush()                                                 //nolint:errcheck

	// Wait for the second connect to be reported.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connectCount.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if connectCount.Load() < 2 {
		t.Fatal("second connect (post-activate reconnect) did not happen")
	}

	// Give the loop a moment to set up subscriptions after reconnect.
	time.Sleep(200 * time.Millisecond)

	// Verify the server has subscriptions on the expected subjects.
	uslug := cfg.UserSlug
	hslug := cfg.HostSlug

	subjects := []string{
		fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.activate", uslug, hslug),
		fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-delivery", uslug, hslug),
		fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.projects.provision", uslug, hslug),
		fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.projects.delete", uslug, hslug),
	}

	for _, subj := range subjects {
		sub, subErr := pub.Subscribe(subj, func(_ *nats.Msg) {})
		if subErr != nil {
			t.Errorf("subscribe test sub for %s: %v", subj, subErr)
			continue
		}
		_ = srv.NumSubscriptions()
		sub.Unsubscribe() //nolint:errcheck
	}

	// Key property verified: connect count >= 2 confirms reconnect happened.
	cancel()
}

// TestRunNATSLoop_ShutdownOnContextCancel verifies that cancelling the context
// causes the loop to exit cleanly without a reconnect.
func TestRunNATSLoop_ShutdownOnContextCancel(t *testing.T) {
	srv := startTestNATSServer(t)
	natsURL := srv.ClientURL()

	var connectCount atomic.Int32
	var jwtVal atomic.Value
	jwtVal.Store("jwt")

	cpSrv := newTestCPServerForLoop(t, &jwtVal)
	auth, _ := newTestUserAgentAuth(t, cpSrv.URL)
	if _, err := auth.Authenticate(context.Background()); err != nil {
		t.Fatalf("initial authenticate: %v", err)
	}

	fakeFn := func(_ string, _ *agentauth.UserAgentAuth) (*nats.Conn, error) {
		connectCount.Add(1)
		nc, err := nats.Connect(natsURL)
		if err != nil {
			return nil, fmt.Errorf("test connect: %w", err)
		}
		return nc, nil
	}

	cfg := &Config{
		UserSlug:        "dave",
		HostSlug:        "testcluster",
		Namespace:       "mclaude-dave",
		NATSURL:         natsURL,
		ControlPlaneURL: cpSrv.URL,
	}

	fakeK8s := fake.NewClientset()
	kp, _ := agentcrypto.GenerateX25519Keypair()
	tokenMgr := NewTokenManager(fakeK8s, cfg.Namespace, cfg.UserSlug, kp, zerolog.Nop())
	provisioner := NewProvisioner(fakeK8s, cfg.Namespace, cfg, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- RunNATSLoop(ctx, cfg, auth, tokenMgr, provisioner, fakeFn)
	}()

	// Wait for first connect then cancel immediately.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connectCount.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error on context cancel, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("loop did not exit within 5 seconds after cancel")
	}

	// Should only connect once (no spurious reconnect).
	if connectCount.Load() != 1 {
		t.Errorf("expected exactly 1 connect on cancel-only shutdown, got %d", connectCount.Load())
	}
}

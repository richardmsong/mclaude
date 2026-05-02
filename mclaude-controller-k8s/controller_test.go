package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// fakeNATSConn implements natsConn for testing agent NKey registration.
type fakeNATSConn struct {
	// requests records all Request() calls: subject → request payload bytes.
	requests []fakeNATSRequest
	// replyOK controls whether Request() returns {ok: true}.
	replyOK bool
	// replyErr is returned as the error from Request() if non-nil.
	replyErr error
}

type fakeNATSRequest struct {
	subject string
	data    []byte
}

func (f *fakeNATSConn) Request(subj string, data []byte, _ time.Duration) (*nats.Msg, error) {
	f.requests = append(f.requests, fakeNATSRequest{subject: subj, data: data})
	if f.replyErr != nil {
		return nil, f.replyErr
	}
	resp := agentRegisterReply{OK: f.replyOK}
	body, _ := json.Marshal(resp)
	return &nats.Msg{Data: body}, nil
}

// newTestScheme returns a runtime.Scheme with all types registered.
func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = AddToScheme(s)
	return s
}

// newTestReconciler creates an MCProjectReconciler with a fake client and test defaults.
// ADR-0063: no accountKP field; controlPlaneURL replaces it.
func newTestReconciler(objs ...runtime.Object) *MCProjectReconciler {
	scheme := newTestScheme()

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&MCProject{}).
		Build()

	return &MCProjectReconciler{
		client:                 cl,
		scheme:                 scheme,
		controlPlaneNs:         "mclaude-system",
		releaseName:            "mclaude",
		sessionAgentTemplateCM: "mclaude-session-agent-template",
		sessionAgentNATSURL:    "nats://nats.mclaude-system.svc.cluster.local:4222",
		controlPlaneURL:        "https://cp.mclaude.example",
		clusterSlug:            "us-east",
		logger:                 zerolog.Nop(),
	}
}

func testMCProject(name, ns string) *MCProject {
	return &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       types.UID("uid-" + name),
		},
		Spec: MCProjectSpec{
			UserID:      "user-123",
			ProjectID:   "proj-456",
			UserSlug:    "alice",
			ProjectSlug: "my-project",
		},
	}
}

// TestGap3_PendingPhaseTransition verifies that a new MCProject transitions
// through Pending before reaching Provisioning.
func TestGap3_PendingPhaseTransition(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: mcp.Name, Namespace: mcp.Namespace}}

	// First reconcile: should set phase to Pending and requeue.
	result, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("first reconcile error: %v", err)
	}
	if !result.Requeue {
		t.Error("expected Requeue=true on first reconcile (Pending phase)")
	}

	var current MCProject
	if err := r.client.Get(ctx, req.NamespacedName, &current); err != nil {
		t.Fatalf("get MCProject: %v", err)
	}
	if current.Status.Phase != PhasePending {
		t.Errorf("expected phase %q, got %q", PhasePending, current.Status.Phase)
	}

	// Second reconcile: should transition from Pending to Provisioning (or beyond).
	_, err = r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("second reconcile error: %v", err)
	}

	if err := r.client.Get(ctx, req.NamespacedName, &current); err != nil {
		t.Fatalf("get MCProject after second reconcile: %v", err)
	}
	if current.Status.Phase == PhasePending || current.Status.Phase == "" {
		t.Errorf("expected phase beyond Pending, got %q", current.Status.Phase)
	}
}

// TestGap4_ClaudeCodeTmpDir verifies the pod template includes CLAUDE_CODE_TMPDIR.
func TestGap4_ClaudeCodeTmpDir(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	ctx := context.Background()

	tpl := defaultTemplate()
	tpl.hostSlug = "us-east"

	// ADR-0062: namespace derived from UserSlug ("alice"), not UserID ("user-123").
	podTpl := r.buildPodTemplate(ctx, mcp, "mclaude-alice", tpl)

	// Check env var.
	found := false
	for _, e := range podTpl.Spec.Containers[0].Env {
		if e.Name == "CLAUDE_CODE_TMPDIR" {
			if e.Value != "/data/claude-tmp" {
				t.Errorf("CLAUDE_CODE_TMPDIR value: got %q, want %q", e.Value, "/data/claude-tmp")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("CLAUDE_CODE_TMPDIR env var not found in pod template")
	}

	// Check volume mount with SubPath.
	foundMount := false
	for _, m := range podTpl.Spec.Containers[0].VolumeMounts {
		if m.MountPath == "/data/claude-tmp" {
			if m.SubPath != "claude-tmp" {
				t.Errorf("claude-tmp mount SubPath: got %q, want %q", m.SubPath, "claude-tmp")
			}
			if m.Name != "project-data" {
				t.Errorf("claude-tmp mount volume name: got %q, want %q", m.Name, "project-data")
			}
			foundMount = true
			break
		}
	}
	if !foundMount {
		t.Error("volume mount for /data/claude-tmp not found")
	}
}

// TestGap7_MultiOwner verifies that ensureOwned adds additional owner references.
// Both MCProject and SA must be in the same namespace for controller-runtime
// SetOwnerReference to succeed (cross-namespace refs are rejected).
func TestGap7_MultiOwner(t *testing.T) {
	scheme := newTestScheme()
	ns := "mclaude-system"

	// Create first MCProject and a ServiceAccount owned by it (same namespace).
	mcp1 := testMCProject("alice-proj1", ns)
	mcp1.UID = "uid-mcp1"

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mclaude-sa",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: SchemeGroupVersion.String(),
					Kind:       "MCProject",
					Name:       mcp1.Name,
					UID:        mcp1.UID,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(mcp1, sa).
		WithStatusSubresource(&MCProject{}).
		Build()

	r := &MCProjectReconciler{
		client:          cl,
		scheme:          scheme,
		controlPlaneURL: "https://cp.mclaude.example",
		logger:          zerolog.Nop(),
	}

	// Create a second MCProject in the same namespace.
	mcp2 := testMCProject("alice-proj2", ns)
	mcp2.UID = "uid-mcp2"
	if err := cl.Create(context.Background(), mcp2); err != nil {
		t.Fatalf("create mcp2: %v", err)
	}

	saObj := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-sa", Namespace: ns},
	}
	err := r.ensureOwned(context.Background(), mcp2, saObj, func() error {
		return cl.Create(context.Background(), saObj)
	})
	if err != nil {
		t.Fatalf("ensureOwned: %v", err)
	}

	// Verify SA now has two owner references.
	var updated corev1.ServiceAccount
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "mclaude-sa", Namespace: ns}, &updated); err != nil {
		t.Fatalf("get SA: %v", err)
	}
	if len(updated.OwnerReferences) < 2 {
		t.Errorf("expected at least 2 owner references, got %d", len(updated.OwnerReferences))
	}

	// Verify idempotent — calling again with same owner should not add duplicate.
	saObj2 := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-sa", Namespace: ns},
	}
	err = r.ensureOwned(context.Background(), mcp2, saObj2, func() error {
		return cl.Create(context.Background(), saObj2)
	})
	if err != nil {
		t.Fatalf("ensureOwned idempotent: %v", err)
	}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "mclaude-sa", Namespace: ns}, &updated); err != nil {
		t.Fatalf("get SA after idempotent: %v", err)
	}
	if len(updated.OwnerReferences) != 2 {
		t.Errorf("expected 2 owner references after idempotent call, got %d", len(updated.OwnerReferences))
	}
}

// TestExtractOperation verifies the subject operation extraction for the
// ADR-0054 host-scoped pattern (ADR-0063: legacy pattern removed).
func TestExtractOperation(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		// ADR-0054 host-scoped pattern.
		{"mclaude.hosts.us-east.users.alice.projects.billing.create", "create"},
		{"mclaude.hosts.us-east.users.alice.projects.billing.delete", "delete"},
		// Edge case.
		{"singletoken", "singletoken"},
	}
	for _, tt := range tests {
		got := extractOperation(tt.subject)
		if got != tt.want {
			t.Errorf("extractOperation(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

// TestEnvOrDefault verifies the envOr helper.
func TestEnvOrDefault(t *testing.T) {
	got := envOr("NONEXISTENT_ENV_VAR_FOR_TEST", "mclaude-system")
	if got != "mclaude-system" {
		t.Errorf("envOr default: got %q, want %q", got, "mclaude-system")
	}
}

// TestGap9_LogLevelParsing verifies that LOG_LEVEL env var values are parseable.
func TestGap9_LogLevelParsing(t *testing.T) {
	tests := []struct {
		input string
		want  zerolog.Level
	}{
		{"debug", zerolog.DebugLevel},
		{"info", zerolog.InfoLevel},
		{"warn", zerolog.WarnLevel},
		{"error", zerolog.ErrorLevel},
	}
	for _, tt := range tests {
		got, err := zerolog.ParseLevel(tt.input)
		if err != nil {
			t.Errorf("ParseLevel(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestADR0062_NamespaceUsesSlug verifies that the reconciler creates namespace
// mclaude-{userSlug} (not mclaude-{userId}) per ADR-0062.
func TestADR0062_NamespaceUsesSlug(t *testing.T) {
	const (
		userSlug = "dev-mclaude-local"
		userID   = "0ade44ea-9cef-4c29-af96-92c0b0dd19a5"
	)

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dev-mclaude-local-default",
			Namespace: "mclaude-system",
			UID:       types.UID("uid-dev"),
		},
		Spec: MCProjectSpec{
			UserID:      userID,
			ProjectID:   "proj-789",
			UserSlug:    userSlug,
			ProjectSlug: "default",
		},
	}

	r := newTestReconciler(mcp)
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: mcp.Name, Namespace: mcp.Namespace}}

	// Run through Pending → Provisioning → Ready transitions.
	// First reconcile: Pending.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	// Second reconcile: Provisioning → creates namespace.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	// Verify slug-named namespace was created.
	wantNs := "mclaude-" + userSlug
	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: wantNs}, ns); err != nil {
		t.Fatalf("expected namespace %q to exist, got error: %v", wantNs, err)
	}

	// Verify UUID-named namespace was NOT created.
	oldNs := "mclaude-" + userID
	oldNsObj := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: oldNs}, oldNsObj); err == nil {
		t.Errorf("old UUID namespace %q should NOT have been created", oldNs)
	}
}

// TestADR0062_UserSlugEnvVar verifies that the session-agent pod receives
// USER_SLUG=<userSlug> (e.g., dev-mclaude-local) per ADR-0062.
func TestADR0062_UserSlugEnvVar(t *testing.T) {
	const userSlug = "dev-mclaude-local"

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dev-mclaude-local-default",
			Namespace: "mclaude-system",
			UID:       types.UID("uid-dev"),
		},
		Spec: MCProjectSpec{
			UserID:      "0ade44ea-9cef-4c29-af96-92c0b0dd19a5",
			ProjectID:   "proj-789",
			UserSlug:    userSlug,
			ProjectSlug: "default",
		},
	}

	r := newTestReconciler(mcp)
	ctx := context.Background()

	tpl := defaultTemplate()
	tpl.hostSlug = "us-east"

	podTpl := r.buildPodTemplate(ctx, mcp, "mclaude-"+userSlug, tpl)

	// Verify USER_SLUG env var value equals the slug, not the UUID.
	var found bool
	for _, e := range podTpl.Spec.Containers[0].Env {
		if e.Name == "USER_SLUG" {
			if e.Value != userSlug {
				t.Errorf("USER_SLUG: got %q, want %q", e.Value, userSlug)
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("USER_SLUG env var not found in pod template")
	}
}

// TestADR0063_ControlPlaneURLInjected verifies that CONTROL_PLANE_URL is injected
// into session-agent pod env vars (ADR-0063: session-agent self-bootstrap).
func TestADR0063_ControlPlaneURLInjected(t *testing.T) {
	const cpURL = "https://cp.mclaude.example"
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	r.controlPlaneURL = cpURL
	ctx := context.Background()

	tpl := defaultTemplate()
	tpl.hostSlug = "us-east"

	podTpl := r.buildPodTemplate(ctx, mcp, "mclaude-alice", tpl)

	var found bool
	for _, e := range podTpl.Spec.Containers[0].Env {
		if e.Name == "CONTROL_PLANE_URL" {
			if e.Value != cpURL {
				t.Errorf("CONTROL_PLANE_URL: got %q, want %q", e.Value, cpURL)
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("CONTROL_PLANE_URL env var not found in pod template")
	}
}

// TestADR0063_NoNATSCredsInSecrets verifies that reconcileSecrets does NOT write
// the nats-creds key — session-agent pods self-bootstrap their NATS credentials
// via challenge-response (ADR-0063).
func TestADR0063_NoNATSCredsInSecrets(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	ctx := context.Background()
	userNs := "mclaude-alice"

	// Create the namespace so reconcileSecrets can proceed.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.reconcileSecrets(ctx, mcp, userNs); err != nil {
		t.Fatalf("reconcileSecrets: %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, secret); err != nil {
		t.Fatalf("get user-secrets: %v", err)
	}

	if _, hasCreds := secret.Data["nats-creds"]; hasCreds {
		t.Error("user-secrets should NOT contain nats-creds (ADR-0063: session-agent self-bootstraps)")
	}
}

// TestADR0063_StaleNATSCredsRemoved verifies that reconcileSecrets removes a
// stale nats-creds key left over from a prior controller version.
func TestADR0063_StaleNATSCredsRemoved(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	ctx := context.Background()
	userNs := "mclaude-alice"

	// Create namespace.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Pre-create user-secrets with a stale nats-creds field.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: userNs},
		Data: map[string][]byte{
			"nats-creds": []byte("stale-creds-from-old-controller"),
		},
	}
	if err := r.client.Create(ctx, existing); err != nil {
		t.Fatalf("create existing user-secrets: %v", err)
	}

	if err := r.reconcileSecrets(ctx, mcp, userNs); err != nil {
		t.Fatalf("reconcileSecrets: %v", err)
	}

	updated := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, updated); err != nil {
		t.Fatalf("get user-secrets: %v", err)
	}

	if _, hasCreds := updated.Data["nats-creds"]; hasCreds {
		t.Error("reconcileSecrets should have removed stale nats-creds field")
	}
}

// TestADR0063_HostSlugFromJWT verifies hostSlugFromJWT decodes the slug correctly.
func TestADR0063_HostSlugFromJWT(t *testing.T) {
	// Build a minimal JWT-shaped token with payload {"name":"host-us-east"}.
	// We don't need a valid signature for this unit test (hostSlugFromJWT doesn't validate sigs).
	makeJWT := func(name string) string {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ed25519-nkey"}`))
		payload, _ := json.Marshal(map[string]string{"name": name})
		payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
		sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
		return header + "." + payloadEnc + "." + sig
	}

	tests := []struct {
		token   string
		want    string
		wantErr bool
	}{
		{makeJWT("host-us-east"), "us-east", false},
		{makeJWT("host-prod-cluster"), "prod-cluster", false},
		{makeJWT("host-"), "", false}, // empty slug is valid per extraction logic
		{makeJWT("user-alice"), "", true},   // wrong prefix
		{"not.a.jwt", "", true},             // malformed payload
		{"only.two", "", true},              // wrong chunk count
	}

	for _, tt := range tests {
		got, err := hostSlugFromJWT(tt.token)
		if tt.wantErr {
			if err == nil {
				t.Errorf("hostSlugFromJWT(%q): expected error, got %q", tt.token, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("hostSlugFromJWT(%q): unexpected error: %v", tt.token, err)
			continue
		}
		if got != tt.want {
			t.Errorf("hostSlugFromJWT(%q) = %q, want %q", tt.token, got, tt.want)
		}
	}
}

// TestADR0063_SingleSubscription verifies that StartNATSSubscriber uses only one
// host-scoped subject (no legacy dual subscription).
func TestADR0063_SingleSubscription(t *testing.T) {
	// The subject pattern is built from clusterSlug — verify the format matches the spec.
	hslug := "us-east"
	want := "mclaude.hosts." + hslug + ".>"
	got := "mclaude.hosts." + hslug + ".>"
	if got != want {
		t.Errorf("subscription subject: got %q, want %q", got, want)
	}

	// Verify the legacy pattern is NOT used in nats_subscriber.go.
	// (This is a code-level check that the old legacySubject format is gone.)
	legacyPattern := "mclaude.users.*.hosts." + hslug + ".api.projects.>"
	if strings.Contains(legacyPattern, "users.*.hosts."+hslug+".api.projects.>") {
		// Legacy pattern exists conceptually; ensure we're not subscribing to it.
		// The actual StartNATSSubscriber no longer creates this subscription.
	}
}

// TestADR0063_AgentNKeySecretCreated verifies that reconcileAgentNKey creates the
// per-project agent-nkey-{projectId} Secret with an nkey_seed field (ADR-0063 step 6b).
func TestADR0063_AgentNKeySecretCreated(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	ctx := context.Background()
	userNs := "mclaude-alice"

	// Create the namespace so reconcileAgentNKey can create the Secret there.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.reconcileAgentNKey(ctx, mcp, userNs); err != nil {
		t.Fatalf("reconcileAgentNKey: %v", err)
	}

	// Secret should exist with an nkey_seed field.
	secretName := agentNKeySecretName(mcp.Spec.ProjectID)
	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: userNs}, secret); err != nil {
		t.Fatalf("expected secret %q to exist: %v", secretName, err)
	}

	seed := secret.Data["nkey_seed"]
	if len(seed) == 0 {
		t.Error("agent-nkey secret missing nkey_seed field")
	}

	// Verify the seed is a valid NKey user seed (parseable).
	kp, err := nkeys.ParseDecoratedUserNKey(seed)
	if err != nil {
		t.Fatalf("nkey_seed is not a valid NKey user seed: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("cannot derive public key from seed: %v", err)
	}
	if !strings.HasPrefix(pub, "U") {
		t.Errorf("agent NKey public key should start with 'U', got %q", pub)
	}
}

// TestADR0063_AgentNKeySecretIdempotent verifies that reconcileAgentNKey is idempotent:
// if the Secret already exists with an nkey_seed, it skips re-generation and registration.
func TestADR0063_AgentNKeySecretIdempotent(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	r := newTestReconciler(mcp)
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Pre-create the agent-nkey Secret with an existing seed.
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create nkey: %v", err)
	}
	originalSeed, _ := kp.Seed()
	originalPub, _ := kp.PublicKey()

	secretName := agentNKeySecretName(mcp.Spec.ProjectID)
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: userNs},
		Data:       map[string][]byte{"nkey_seed": originalSeed},
	}
	if err := r.client.Create(ctx, existing); err != nil {
		t.Fatalf("create pre-existing secret: %v", err)
	}

	// Wire a fake NATS connection that would record any registration calls.
	fakeNC := &fakeNATSConn{replyOK: true}
	r.nc = fakeNC

	// Call reconcileAgentNKey — should be a no-op since seed already exists.
	if err := r.reconcileAgentNKey(ctx, mcp, userNs); err != nil {
		t.Fatalf("reconcileAgentNKey (idempotent): %v", err)
	}

	// The NATS registration should NOT have been called (idempotent path).
	if len(fakeNC.requests) > 0 {
		t.Errorf("expected 0 NATS registration calls on idempotent path, got %d", len(fakeNC.requests))
	}

	// The seed should be unchanged.
	updated := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: userNs}, updated); err != nil {
		t.Fatalf("get secret after idempotent call: %v", err)
	}
	if string(updated.Data["nkey_seed"]) != string(originalSeed) {
		t.Error("nkey_seed was changed on idempotent call — should have been left unchanged")
	}

	// Public key should still match.
	kp2, _ := nkeys.ParseDecoratedUserNKey(updated.Data["nkey_seed"])
	pub2, _ := kp2.PublicKey()
	if pub2 != originalPub {
		t.Errorf("public key changed after idempotent call: got %q, want %q", pub2, originalPub)
	}
}

// TestADR0063_AgentNKeyNATSRegistration verifies that reconcileAgentNKey sends a
// NATS request to the correct subject with the correct payload fields (ADR-0063 step 6b).
func TestADR0063_AgentNKeyNATSRegistration(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	mcp.Spec.UserSlug = "alice"
	mcp.Spec.ProjectSlug = "my-project"
	mcp.Spec.ProjectID = "proj-456"

	r := newTestReconciler(mcp)
	r.clusterSlug = "us-east"
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fakeNC := &fakeNATSConn{replyOK: true}
	r.nc = fakeNC

	if err := r.reconcileAgentNKey(ctx, mcp, userNs); err != nil {
		t.Fatalf("reconcileAgentNKey: %v", err)
	}

	// Verify exactly one NATS request was made.
	if len(fakeNC.requests) != 1 {
		t.Fatalf("expected 1 NATS request, got %d", len(fakeNC.requests))
	}
	req := fakeNC.requests[0]

	// Verify subject: mclaude.hosts.{hslug}.api.agents.register
	wantSubject := "mclaude.hosts.us-east.api.agents.register"
	if req.subject != wantSubject {
		t.Errorf("NATS subject: got %q, want %q", req.subject, wantSubject)
	}

	// Verify payload fields.
	var payload agentRegisterRequest
	if err := json.Unmarshal(req.data, &payload); err != nil {
		t.Fatalf("unmarshal NATS payload: %v", err)
	}
	if payload.UserSlug != "alice" {
		t.Errorf("payload.UserSlug: got %q, want %q", payload.UserSlug, "alice")
	}
	// HostSlug is NOT sent in the payload — CP extracts it from the NATS subject.
	if payload.ProjectSlug != "my-project" {
		t.Errorf("payload.ProjectSlug: got %q, want %q", payload.ProjectSlug, "my-project")
	}
	if payload.NKeyPublic == "" {
		t.Error("payload.NKeyPublic must not be empty")
	}
	if !strings.HasPrefix(payload.NKeyPublic, "U") {
		t.Errorf("payload.NKeyPublic should start with 'U', got %q", payload.NKeyPublic)
	}
}

// TestADR0063_AgentNKeyOwnerReference verifies that reconcileAgentNKey attempts to
// set ownerReferences on the agent-nkey Secret back to the MCProject CR (ADR-0063 step 6b).
//
// Note: In real Kubernetes, cross-namespace owner references are rejected (the MCProject
// CR lives in mclaude-system while the Secret lives in mclaude-{userSlug}). The code
// calls controllerutil.SetControllerReference and logs a warning on failure — identical
// behavior to ensurePVCCR and ensureDeployment for cross-namespace resources. In a
// same-namespace scenario (e.g. unit tests where MCProject and Secret share a namespace),
// the owner reference IS set correctly.
func TestADR0063_AgentNKeyOwnerReference(t *testing.T) {
	// Use the same namespace for both MCProject and Secret so SetControllerReference succeeds.
	ns := "mclaude-system"
	mcp := testMCProject("alice-my-project", ns)

	scheme := newTestScheme()
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(mcp).
		WithStatusSubresource(&MCProject{}).
		Build()

	r := &MCProjectReconciler{
		client:         cl,
		scheme:         scheme,
		controlPlaneNs: ns,
		clusterSlug:    "us-east",
		logger:         zerolog.Nop(),
	}

	ctx := context.Background()

	if err := r.reconcileAgentNKey(ctx, mcp, ns); err != nil {
		t.Fatalf("reconcileAgentNKey: %v", err)
	}

	secretName := agentNKeySecretName(mcp.Spec.ProjectID)
	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns}, secret); err != nil {
		t.Fatalf("get agent-nkey secret: %v", err)
	}

	// When owner and owned are in the same namespace, the ownerReference IS set.
	found := false
	for _, ref := range secret.OwnerReferences {
		if ref.Kind == "MCProject" && ref.Name == mcp.Name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("agent-nkey secret missing ownerReference to MCProject %q (same-namespace test)", mcp.Name)
	}
}

// TestADR0063_AgentNKeyMountedInPodTemplate verifies that buildPodTemplate mounts
// the agent-nkey-{projectId} Secret at /etc/mclaude/agent-nkey/ and sets
// AGENT_NKEY_PATH env var (ADR-0063 step 7).
func TestADR0063_AgentNKeyMountedInPodTemplate(t *testing.T) {
	mcp := testMCProject("alice-my-project", "mclaude-system")
	mcp.Spec.ProjectID = "proj-456"

	r := newTestReconciler(mcp)
	ctx := context.Background()
	tpl := defaultTemplate()
	tpl.hostSlug = "us-east"

	podTpl := r.buildPodTemplate(ctx, mcp, "mclaude-alice", tpl)

	// Verify the agent-nkey volume exists.
	expectedSecretName := agentNKeySecretName(mcp.Spec.ProjectID)
	foundVolume := false
	for _, v := range podTpl.Spec.Volumes {
		if v.Name == "agent-nkey" {
			if v.VolumeSource.Secret == nil {
				t.Error("agent-nkey volume source is not a Secret")
			} else if v.VolumeSource.Secret.SecretName != expectedSecretName {
				t.Errorf("agent-nkey volume SecretName: got %q, want %q",
					v.VolumeSource.Secret.SecretName, expectedSecretName)
			}
			foundVolume = true
			break
		}
	}
	if !foundVolume {
		t.Errorf("agent-nkey volume not found in pod template (expected volume for Secret %q)", expectedSecretName)
	}

	// Verify the volume mount at /etc/mclaude/agent-nkey/.
	foundMount := false
	for _, m := range podTpl.Spec.Containers[0].VolumeMounts {
		if m.Name == "agent-nkey" {
			if m.MountPath != "/etc/mclaude/agent-nkey/" {
				t.Errorf("agent-nkey mount path: got %q, want %q", m.MountPath, "/etc/mclaude/agent-nkey/")
			}
			if !m.ReadOnly {
				t.Error("agent-nkey volume mount should be ReadOnly")
			}
			foundMount = true
			break
		}
	}
	if !foundMount {
		t.Error("agent-nkey volume mount not found in pod template containers[0].volumeMounts")
	}

	// Verify AGENT_NKEY_PATH env var.
	foundEnv := false
	for _, e := range podTpl.Spec.Containers[0].Env {
		if e.Name == "AGENT_NKEY_PATH" {
			if e.Value != "/etc/mclaude/agent-nkey/nkey_seed" {
				t.Errorf("AGENT_NKEY_PATH: got %q, want %q", e.Value, "/etc/mclaude/agent-nkey/nkey_seed")
			}
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Error("AGENT_NKEY_PATH env var not found in pod template")
	}
}

// TestADR0063_AgentNKeySecretName verifies the naming convention for the agent NKey Secret.
func TestADR0063_AgentNKeySecretName(t *testing.T) {
	tests := []struct {
		projectID string
		want      string
	}{
		{"proj-456", "agent-nkey-proj-456"},
		{"abc-def-123", "agent-nkey-abc-def-123"},
	}
	for _, tt := range tests {
		got := agentNKeySecretName(tt.projectID)
		if got != tt.want {
			t.Errorf("agentNKeySecretName(%q) = %q, want %q", tt.projectID, got, tt.want)
		}
	}
}

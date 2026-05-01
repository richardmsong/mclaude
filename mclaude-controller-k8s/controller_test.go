package main

import (
	"context"
	"testing"

	natsjwt "github.com/nats-io/jwt/v2"
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

// newTestScheme returns a runtime.Scheme with all types registered.
func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = AddToScheme(s)
	return s
}

// newTestReconciler creates an MCProjectReconciler with a fake client and test defaults.
func newTestReconciler(objs ...runtime.Object) *MCProjectReconciler {
	scheme := newTestScheme()
	accountKP, _ := nkeys.CreateAccount()

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
		accountKP:              accountKP,
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

// TestGap2_JWTPermissionScoping verifies that generateNATSUserCreds produces
// a JWT with cluster-scoped permissions covering both the legacy ADR-0035 user-scoped
// pattern and the ADR-0054 host-scoped pattern (dual subscription per ADR-0061).
func TestGap2_JWTPermissionScoping(t *testing.T) {
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	jwt, _, err := generateNATSUserCreds(accountKP, "us-east")
	if err != nil {
		t.Fatalf("generateNATSUserCreds: %v", err)
	}

	// Parse the JWT to verify permissions.
	claims, err := natsjwt.DecodeUserClaims(jwt)
	if err != nil {
		t.Fatalf("decode user claims: %v", err)
	}

	// Verify publish permissions — must include both legacy and host-scoped patterns (ADR-0061).
	expectedPub := []string{
		"mclaude.users.*.hosts.us-east.>", // legacy ADR-0035
		"mclaude.hosts.us-east.>",         // ADR-0054 host-scoped (ADR-0061)
		"_INBOX.>",
		"$JS.*.API.>",
		"$SYS.ACCOUNT.*.CONNECT",
		"$SYS.ACCOUNT.*.DISCONNECT",
	}
	if len(claims.Permissions.Pub.Allow) != len(expectedPub) {
		t.Fatalf("pub.allow length: got %d, want %d\npub.allow: %v", len(claims.Permissions.Pub.Allow), len(expectedPub), claims.Permissions.Pub.Allow)
	}
	for i, v := range expectedPub {
		if string(claims.Permissions.Pub.Allow[i]) != v {
			t.Errorf("pub.allow[%d]: got %q, want %q", i, claims.Permissions.Pub.Allow[i], v)
		}
	}

	// Verify subscribe permissions — must include both legacy and host-scoped patterns (ADR-0061).
	expectedSub := []string{
		"mclaude.users.*.hosts.us-east.>", // legacy ADR-0035
		"mclaude.hosts.us-east.>",         // ADR-0054 host-scoped (ADR-0061)
		"_INBOX.>",
		"$JS.*.API.>",
	}
	if len(claims.Permissions.Sub.Allow) != len(expectedSub) {
		t.Fatalf("sub.allow length: got %d, want %d\nsub.allow: %v", len(claims.Permissions.Sub.Allow), len(expectedSub), claims.Permissions.Sub.Allow)
	}
	for i, v := range expectedSub {
		if string(claims.Permissions.Sub.Allow[i]) != v {
			t.Errorf("sub.allow[%d]: got %q, want %q", i, claims.Permissions.Sub.Allow[i], v)
		}
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
	accountKP, _ := nkeys.CreateAccount()
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
		client:    cl,
		scheme:    scheme,
		accountKP: accountKP,
		logger:    zerolog.Nop(),
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

// TestExtractOperation verifies the subject operation extraction for both the
// legacy ADR-0035 user-scoped pattern and the ADR-0054 host-scoped pattern (ADR-0061).
func TestExtractOperation(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		// Legacy ADR-0035 user-scoped pattern.
		{"mclaude.users.alice.hosts.us-east.api.projects.create", "create"},
		{"mclaude.users.bob.hosts.eu-west.api.projects.delete", "delete"},
		{"mclaude.users.alice.hosts.us-east.api.projects.update", "update"},
		{"mclaude.users.alice.hosts.us-east.api.projects.provision", "provision"},
		// ADR-0054 host-scoped pattern (ADR-0061).
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
// Integration test case: "Namespace uses slug — Controller creates namespace
// mclaude-dev-mclaude-local (not UUID)".
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

// TestSessionAgentNATSURL verifies FQDN derivation.
func TestSessionAgentNATSURL(t *testing.T) {
	tests := []struct {
		raw  string
		ns   string
		want string
	}{
		{"nats://nats:4222", "mclaude-system", "nats://nats.mclaude-system.svc.cluster.local:4222"},
		{"nats://nats.example.com:4222", "mclaude-system", "nats://nats.example.com:4222"},
	}
	for _, tt := range tests {
		got := sessionAgentNATSURL(tt.raw, tt.ns)
		if got != tt.want {
			t.Errorf("sessionAgentNATSURL(%q, %q) = %q, want %q", tt.raw, tt.ns, got, tt.want)
		}
	}
}

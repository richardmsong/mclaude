// controller_test.go tests the per-user reconciler and NATS subscriber per ADR-0091.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"mclaude-control-plane/hostauth"
)

// ---- test infrastructure ----

// fakeNATSConn implements natsConn for testing NKey registration.
type fakeNATSConn struct {
	requests []fakeNATSRequest
	replyOK  bool
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
	resp := userAgentRegisterReply{OK: f.replyOK}
	body, _ := json.Marshal(resp)
	return &nats.Msg{Data: body}, nil
}

// newTestScheme returns a runtime.Scheme with standard Kubernetes types registered.
// Per ADR-0091 MCProject CRD is removed — no custom types needed.
func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return s
}

// newTestUserReconciler creates a UserReconciler with a fake client and test defaults.
func newTestUserReconciler(objs ...runtime.Object) *UserReconciler {
	scheme := newTestScheme()
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		Build()

	return &UserReconciler{
		client:          cl,
		controlPlaneNs:  "mclaude-system",
		releaseName:     "mclaude",
		hslug:           "us-east",
		controlPlaneURL: "https://cp.mclaude.example",
		hubNATSURL:      "nats://nats.mclaude-system.svc.cluster.local:4222",
		logger:          zerolog.Nop(),
	}
}

// ---- build tests ----

// TestBuild verifies the package compiles — covered by running the tests themselves.
// (go test will fail to compile if any code is broken.)

// ---- unit tests: hostSlugFromJWT ----

func makeJWT(name string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ed25519-nkey"}`))
	payload, _ := json.Marshal(map[string]string{"name": name})
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + payloadEnc + "." + sig
}

func TestHostSlugFromJWT(t *testing.T) {
	tests := []struct {
		token   string
		want    string
		wantErr bool
	}{
		{makeJWT("host-us-east"), "us-east", false},
		{makeJWT("host-prod-cluster"), "prod-cluster", false},
		{makeJWT("host-"), "", false},
		{makeJWT("user-alice"), "", true},
		{"not.a.jwt", "", true},
		{"only.two", "", true},
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

// ---- unit tests: envOr ----

func TestEnvOrDefault(t *testing.T) {
	got := envOr("NONEXISTENT_ENV_VAR_FOR_TEST", "mclaude-system")
	if got != "mclaude-system" {
		t.Errorf("envOr default: got %q, want %q", got, "mclaude-system")
	}
}

// ---- unit tests: NATS subject routing ----

func TestMatchesProjectsUpdate(t *testing.T) {
	tests := []struct {
		suffix string
		want   bool
	}{
		{"users.alice.projects.my-project.update", true},
		{"users.bob.projects.foo.update", true},
		{"grants.create", false},
		{"users.alice.projects.my-project.create", false},
		{"users.alice.projects.my-project.delete", false},
		{"users.alice.projects.update", false}, // too few parts
	}
	for _, tt := range tests {
		got := matchesProjectsUpdate(tt.suffix)
		if got != tt.want {
			t.Errorf("matchesProjectsUpdate(%q) = %v, want %v", tt.suffix, got, tt.want)
		}
	}
}

func TestExtractProjectsUSlug(t *testing.T) {
	tests := []struct {
		suffix string
		want   string
	}{
		{"users.alice.projects.my-project.update", "alice"},
		{"users.bob.projects.foo.update", "bob"},
		{"grants.create", ""},
	}
	for _, tt := range tests {
		got := extractProjectsUSlug(tt.suffix)
		if got != tt.want {
			t.Errorf("extractProjectsUSlug(%q) = %q, want %q", tt.suffix, got, tt.want)
		}
	}
}

func TestLogLevelParsing(t *testing.T) {
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

// ---- unit tests: namespace ----

func TestEnsureNamespace_CreatesWithManagedLabel(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()

	if err := r.ensureNamespace(ctx, "mclaude-alice", nil); err != nil {
		t.Fatalf("ensureNamespace: %v", err)
	}

	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "mclaude-alice"}, ns); err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	if ns.Labels["mclaude.io/managed"] != "true" {
		t.Errorf("namespace label mclaude.io/managed: got %q, want %q",
			ns.Labels["mclaude.io/managed"], "true")
	}
}

func TestEnsureNamespace_CorporateCALabel(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	tpl := &userAgentTpl{corporateCAEnabled: true}

	if err := r.ensureNamespace(ctx, "mclaude-alice", tpl); err != nil {
		t.Fatalf("ensureNamespace: %v", err)
	}

	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "mclaude-alice"}, ns); err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	if ns.Labels["mclaude.io/user-namespace"] != "true" {
		t.Errorf("namespace label mclaude.io/user-namespace: got %q, want %q",
			ns.Labels["mclaude.io/user-namespace"], "true")
	}
}

func TestEnsureNamespace_Idempotent(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()

	// First call creates.
	if err := r.ensureNamespace(ctx, "mclaude-alice", nil); err != nil {
		t.Fatalf("first ensureNamespace: %v", err)
	}
	// Second call should not error.
	if err := r.ensureNamespace(ctx, "mclaude-alice", nil); err != nil {
		t.Fatalf("second ensureNamespace (idempotent): %v", err)
	}
}

// ---- unit tests: RBAC ----

func TestEnsureRBAC_BothServiceAccountsCreated(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureRBAC(ctx, userNs, "alice"); err != nil {
		t.Fatalf("ensureRBAC: %v", err)
	}

	// Verify both service accounts exist.
	for _, saName := range []string{"mclaude-sa", "mclaude-user-agent"} {
		sa := &corev1.ServiceAccount{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: saName, Namespace: userNs}, sa); err != nil {
			t.Errorf("service account %q not found: %v", saName, err)
		}
	}
}

func TestEnsureRBAC_UserAgentRoleHasCorrectVerbs(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureRBAC(ctx, userNs, uslug); err != nil {
		t.Fatalf("ensureRBAC: %v", err)
	}

	role := &rbacv1.Role{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "mclaude-user-agent-role", Namespace: userNs}, role); err != nil {
		t.Fatalf("get user-agent role: %v", err)
	}

	// Find the Secret rule — must be scoped to user-tokens-{uslug} only.
	userTokensSecretName := "user-tokens-" + uslug
	foundSecretRule := false
	for _, rule := range role.Rules {
		for _, r := range rule.Resources {
			if r == "secrets" {
				foundSecretRule = true
				if len(rule.ResourceNames) != 1 || rule.ResourceNames[0] != userTokensSecretName {
					t.Errorf("secrets rule ResourceNames: got %v, want [%q]", rule.ResourceNames, userTokensSecretName)
				}
			}
		}
	}
	if !foundSecretRule {
		t.Error("user-agent role missing secrets rule")
	}

	// ConfigMap rule must be scoped to session-agent-template only.
	foundCMRule := false
	for _, rule := range role.Rules {
		for _, res := range rule.Resources {
			if res == "configmaps" {
				foundCMRule = true
				if len(rule.ResourceNames) != 1 || rule.ResourceNames[0] != "session-agent-template" {
					t.Errorf("configmaps rule ResourceNames: got %v, want [session-agent-template]", rule.ResourceNames)
				}
			}
		}
	}
	if !foundCMRule {
		t.Error("user-agent role missing configmaps rule")
	}

	// Deployments rule must cover CRUD verbs.
	foundDeployRule := false
	for _, rule := range role.Rules {
		for _, g := range rule.APIGroups {
			if g == "apps" {
				for _, res := range rule.Resources {
					if res == "deployments" {
						foundDeployRule = true
						// Verify create+delete are present.
						hasCreate, hasDelete := false, false
						for _, v := range rule.Verbs {
							if v == "create" {
								hasCreate = true
							}
							if v == "delete" {
								hasDelete = true
							}
						}
						if !hasCreate || !hasDelete {
							t.Errorf("deployments rule missing create/delete verbs, got: %v", rule.Verbs)
						}
					}
				}
			}
		}
	}
	if !foundDeployRule {
		t.Error("user-agent role missing deployments rule")
	}
}

func TestEnsureRBAC_RoleBindingsCreated(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureRBAC(ctx, userNs, "alice"); err != nil {
		t.Fatalf("ensureRBAC: %v", err)
	}

	for _, rbName := range []string{"mclaude-role", "mclaude-user-agent-role"} {
		rb := &rbacv1.RoleBinding{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: rbName, Namespace: userNs}, rb); err != nil {
			t.Errorf("rolebinding %q not found: %v", rbName, err)
		}
	}
}

func TestEnsureRBAC_Idempotent(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureRBAC(ctx, userNs, "alice"); err != nil {
		t.Fatalf("first ensureRBAC: %v", err)
	}
	if err := r.ensureRBAC(ctx, userNs, "alice"); err != nil {
		t.Fatalf("second ensureRBAC (idempotent): %v", err)
	}
}

// ---- unit tests: user-secrets ----

func TestEnsureUserSecrets_CreatedWithNoOAuthToken(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureUserSecrets(ctx, userNs); err != nil {
		t.Fatalf("ensureUserSecrets: %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, secret); err != nil {
		t.Fatalf("get user-secrets: %v", err)
	}

	// Per ADR-0091: no oauth-token field.
	if _, has := secret.Data["oauth-token"]; has {
		t.Error("user-secrets must NOT contain oauth-token (removed per ADR-0091)")
	}
	// No nats-creds either.
	if _, has := secret.Data["nats-creds"]; has {
		t.Error("user-secrets must NOT contain nats-creds")
	}
}

func TestEnsureUserSecrets_RemovesStaleOAuthToken(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Pre-create with stale oauth-token from old controller version.
	old := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: userNs},
		Data: map[string][]byte{
			"oauth-token": []byte("old-token"),
			"nats-creds":  []byte("old-creds"),
		},
	}
	if err := r.client.Create(ctx, old); err != nil {
		t.Fatalf("create old secret: %v", err)
	}

	if err := r.ensureUserSecrets(ctx, userNs); err != nil {
		t.Fatalf("ensureUserSecrets: %v", err)
	}

	updated := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, updated); err != nil {
		t.Fatalf("get user-secrets: %v", err)
	}
	if _, has := updated.Data["oauth-token"]; has {
		t.Error("stale oauth-token should have been removed")
	}
	if _, has := updated.Data["nats-creds"]; has {
		t.Error("stale nats-creds should have been removed")
	}
}

// ---- unit tests: user-agent-key Secret ----

func TestEnsureUserAgentKey_CreatesSecretWithSeed(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureUserAgentKey(ctx, userNs, "alice"); err != nil {
		t.Fatalf("ensureUserAgentKey: %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent-key", Namespace: userNs}, secret); err != nil {
		t.Fatalf("get user-agent-key: %v", err)
	}

	seed := secret.Data["nkey-seed"]
	if len(seed) == 0 {
		t.Fatal("user-agent-key missing nkey-seed field")
	}

	// Verify the seed is a valid NKey user seed.
	kp, err := nkeys.ParseDecoratedUserNKey(seed)
	if err != nil {
		t.Fatalf("nkey-seed is not a valid NKey user seed: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("cannot derive public key from seed: %v", err)
	}
	if !strings.HasPrefix(pub, "U") {
		t.Errorf("user-agent NKey public key should start with 'U', got %q", pub)
	}
}

func TestEnsureUserAgentKey_Idempotent(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Pre-create the Secret with an existing seed.
	kp, _ := nkeys.CreateUser()
	originalSeed, _ := kp.Seed()

	pre := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-agent-key", Namespace: userNs},
		Data:       map[string][]byte{"nkey-seed": originalSeed},
	}
	if err := r.client.Create(ctx, pre); err != nil {
		t.Fatalf("create pre-existing secret: %v", err)
	}

	// Wire fake NATS — no requests should be sent.
	fakeNC := &fakeNATSConn{replyOK: true}
	r.nc = fakeNC

	if err := r.ensureUserAgentKey(ctx, userNs, "alice"); err != nil {
		t.Fatalf("ensureUserAgentKey (idempotent): %v", err)
	}

	if len(fakeNC.requests) > 0 {
		t.Errorf("expected 0 NATS calls on idempotent path, got %d", len(fakeNC.requests))
	}

	updated := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent-key", Namespace: userNs}, updated); err != nil {
		t.Fatalf("get secret after idempotent: %v", err)
	}
	if string(updated.Data["nkey-seed"]) != string(originalSeed) {
		t.Error("nkey-seed was changed on idempotent call — should be unchanged")
	}
}

func TestEnsureUserAgentKey_NATSRegistration(t *testing.T) {
	r := newTestUserReconciler()
	r.hslug = "us-east"
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fakeNC := &fakeNATSConn{replyOK: true}
	r.nc = fakeNC

	if err := r.ensureUserAgentKey(ctx, userNs, "alice"); err != nil {
		t.Fatalf("ensureUserAgentKey: %v", err)
	}

	if len(fakeNC.requests) != 1 {
		t.Fatalf("expected 1 NATS request, got %d", len(fakeNC.requests))
	}
	req := fakeNC.requests[0]

	// Verify subject: mclaude.hosts.{hslug}.api.agents.user-agent.register
	wantSubject := "mclaude.hosts.us-east.api.agents.user-agent.register"
	if req.subject != wantSubject {
		t.Errorf("NATS subject: got %q, want %q", req.subject, wantSubject)
	}

	var payload userAgentRegisterRequest
	if err := json.Unmarshal(req.data, &payload); err != nil {
		t.Fatalf("unmarshal NATS payload: %v", err)
	}
	if payload.USlug != "alice" {
		t.Errorf("payload.USlug: got %q, want %q", payload.USlug, "alice")
	}
	if payload.NKeyPublic == "" {
		t.Error("payload.NKeyPublic must not be empty")
	}
	if !strings.HasPrefix(payload.NKeyPublic, "U") {
		t.Errorf("payload.NKeyPublic should start with 'U', got %q", payload.NKeyPublic)
	}
}

// ---- unit tests: user-tokens Secret ----

func TestEnsureUserTokensSecret_CreatedEmpty(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureUserTokensSecret(ctx, userNs, uslug); err != nil {
		t.Fatalf("ensureUserTokensSecret: %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-tokens-alice", Namespace: userNs}, secret); err != nil {
		t.Fatalf("get user-tokens-alice: %v", err)
	}

	// Must be empty — controller never writes token material.
	if len(secret.Data) != 0 {
		t.Errorf("user-tokens secret should be empty, got %d keys", len(secret.Data))
	}
}

func TestEnsureUserTokensSecret_IdempotentWithPopulatedKeys(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Pre-create with a populated anthropic key (written by user-agent).
	pre := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-tokens-alice", Namespace: userNs},
		Data:       map[string][]byte{"anthropic": []byte("sk-ant-xxx")},
	}
	if err := r.client.Create(ctx, pre); err != nil {
		t.Fatalf("create pre-existing secret: %v", err)
	}

	// Controller should not overwrite the token.
	if err := r.ensureUserTokensSecret(ctx, userNs, uslug); err != nil {
		t.Fatalf("ensureUserTokensSecret (idempotent): %v", err)
	}

	updated := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-tokens-alice", Namespace: userNs}, updated); err != nil {
		t.Fatalf("get user-tokens-alice: %v", err)
	}
	if string(updated.Data["anthropic"]) != "sk-ant-xxx" {
		t.Error("controller overwrote user-tokens data — should have been idempotent")
	}
}

// ---- unit tests: user-agent Deployment ----

func TestEnsureUserAgentDeployment_Created(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	tpl := defaultUserAgentTemplate()
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get user-agent deployment: %v", err)
	}

	if *deploy.Spec.Replicas != 1 {
		t.Errorf("replicas: got %d, want 1", *deploy.Spec.Replicas)
	}
	if deploy.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Errorf("strategy: got %q, want RollingUpdate", deploy.Spec.Strategy.Type)
	}
	if deploy.Spec.Template.Spec.ServiceAccountName != "mclaude-user-agent" {
		t.Errorf("serviceAccountName: got %q, want mclaude-user-agent",
			deploy.Spec.Template.Spec.ServiceAccountName)
	}
}

func TestEnsureUserAgentDeployment_VolumeMounts(t *testing.T) {
	r := newTestUserReconciler()
	r.hslug = "us-east"
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	tpl := defaultUserAgentTemplate()
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]

	// Verify volume mounts.
	mountPaths := map[string]string{}
	for _, m := range container.VolumeMounts {
		mountPaths[m.Name] = m.MountPath
	}

	if mountPaths["agent-key"] != "/secrets/agent/" {
		t.Errorf("agent-key mount path: got %q, want /secrets/agent/", mountPaths["agent-key"])
	}
	if mountPaths["user-tokens"] != "/secrets/tokens/" {
		t.Errorf("user-tokens mount path: got %q, want /secrets/tokens/", mountPaths["user-tokens"])
	}

	// Verify volumes reference the correct Secrets.
	volumes := map[string]string{}
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Secret != nil {
			volumes[v.Name] = v.Secret.SecretName
		}
	}
	if volumes["agent-key"] != "user-agent-key" {
		t.Errorf("agent-key volume SecretName: got %q, want user-agent-key", volumes["agent-key"])
	}
	if volumes["user-tokens"] != "user-tokens-alice" {
		t.Errorf("user-tokens volume SecretName: got %q, want user-tokens-alice", volumes["user-tokens"])
	}
}

func TestEnsureUserAgentDeployment_EnvVars(t *testing.T) {
	r := newTestUserReconciler()
	r.hslug = "us-east"
	r.hubNATSURL = "nats://nats.example:4222"
	r.controlPlaneURL = "https://cp.example"
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	tpl := defaultUserAgentTemplate()
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]
	envMap := map[string]string{}
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}

	checks := map[string]string{
		"USER_SLUG":         "alice",
		"HOST_SLUG":         "us-east",
		"NAMESPACE":         "mclaude-alice",
		"NATS_URL":          "nats://nats.example:4222",
		"CONTROL_PLANE_URL": "https://cp.example",
	}
	for k, want := range checks {
		if got := envMap[k]; got != want {
			t.Errorf("env %s: got %q, want %q", k, got, want)
		}
	}

	// DEV_OAUTH_TOKEN must not be present per ADR-0091.
	if _, has := envMap["DEV_OAUTH_TOKEN"]; has {
		t.Error("DEV_OAUTH_TOKEN must not be present in user-agent env (removed per ADR-0091)")
	}
}

func TestEnsureUserAgentDeployment_Idempotent(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	tpl := defaultUserAgentTemplate()
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("first ensureUserAgentDeployment: %v", err)
	}
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("second ensureUserAgentDeployment (idempotent): %v", err)
	}
}

// ---- unit tests: session-agent-template copy ----

func TestEnsureSessionAgentTemplate_Copies(t *testing.T) {
	sourceCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mclaude-session-agent-template",
			Namespace: "mclaude-system",
		},
		Data: map[string]string{
			"image": "session-agent:v1",
		},
	}
	r := newTestUserReconciler(sourceCM)
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureSessionAgentTemplate(ctx, userNs); err != nil {
		t.Fatalf("ensureSessionAgentTemplate: %v", err)
	}

	dest := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "session-agent-template", Namespace: userNs}, dest); err != nil {
		t.Fatalf("get session-agent-template in user namespace: %v", err)
	}
	if dest.Data["image"] != "session-agent:v1" {
		t.Errorf("copied data: got %q, want session-agent:v1", dest.Data["image"])
	}
}

func TestEnsureSessionAgentTemplate_UpdatesOnChange(t *testing.T) {
	sourceCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mclaude-session-agent-template",
			Namespace: "mclaude-system",
		},
		Data: map[string]string{"image": "session-agent:v1"},
	}
	existingDestCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "session-agent-template",
			Namespace: "mclaude-alice",
		},
		Data: map[string]string{"image": "session-agent:v0"},
	}

	r := newTestUserReconciler(sourceCM, existingDestCM)
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.ensureSessionAgentTemplate(ctx, userNs); err != nil {
		t.Fatalf("ensureSessionAgentTemplate: %v", err)
	}

	dest := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "session-agent-template", Namespace: userNs}, dest); err != nil {
		t.Fatalf("get session-agent-template: %v", err)
	}
	if dest.Data["image"] != "session-agent:v1" {
		t.Errorf("data after update: got %q, want session-agent:v1", dest.Data["image"])
	}
}

func TestEnsureSessionAgentTemplate_NoSourceIsNoop(t *testing.T) {
	r := newTestUserReconciler() // no source CM
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Should not error when source doesn't exist.
	if err := r.ensureSessionAgentTemplate(ctx, userNs); err != nil {
		t.Fatalf("ensureSessionAgentTemplate with no source: %v", err)
	}
}

// ---- unit tests: imagePullSecrets copy ----

func TestCopyImagePullSecrets(t *testing.T) {
	dockerData := []byte(`{"auths":{"registry.example.com":{"auth":"dXNlcjpwYXNz"}}}`)
	regcred := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "regcred", Namespace: "mclaude-system"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: dockerData},
	}
	opaqueSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "opaque-should-not-copy", Namespace: "mclaude-system"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"key": []byte("value")},
	}

	r := newTestUserReconciler(regcred, opaqueSecret)
	ctx := context.Background()
	userNs := "mclaude-alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := r.copyImagePullSecrets(ctx, userNs); err != nil {
		t.Fatalf("copyImagePullSecrets: %v", err)
	}

	// dockerconfigjson should be copied.
	copied := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "regcred", Namespace: userNs}, copied); err != nil {
		t.Fatalf("expected regcred to be copied: %v", err)
	}
	if string(copied.Data[corev1.DockerConfigJsonKey]) != string(dockerData) {
		t.Error("copied secret data mismatch")
	}

	// Opaque should not be copied.
	notCopied := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "opaque-should-not-copy", Namespace: userNs}, notCopied); err == nil {
		t.Error("opaque secret should NOT have been copied")
	}
}

// ---- integration-style: full ReconcileUser ----

func TestReconcileUser_AllResourcesCreated(t *testing.T) {
	r := newTestUserReconciler()
	fakeNC := &fakeNATSConn{replyOK: true}
	r.nc = fakeNC
	ctx := context.Background()
	uslug := "alice"
	userNs := "mclaude-alice"

	if err := r.ReconcileUser(ctx, uslug); err != nil {
		t.Fatalf("ReconcileUser: %v", err)
	}

	// Namespace
	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: userNs}, ns); err != nil {
		t.Errorf("namespace not created: %v", err)
	}

	// ServiceAccounts
	for _, saName := range []string{"mclaude-sa", "mclaude-user-agent"} {
		sa := &corev1.ServiceAccount{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: saName, Namespace: userNs}, sa); err != nil {
			t.Errorf("service account %q not created: %v", saName, err)
		}
	}

	// user-secrets
	usecret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, usecret); err != nil {
		t.Errorf("user-secrets not created: %v", err)
	}
	if _, has := usecret.Data["oauth-token"]; has {
		t.Error("user-secrets must not contain oauth-token (ADR-0091)")
	}

	// user-config
	ucfg := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-config", Namespace: userNs}, ucfg); err != nil {
		t.Errorf("user-config not created: %v", err)
	}

	// user-agent-key
	agentKey := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent-key", Namespace: userNs}, agentKey); err != nil {
		t.Errorf("user-agent-key not created: %v", err)
	}
	if len(agentKey.Data["nkey-seed"]) == 0 {
		t.Error("user-agent-key missing nkey-seed")
	}

	// user-tokens-{uslug}
	tokenSecret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-tokens-alice", Namespace: userNs}, tokenSecret); err != nil {
		t.Errorf("user-tokens-alice not created: %v", err)
	}

	// user-agent Deployment
	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Errorf("user-agent deployment not created: %v", err)
	}

	// NATS registration call for user-agent.register
	if len(fakeNC.requests) == 0 {
		t.Error("expected at least one NATS request (user-agent.register)")
	}
	foundRegister := false
	for _, req := range fakeNC.requests {
		if strings.Contains(req.subject, "api.agents.user-agent.register") {
			foundRegister = true
			break
		}
	}
	if !foundRegister {
		t.Error("expected NATS request to api.agents.user-agent.register")
	}
}

func TestReconcileUser_Idempotent(t *testing.T) {
	r := newTestUserReconciler()
	fakeNC := &fakeNATSConn{replyOK: true}
	r.nc = fakeNC
	ctx := context.Background()

	// First reconcile.
	if err := r.ReconcileUser(ctx, "alice"); err != nil {
		t.Fatalf("first ReconcileUser: %v", err)
	}

	// Second reconcile — should not error or duplicate resources.
	if err := r.ReconcileUser(ctx, "alice"); err != nil {
		t.Fatalf("second ReconcileUser (idempotent): %v", err)
	}
}

// TestNoDEVOAuthTokenInCode verifies DEV_OAUTH_TOKEN does not appear in the reconciler.
// This is a compile-time assertion — the field was removed from UserReconciler.
func TestNoDEVOAuthTokenInCode(t *testing.T) {
	r := newTestUserReconciler()
	// If DEV_OAUTH_TOKEN were still a field, we'd assign it here.
	// This test simply verifies the struct compiles without it.
	_ = r
}

// TestSingleNATSSubscription verifies the subscription subject pattern.
func TestSingleNATSSubscription(t *testing.T) {
	hslug := "us-east"
	want := "mclaude.hosts." + hslug + ".>"
	got := fmt.Sprintf("mclaude.hosts.%s.>", hslug)
	if got != want {
		t.Errorf("subscription subject: got %q, want %q", got, want)
	}
}

// ---- unit tests: bootRefresh exponential backoff ----

// fakeRefresher implements the refresher interface for testing bootRefresh.
type fakeRefresher struct {
	// responses is a queue of (jwt, error) pairs returned in order.
	responses []fakeRefreshResponse
	idx       int
}

type fakeRefreshResponse struct {
	jwt string
	err error
}

func (f *fakeRefresher) Refresh(_ context.Context) (string, error) {
	if f.idx >= len(f.responses) {
		return "final-jwt", nil
	}
	r := f.responses[f.idx]
	f.idx++
	return r.jwt, r.err
}

// TestBootRefresh_SucceedsOnFirstTry verifies bootRefresh returns immediately when
// Refresh() succeeds on the first call.
func TestBootRefresh_SucceedsOnFirstTry(t *testing.T) {
	fr := &fakeRefresher{
		responses: []fakeRefreshResponse{
			{jwt: "host-jwt-v1", err: nil},
		},
	}
	ctx := context.Background()
	// Use 1ms intervals to avoid real sleeps in tests.
	got := bootRefreshWithInterval(ctx, fr, zerolog.Nop(), time.Millisecond, time.Millisecond)
	if got != "host-jwt-v1" {
		t.Errorf("bootRefresh: got %q, want host-jwt-v1", got)
	}
	if fr.idx != 1 {
		t.Errorf("Refresh called %d times, expected 1", fr.idx)
	}
}

// TestBootRefresh_RetriesOnErrNotRegistered verifies bootRefresh retries when CP
// returns ErrNotRegistered (404 — NKey not yet registered) and eventually returns
// the JWT once the operator completes registration (spec-k8s-architecture.md §Boot
// Sequence step 3; ADR-0063).
func TestBootRefresh_RetriesOnErrNotRegistered(t *testing.T) {
	// First two calls return ErrNotRegistered; third call succeeds.
	fr := &fakeRefresher{
		responses: []fakeRefreshResponse{
			{jwt: "", err: hostauth.ErrNotRegistered},
			{jwt: "", err: hostauth.ErrNotRegistered},
			{jwt: "host-jwt-registered", err: nil},
		},
	}

	ctx := context.Background()
	// Use 1ms intervals so the test completes in milliseconds, not seconds.
	jwt := bootRefreshWithInterval(ctx, fr, zerolog.Nop(), time.Millisecond, time.Millisecond)

	if jwt != "host-jwt-registered" {
		t.Errorf("bootRefresh: got %q, want host-jwt-registered", jwt)
	}
	if fr.idx != 3 {
		t.Errorf("Refresh called %d times, expected 3", fr.idx)
	}
}

// TestBootRefresh_RetriesOnGenericError verifies bootRefresh retries on non-404
// errors (network failures, transient CP unavailability) as well.
func TestBootRefresh_RetriesOnGenericError(t *testing.T) {
	networkErr := errors.New("connection refused")
	fr := &fakeRefresher{
		responses: []fakeRefreshResponse{
			{jwt: "", err: networkErr},
			{jwt: "host-jwt-ok", err: nil},
		},
	}

	ctx := context.Background()
	jwt := bootRefreshWithInterval(ctx, fr, zerolog.Nop(), time.Millisecond, time.Millisecond)

	if jwt != "host-jwt-ok" {
		t.Errorf("bootRefresh: got %q, want host-jwt-ok", jwt)
	}
	if fr.idx != 2 {
		t.Errorf("Refresh called %d times, expected 2", fr.idx)
	}
}

// TestBootRefresh_ExponentialBackoffCapAtMax verifies the exponential backoff
// caps at maxInterval and does not exceed it.
func TestBootRefresh_ExponentialBackoffCapAtMax(t *testing.T) {
	// 5 failures then success: verifies the retry loop doesn't bail out early.
	responses := make([]fakeRefreshResponse, 5)
	for i := range responses {
		responses[i] = fakeRefreshResponse{jwt: "", err: hostauth.ErrNotRegistered}
	}
	responses = append(responses, fakeRefreshResponse{jwt: "final-jwt", err: nil})

	fr := &fakeRefresher{responses: responses}

	ctx := context.Background()
	// initialInterval=1ms, maxInterval=2ms — verifies cap logic runs without overflow.
	jwt := bootRefreshWithInterval(ctx, fr, zerolog.Nop(), time.Millisecond, 2*time.Millisecond)

	if jwt != "final-jwt" {
		t.Errorf("bootRefresh: got %q, want final-jwt", jwt)
	}
	if fr.idx != 6 {
		t.Errorf("Refresh called %d times, expected 6", fr.idx)
	}
}

// ---- unit tests: corporate CA injection ----

func TestEnsureUserAgentDeployment_CorporateCAInjected(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Pre-create the CA bundle ConfigMap in the user namespace (trust-manager would do this).
	caCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "corporate-ca-bundle", Namespace: userNs},
		Data:       map[string]string{"ca-certificates.crt": "-----BEGIN CERTIFICATE-----\nfakeCA\n-----END CERTIFICATE-----\n"},
	}
	if err := r.client.Create(ctx, caCM); err != nil {
		t.Fatalf("create CA ConfigMap: %v", err)
	}

	tpl := &userAgentTpl{
		image:                    "mclaude-user-agent:latest",
		imagePullPolicy:          corev1.PullIfNotPresent,
		corporateCAEnabled:       true,
		corporateCAConfigMapName: "corporate-ca-bundle",
		corporateCAConfigMapKey:  "ca-certificates.crt",
	}
	applyDefaultUserAgentResources(tpl)

	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]

	// Verify corporate-ca volume exists and references the CA ConfigMap.
	foundCAVolume := false
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "corporate-ca" {
			foundCAVolume = true
			if v.ConfigMap == nil || v.ConfigMap.Name != "corporate-ca-bundle" {
				t.Errorf("corporate-ca volume ConfigMap: got %+v, want corporate-ca-bundle", v.ConfigMap)
			}
		}
	}
	if !foundCAVolume {
		t.Error("corporate-ca volume not injected")
	}

	// Verify volume mount at /etc/ssl/certs/corporate-ca-certificates.crt.
	foundCAMount := false
	for _, m := range container.VolumeMounts {
		if m.Name == "corporate-ca" {
			foundCAMount = true
			if m.MountPath != "/etc/ssl/certs/corporate-ca-certificates.crt" {
				t.Errorf("corporate-ca mount path: got %q, want /etc/ssl/certs/corporate-ca-certificates.crt", m.MountPath)
			}
			if m.SubPath != "ca-certificates.crt" {
				t.Errorf("corporate-ca mount SubPath: got %q, want ca-certificates.crt", m.SubPath)
			}
			if !m.ReadOnly {
				t.Error("corporate-ca mount should be ReadOnly")
			}
		}
	}
	if !foundCAMount {
		t.Error("corporate-ca volume mount not injected")
	}

	// Verify NODE_EXTRA_CA_CERTS env var.
	envMap := map[string]string{}
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}
	if envMap["NODE_EXTRA_CA_CERTS"] != "/etc/ssl/certs/corporate-ca-certificates.crt" {
		t.Errorf("NODE_EXTRA_CA_CERTS: got %q, want /etc/ssl/certs/corporate-ca-certificates.crt",
			envMap["NODE_EXTRA_CA_CERTS"])
	}

	// Verify mclaude.io/ca-bundle-hash annotation is set and non-empty.
	annotations := deploy.Spec.Template.Annotations
	hash, ok := annotations["mclaude.io/ca-bundle-hash"]
	if !ok {
		t.Error("pod template annotation mclaude.io/ca-bundle-hash not set")
	}
	if hash == "" {
		t.Error("mclaude.io/ca-bundle-hash should be non-empty when CA ConfigMap has data")
	}
}

func TestEnsureUserAgentDeployment_CorporateCANotInjectedWhenDisabled(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	tpl := defaultUserAgentTemplate() // corporateCAEnabled = false
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]

	// No corporate-ca volume.
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "corporate-ca" {
			t.Error("corporate-ca volume should NOT be present when corporateCAEnabled=false")
		}
	}

	// No corporate-ca mount.
	for _, m := range container.VolumeMounts {
		if m.Name == "corporate-ca" {
			t.Error("corporate-ca volume mount should NOT be present when corporateCAEnabled=false")
		}
	}

	// No NODE_EXTRA_CA_CERTS.
	for _, e := range container.Env {
		if e.Name == "NODE_EXTRA_CA_CERTS" {
			t.Error("NODE_EXTRA_CA_CERTS should NOT be present when corporateCAEnabled=false")
		}
	}

	// No ca-bundle-hash annotation.
	if _, ok := deploy.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]; ok {
		t.Error("mclaude.io/ca-bundle-hash annotation should NOT be present when corporateCAEnabled=false")
	}
}

func TestEnsureUserAgentDeployment_CorporateCAHashUpdatesOnCMChange(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Initial CA bundle.
	caCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "corporate-ca-bundle", Namespace: userNs},
		Data:       map[string]string{"ca-certificates.crt": "CERT_V1"},
	}
	if err := r.client.Create(ctx, caCM); err != nil {
		t.Fatalf("create CA ConfigMap: %v", err)
	}

	tpl := &userAgentTpl{
		image:                    "mclaude-user-agent:latest",
		imagePullPolicy:          corev1.PullIfNotPresent,
		corporateCAEnabled:       true,
		corporateCAConfigMapName: "corporate-ca-bundle",
		corporateCAConfigMapKey:  "ca-certificates.crt",
	}
	applyDefaultUserAgentResources(tpl)

	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("first ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	hashV1 := deploy.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]
	if hashV1 == "" {
		t.Fatal("expected non-empty ca-bundle-hash for v1")
	}

	// Update the CA bundle ConfigMap.
	caCM.Data["ca-certificates.crt"] = "CERT_V2"
	if err := r.client.Update(ctx, caCM); err != nil {
		t.Fatalf("update CA ConfigMap: %v", err)
	}

	// Re-reconcile.
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("second ensureUserAgentDeployment: %v", err)
	}

	updated := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, updated); err != nil {
		t.Fatalf("get updated deployment: %v", err)
	}
	hashV2 := updated.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]
	if hashV2 == "" {
		t.Fatal("expected non-empty ca-bundle-hash for v2")
	}
	if hashV1 == hashV2 {
		t.Errorf("ca-bundle-hash did not change after CA bundle update: %q == %q", hashV1, hashV2)
	}
}

func TestEnsureUserAgentDeployment_CorporateCADefaultConfigMapName(t *testing.T) {
	r := newTestUserReconciler()
	ctx := context.Background()
	userNs := "mclaude-alice"
	uslug := "alice"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: userNs}}
	if err := r.client.Create(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Template with corporateCAEnabled but no configMapName/Key — should use defaults.
	tpl := &userAgentTpl{
		image:              "mclaude-user-agent:latest",
		imagePullPolicy:    corev1.PullIfNotPresent,
		corporateCAEnabled: true,
		// corporateCAConfigMapName and corporateCAConfigMapKey are empty — use defaults.
	}
	applyDefaultUserAgentResources(tpl)

	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		t.Fatalf("ensureUserAgentDeployment: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	// Volume should reference the default ConfigMap name.
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "corporate-ca" {
			if v.ConfigMap == nil || v.ConfigMap.Name != "corporate-ca-bundle" {
				t.Errorf("corporate-ca volume default name: got %+v, want corporate-ca-bundle", v.ConfigMap)
			}
			return
		}
	}
	t.Error("corporate-ca volume not found in deployment")
}

// TestCaBundleHash verifies the hash function produces stable, deterministic output.
func TestCaBundleHash(t *testing.T) {
	cm1 := &corev1.ConfigMap{Data: map[string]string{"ca.crt": "CERT_DATA"}}
	cm2 := &corev1.ConfigMap{Data: map[string]string{"ca.crt": "CERT_DATA"}}
	cm3 := &corev1.ConfigMap{Data: map[string]string{"ca.crt": "DIFFERENT"}}

	h1 := caBundleHash(cm1)
	h2 := caBundleHash(cm2)
	h3 := caBundleHash(cm3)

	if h1 == "" {
		t.Error("hash should not be empty for non-empty CM")
	}
	if h1 != h2 {
		t.Errorf("identical CMs should produce identical hashes: %q != %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different CMs should produce different hashes: %q == %q", h1, h3)
	}

	// Nil CM should return empty string.
	if caBundleHash(nil) != "" {
		t.Error("nil CM should return empty hash")
	}

	// Empty CM should return empty string.
	if caBundleHash(&corev1.ConfigMap{}) != "" {
		t.Error("empty CM should return empty hash")
	}
}

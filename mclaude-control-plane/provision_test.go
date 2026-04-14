package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestProvisioner returns a K8sProvisioner backed by a fake k8s client.
func newTestProvisioner(t *testing.T) *K8sProvisioner {
	t.Helper()
	nkp, err := GenerateAccountNKey()
	if err != nil {
		t.Fatalf("GenerateAccountNKey: %v", err)
	}
	return &K8sProvisioner{
		client:              fake.NewClientset(),
		controlPlaneNs:      "mclaude-system",
		releaseName:         "mclaude",
		sessionAgentNATSURL: "nats://nats:4222",
		accountKP:           nkp.KeyPair,
	}
}

// TestEnsureUserSecrets_SecretMissing verifies that ensureUserSecrets creates a
// new Secret with the nats-creds key when no Secret exists yet.
func TestEnsureUserSecrets_SecretMissing(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-alice"

	if err := p.ensureUserSecrets(ctx, ns, "alice"); err != nil {
		t.Fatalf("ensureUserSecrets: %v", err)
	}

	secret, err := p.client.CoreV1().Secrets(ns).Get(ctx, "user-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get user-secrets: %v", err)
	}
	if len(secret.Data["nats-creds"]) == 0 {
		t.Error("expected nats-creds to be populated; got empty")
	}
}

// TestEnsureUserSecrets_SecretExistsWithCreds verifies that ensureUserSecrets
// returns nil without modifying a Secret that already has nats-creds.
func TestEnsureUserSecrets_SecretExistsWithCreds(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-bob"

	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: ns},
		Data: map[string][]byte{
			"nats-creds": []byte("existing-creds-value"),
		},
	}
	if _, err := p.client.CoreV1().Secrets(ns).Create(ctx, existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("pre-create Secret: %v", err)
	}

	if err := p.ensureUserSecrets(ctx, ns, "bob"); err != nil {
		t.Fatalf("ensureUserSecrets: %v", err)
	}

	secret, err := p.client.CoreV1().Secrets(ns).Get(ctx, "user-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get user-secrets: %v", err)
	}
	if string(secret.Data["nats-creds"]) != "existing-creds-value" {
		t.Errorf("nats-creds was overwritten; got %q", string(secret.Data["nats-creds"]))
	}
}

// TestEnsureUserSecrets_SecretExistsEmptyData verifies that ensureUserSecrets
// patches an existing Secret that has no nats-creds key (the empty-Secret bug).
func TestEnsureUserSecrets_SecretExistsEmptyData(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-carol"

	empty := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: ns},
	}
	if _, err := p.client.CoreV1().Secrets(ns).Create(ctx, empty, metav1.CreateOptions{}); err != nil {
		t.Fatalf("pre-create empty Secret: %v", err)
	}

	if err := p.ensureUserSecrets(ctx, ns, "carol"); err != nil {
		t.Fatalf("ensureUserSecrets: %v", err)
	}

	secret, err := p.client.CoreV1().Secrets(ns).Get(ctx, "user-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get user-secrets after update: %v", err)
	}
	if len(secret.Data["nats-creds"]) == 0 {
		t.Error("expected nats-creds to be populated after update; got empty")
	}
}

// TestEnsureUserSecrets_SecretExistsNilData covers nil Data map (defensive nil check).
func TestEnsureUserSecrets_SecretExistsNilData(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-dave"

	nilData := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: ns},
		Data:       nil,
	}
	if _, err := p.client.CoreV1().Secrets(ns).Create(ctx, nilData, metav1.CreateOptions{}); err != nil {
		t.Fatalf("pre-create nil-data Secret: %v", err)
	}

	if err := p.ensureUserSecrets(ctx, ns, "dave"); err != nil {
		t.Fatalf("ensureUserSecrets: %v", err)
	}

	secret, err := p.client.CoreV1().Secrets(ns).Get(ctx, "user-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get user-secrets: %v", err)
	}
	if len(secret.Data["nats-creds"]) == 0 {
		t.Error("expected nats-creds to be populated; got empty")
	}
}

// TestEnsureUserSecrets_Idempotent verifies that calling ensureUserSecrets twice works.
func TestEnsureUserSecrets_Idempotent(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-eve"

	if err := p.ensureUserSecrets(ctx, ns, "eve"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := p.ensureUserSecrets(ctx, ns, "eve"); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

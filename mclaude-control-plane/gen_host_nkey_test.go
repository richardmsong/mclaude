package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// nopLogger returns a zerolog.Logger that discards all output — suitable for unit tests.
func nopGenLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// TestGenHostNkey_CreatesSecret verifies that genHostNkeyWithClient creates a K8s Secret
// with a valid U-prefix NKey seed in the nkey_seed field when no Secret exists.
func TestGenHostNkey_CreatesSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()
	logger := nopGenLogger()

	err := genHostNkeyWithClient(ctx, logger, client, "mclaude-system", "mclaude-worker-host-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient: %v", err)
	}

	secret, err := client.CoreV1().Secrets("mclaude-system").Get(ctx, "mclaude-worker-host-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created Secret: %v", err)
	}

	seed, ok := secret.Data["nkey_seed"]
	if !ok || len(seed) == 0 {
		t.Fatal("Secret missing nkey_seed field")
	}

	// Seed must be a valid NKey user seed (U-prefix per ADR-0063).
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		t.Fatalf("nkey_seed is not a valid NKey seed: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("extract public key from seed: %v", err)
	}
	if !strings.HasPrefix(pub, "U") {
		t.Errorf("host NKey public key must start with 'U' (U-prefix); got %q", pub[:1])
	}
}

// TestGenHostNkey_Idempotent verifies that when the Secret already exists,
// genHostNkeyWithClient exits 0 without overwriting the existing key.
func TestGenHostNkey_Idempotent(t *testing.T) {
	// Pre-create a Secret with a known seed.
	existingKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create test NKey: %v", err)
	}
	existingSeed, _ := existingKP.Seed()

	preExisting := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mclaude-worker-host-creds",
			Namespace: "mclaude-system",
		},
		Data: map[string][]byte{"nkey_seed": existingSeed},
	}
	client := fake.NewSimpleClientset(preExisting)
	ctx := context.Background()
	logger := nopGenLogger()

	err = genHostNkeyWithClient(ctx, logger, client, "mclaude-system", "mclaude-worker-host-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient (idempotent): %v", err)
	}

	// Secret must not have been changed.
	secret, err := client.CoreV1().Secrets("mclaude-system").Get(ctx, "mclaude-worker-host-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Secret after idempotent call: %v", err)
	}
	if !bytes.Equal(secret.Data["nkey_seed"], existingSeed) {
		t.Error("nkey_seed was overwritten on idempotent call — must remain unchanged")
	}
}

// TestGenHostNkey_UniqueKeysPerInstall verifies that two independent calls generate
// different NKey pairs (ensuring the RNG is not degenerate).
func TestGenHostNkey_UniqueKeysPerInstall(t *testing.T) {
	ctx := context.Background()
	logger := nopGenLogger()

	client1 := fake.NewSimpleClientset()
	if err := genHostNkeyWithClient(ctx, logger, client1, "ns", "s1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	client2 := fake.NewSimpleClientset()
	if err := genHostNkeyWithClient(ctx, logger, client2, "ns", "s1"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	s1, _ := client1.CoreV1().Secrets("ns").Get(ctx, "s1", metav1.GetOptions{})
	s2, _ := client2.CoreV1().Secrets("ns").Get(ctx, "s1", metav1.GetOptions{})

	if bytes.Equal(s1.Data["nkey_seed"], s2.Data["nkey_seed"]) {
		t.Error("two independent NKey generations produced the same seed — RNG broken?")
	}
}

// TestGenHostNkey_LogsPublicKey verifies that the public key is logged on successful creation.
func TestGenHostNkey_LogsPublicKey(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	client := fake.NewSimpleClientset()
	ctx := context.Background()

	err := genHostNkeyWithClient(ctx, logger, client, "mclaude-system", "test-host-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "nkeyPublic") {
		t.Errorf("log output missing nkeyPublic field; got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "generated host NKey") {
		t.Errorf("log output missing 'generated host NKey' message; got: %q", logOutput)
	}
}

// TestGenHostNkey_SkipLogOnExisting verifies that when the Secret already exists,
// the "already exists — skipping" message is logged.
func TestGenHostNkey_SkipLogOnExisting(t *testing.T) {
	preExisting := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-creds",
			Namespace: "ns",
		},
		Data: map[string][]byte{"nkey_seed": []byte("SUABC")},
	}
	client := fake.NewSimpleClientset(preExisting)

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	ctx := context.Background()

	err := genHostNkeyWithClient(ctx, logger, client, "ns", "existing-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "skipping") {
		t.Errorf("log output missing 'skipping' message on existing Secret; got: %q", logOutput)
	}
}

// TestGenHostNkey_SecretHasLabel verifies that the created Secret has the
// app.kubernetes.io/managed-by label set to "mclaude-gen-host-nkey".
func TestGenHostNkey_SecretHasLabel(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()

	err := genHostNkeyWithClient(ctx, nopGenLogger(), client, "mclaude-system", "worker-host-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient: %v", err)
	}

	secret, err := client.CoreV1().Secrets("mclaude-system").Get(ctx, "worker-host-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Secret: %v", err)
	}

	got := secret.Labels["app.kubernetes.io/managed-by"]
	if got != "mclaude-gen-host-nkey" {
		t.Errorf("label app.kubernetes.io/managed-by = %q; want %q", got, "mclaude-gen-host-nkey")
	}
}

// TestGenHostNkey_SecretHasResourcePolicyKeepAnnotation verifies that the created Secret
// carries helm.sh/resource-policy: keep so Helm does not delete it on helm uninstall
// or when the chart template is removed (ADR-0074).
func TestGenHostNkey_SecretHasResourcePolicyKeepAnnotation(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()

	err := genHostNkeyWithClient(ctx, nopGenLogger(), client, "mclaude-system", "worker-host-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient: %v", err)
	}

	secret, err := client.CoreV1().Secrets("mclaude-system").Get(ctx, "worker-host-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Secret: %v", err)
	}

	got := secret.Annotations["helm.sh/resource-policy"]
	if got != "keep" {
		t.Errorf("annotation helm.sh/resource-policy = %q; want %q", got, "keep")
	}
}

// TestGenHostNkey_SeedRoundTrip verifies that the stored seed can be parsed back
// by nkeys.ParseDecoratedUserNKey (the function used by mclaude-controller-k8s
// via hostauth.NewHostAuthFromSeed).
func TestGenHostNkey_SeedRoundTrip(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()

	err := genHostNkeyWithClient(ctx, nopGenLogger(), client, "ns", "rt-creds")
	if err != nil {
		t.Fatalf("genHostNkeyWithClient: %v", err)
	}

	secret, _ := client.CoreV1().Secrets("ns").Get(ctx, "rt-creds", metav1.GetOptions{})
	seed := secret.Data["nkey_seed"]

	// Verify the seed is parseable by the same function the controller uses.
	kp, err := nkeys.ParseDecoratedUserNKey(seed)
	if err != nil {
		t.Fatalf("nkeys.ParseDecoratedUserNKey: %v — seed stored in Secret not usable by controller", err)
	}

	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("extract public key: %v", err)
	}
	if !strings.HasPrefix(pub, "U") {
		t.Errorf("round-tripped key must have U prefix, got %q", pub[:1])
	}
}

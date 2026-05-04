package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// makeProvisionerConfig builds a test AgentConfig.
func makeProvisionerConfig(uslug, hslug string) *AgentConfig {
	return &AgentConfig{
		UserSlug:        uslug,
		HostSlug:        hslug,
		Namespace:       "mclaude-" + uslug,
		NATSURL:         "nats://nats:4222",
		ControlPlaneURL: "http://cp:8080",
	}
}

// newTestProvisioner creates a Provisioner with a temp token dir and writes a token.
func newTestProvisioner(t *testing.T, uslug, hslug, provider, tokenValue string) (*Provisioner, *fake.Clientset) {
	t.Helper()
	cfg := makeProvisionerConfig(uslug, hslug)
	ns := cfg.Namespace

	fakeK8s := fake.NewClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "session-agent-template",
				Namespace: ns,
			},
			Data: map[string]string{
				"image":          "mclaude-session-agent:test",
				"projectPvcSize": "5Gi",
				"nixPvcSize":     "5Gi",
			},
		},
	)

	tokenBaseDir := t.TempDir()
	tokensDir := filepath.Join(tokenBaseDir, "tokens")
	if err := os.MkdirAll(tokensDir, 0755); err != nil {
		t.Fatalf("MkdirAll tokens dir: %v", err)
	}
	if tokenValue != "" {
		if err := os.WriteFile(filepath.Join(tokensDir, provider), []byte(tokenValue), 0600); err != nil {
			t.Fatalf("write token file: %v", err)
		}
	}

	p := NewProvisionerWithTokenDir(fakeK8s, ns, cfg, tokenBaseDir, zerolog.Nop())
	return p, fakeK8s
}

// TestProvisioner_HostNotTrusted verifies that provisioning fails with host_not_trusted
// when the token file is absent.
func TestProvisioner_HostNotTrusted(t *testing.T) {
	cfg := makeProvisionerConfig("alice", "mycluster")
	ns := cfg.Namespace

	fakeK8s := fake.NewClientset()
	// Empty tokenBaseDir — no token files written.
	p := NewProvisionerWithTokenDir(fakeK8s, ns, cfg, t.TempDir(), zerolog.Nop())

	req := ProvisionRequest{
		ProjectID:   "proj-001",
		ProjectSlug: "my-project",
		Provider:    "anthropic",
	}

	reply := p.Provision(context.Background(), req)
	if reply.OK {
		t.Error("expected ok=false when no token file exists")
	}
	if reply.Error != "host_not_trusted" {
		t.Errorf("expected error=host_not_trusted, got %q", reply.Error)
	}
}

// TestProvisioner_ProvisionCreatesDeploymentAndPVCs verifies that Provision
// creates the project Deployment and both PVCs when a token is available.
func TestProvisioner_ProvisionCreatesDeploymentAndPVCs(t *testing.T) {
	p, fakeK8s := newTestProvisioner(t, "alice", "mycluster", "anthropic", "sk-ant-test")
	ns := p.namespace

	req := ProvisionRequest{
		ProjectID:   "proj-001",
		ProjectSlug: "my-project",
		GitURL:      "https://github.com/example/repo.git",
		UserID:      "user-uuid-001",
		Provider:    "anthropic",
	}

	reply := p.Provision(context.Background(), req)
	if !reply.OK {
		t.Fatalf("expected ok=true, got error=%q", reply.Error)
	}
	if reply.Status != "provisioning" {
		t.Errorf("expected status=provisioning, got %q", reply.Status)
	}

	// Verify Deployment was created.
	deploy, err := fakeK8s.AppsV1().Deployments(ns).Get(
		context.Background(), "project-proj-001", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if deploy.Name != "project-proj-001" {
		t.Errorf("deploy name = %q", deploy.Name)
	}

	// Verify both PVCs were created.
	_, err = fakeK8s.CoreV1().PersistentVolumeClaims(ns).Get(
		context.Background(), "project-proj-001", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get project PVC: %v", err)
	}

	_, err = fakeK8s.CoreV1().PersistentVolumeClaims(ns).Get(
		context.Background(), "nix-proj-001", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get nix PVC: %v", err)
	}
}

// TestProvisioner_DeploymentEnvVars verifies that all required env vars are set
// on the session-agent container per ADR-0091 spec.
func TestProvisioner_DeploymentEnvVars(t *testing.T) {
	p, fakeK8s := newTestProvisioner(t, "alice", "mycluster", "anthropic", "sk-ant-test")
	ns := p.namespace

	req := ProvisionRequest{
		ProjectID:     "proj-env",
		ProjectSlug:   "env-project",
		GitURL:        "https://github.com/example/env-repo.git",
		GitIdentityID: "git-id-001",
		UserID:        "user-uuid-env",
		Provider:      "anthropic",
	}

	reply := p.Provision(context.Background(), req)
	if !reply.OK {
		t.Fatalf("provision failed: %s", reply.Error)
	}

	deploy, err := fakeK8s.AppsV1().Deployments(ns).Get(
		context.Background(), "project-proj-env", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Deployment: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]
	envMap := make(map[string]string)
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}

	// Required env vars per spec (ADR-0091, spec-user-agent.md).
	requiredEnv := map[string]string{
		"TOKEN_PROVIDER":    "anthropic",
		"PROJECT_ID":        "proj-env",
		"PROJECT_SLUG":      "env-project",
		"GIT_URL":           "https://github.com/example/env-repo.git",
		"HOST_SLUG":         "mycluster",
		"USER_SLUG":         "alice",
		"USER_ID":           "user-uuid-env",
		"GIT_IDENTITY_ID":   "git-id-001",
		"NATS_URL":          "nats://nats:4222",
		"CONTROL_PLANE_URL": "http://cp:8080",
	}

	for key, want := range requiredEnv {
		if got, ok := envMap[key]; !ok {
			t.Errorf("env var %s missing from Deployment", key)
		} else if got != want {
			t.Errorf("env var %s = %q, want %q", key, got, want)
		}
	}
}

// TestProvisioner_DeploymentVolumeMounts verifies all required volume mounts are present.
func TestProvisioner_DeploymentVolumeMounts(t *testing.T) {
	p, fakeK8s := newTestProvisioner(t, "alice", "mycluster", "anthropic", "sk-ant-test")
	ns := p.namespace

	req := ProvisionRequest{
		ProjectID:   "proj-vm",
		ProjectSlug: "vm-project",
		Provider:    "anthropic",
	}

	reply := p.Provision(context.Background(), req)
	if !reply.OK {
		t.Fatalf("provision failed: %s", reply.Error)
	}

	deploy, err := fakeK8s.AppsV1().Deployments(ns).Get(
		context.Background(), "project-proj-vm", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Deployment: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]
	mountPaths := make(map[string]bool)
	for _, vm := range container.VolumeMounts {
		mountPaths[vm.MountPath] = true
	}

	// Required mounts per spec (spec-user-agent.md Project Pod Lifecycle section).
	required := []string{
		"/data",
		"/nix",
		"/home/node/.claude-seed",
		"/home/node/.user-secrets",
		"/secrets/tokens/",
		"/home/node/.claude",
	}
	for _, path := range required {
		if !mountPaths[path] {
			t.Errorf("volume mount %q missing from Deployment", path)
		}
	}
}

// TestProvisioner_DeploymentManagedLabel verifies the mclaude/managed=true label.
func TestProvisioner_DeploymentManagedLabel(t *testing.T) {
	p, fakeK8s := newTestProvisioner(t, "alice", "mycluster", "anthropic", "sk-ant-test")
	ns := p.namespace

	req := ProvisionRequest{
		ProjectID:   "proj-label",
		ProjectSlug: "label-project",
		Provider:    "anthropic",
	}
	p.Provision(context.Background(), req) //nolint:errcheck

	deploy, err := fakeK8s.AppsV1().Deployments(ns).Get(
		context.Background(), "project-proj-label", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if deploy.Labels["mclaude/managed"] != "true" {
		t.Errorf("mclaude/managed label = %q, want true", deploy.Labels["mclaude/managed"])
	}
}

// TestProvisioner_Delete verifies that Delete removes the Deployment.
func TestProvisioner_Delete(t *testing.T) {
	cfg := makeProvisionerConfig("alice", "mycluster")
	ns := cfg.Namespace
	fakeK8s := fake.NewClientset()
	p := NewProvisioner(fakeK8s, ns, cfg, zerolog.Nop())

	// Create a deployment directly via fake client.
	deploy := p.buildDeployment(ProvisionRequest{
		ProjectID:   "proj-del",
		ProjectSlug: "del-project",
		Provider:    "anthropic",
	}, defaultSessionAgentTpl(), "anthropic")
	_, err := fakeK8s.AppsV1().Deployments(ns).Create(
		context.Background(), deploy, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("create deploy: %v", err)
	}

	// Delete it.
	p.Delete(context.Background(), DeleteRequest{
		ProjectID:   "proj-del",
		ProjectSlug: "del-project",
	})

	// Verify it's gone.
	_, getErr := fakeK8s.AppsV1().Deployments(ns).Get(
		context.Background(), "project-proj-del", metav1.GetOptions{},
	)
	if getErr == nil {
		t.Error("expected Deployment to be deleted, but it still exists")
	}
}

// TestProvisioner_Delete_NotFound verifies that deleting a non-existent Deployment
// is treated as success (idempotent).
func TestProvisioner_Delete_NotFound(t *testing.T) {
	cfg := makeProvisionerConfig("alice", "mycluster")
	p := NewProvisioner(fake.NewClientset(), cfg.Namespace, cfg, zerolog.Nop())

	// Should not panic or return error.
	p.Delete(context.Background(), DeleteRequest{
		ProjectID:   "nonexistent",
		ProjectSlug: "nonexistent",
	})
}

// TestProvisioner_ProvisionIdempotent verifies that provisioning the same project
// twice does not fail (idempotent update path).
func TestProvisioner_ProvisionIdempotent(t *testing.T) {
	p, _ := newTestProvisioner(t, "alice", "mycluster", "anthropic", "sk-ant-test")

	req := ProvisionRequest{
		ProjectID:   "proj-idem",
		ProjectSlug: "idem-project",
		Provider:    "anthropic",
	}

	reply1 := p.Provision(context.Background(), req)
	if !reply1.OK {
		t.Fatalf("first provision failed: %s", reply1.Error)
	}

	reply2 := p.Provision(context.Background(), req)
	if !reply2.OK {
		t.Fatalf("second provision (idempotent) failed: %s", reply2.Error)
	}
}

// TestProvisioner_DefaultTemplate verifies that provisioner works without a
// session-agent-template ConfigMap (uses defaults).
func TestProvisioner_DefaultTemplate(t *testing.T) {
	cfg := makeProvisionerConfig("alice", "mycluster")
	ns := cfg.Namespace
	fakeK8s := fake.NewClientset() // no ConfigMap

	tokenBaseDir := t.TempDir()
	tokensDir := filepath.Join(tokenBaseDir, "tokens")
	os.MkdirAll(tokensDir, 0755)                                              //nolint:errcheck
	os.WriteFile(filepath.Join(tokensDir, "anthropic"), []byte("tok"), 0600) //nolint:errcheck

	p := NewProvisionerWithTokenDir(fakeK8s, ns, cfg, tokenBaseDir, zerolog.Nop())

	req := ProvisionRequest{
		ProjectID:   "proj-deftpl",
		ProjectSlug: "deftpl-project",
		Provider:    "anthropic",
	}

	reply := p.Provision(context.Background(), req)
	if !reply.OK {
		t.Fatalf("provision with default template failed: %s", reply.Error)
	}

	deploy, err := fakeK8s.AppsV1().Deployments(ns).Get(
		context.Background(), "project-proj-deftpl", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	if container.Image == "" {
		t.Error("container image is empty with default template")
	}
}

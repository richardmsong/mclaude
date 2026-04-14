// reconciler_test.go tests the MCProjectReconciler using controller-runtime envtest.
// It verifies that the reconciler creates K8s resources (namespace, RBAC, secrets,
// PVCs, Deployment) in response to MCProject CRs, and repairs drift when resources
// are deleted.
//
// Requires KUBEBUILDER_ASSETS to be set. Skip-guarded if absent.
//
// Run with: KUBEBUILDER_ASSETS=... go test -run TestReconciler ./...
package main

import (
	"context"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// startTestEnv starts an envtest environment with the MCProject CRD registered.
// Skips the test if KUBEBUILDER_ASSETS is not set.
func startTestEnv(t *testing.T) (client.Client, context.CancelFunc) {
	t.Helper()

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run setup-envtest first")
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("add MCProject scheme: %v", err)
	}
	// RBAC types
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add rbacv1 scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add appsv1 scheme: %v", err)
	}

	env := &envtest.Environment{
		Scheme: scheme,
	}

	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest.Start: %v", err)
	}

	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Logf("envtest.Stop: %v", err)
		}
	})

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                     scheme,
		Metrics:                    metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:     "0",
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	accountKP, err := GenerateAccountNKey()
	if err != nil {
		t.Fatalf("GenerateAccountNKey: %v", err)
	}

	reconciler := &MCProjectReconciler{
		client:              mgr.GetClient(),
		scheme:              mgr.GetScheme(),
		controlPlaneNs:      "mclaude-system",
		releaseName:         "mclaude",
		sessionAgentNATSURL: "nats://nats.mclaude-system.svc.cluster.local:4222",
		accountKP:           accountKP.KeyPair,
		logger:              testLogger(t),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Logf("manager stopped: %v", err)
		}
	}()

	// Wait for cache to sync
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		cancel()
		t.Fatal("cache sync timeout")
	}

	// Create the control-plane namespace for template lookups
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-system"},
	}
	if err := mgr.GetClient().Create(ctx, ns); err != nil && !errors.IsAlreadyExists(err) {
		t.Logf("create mclaude-system ns: %v (non-fatal)", err)
	}

	return mgr.GetClient(), cancel
}

// waitForCondition polls until predicate returns true or timeout.
func waitForCondition(t *testing.T, timeout time.Duration, msg string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("timeout waiting for: %s", msg)
}

// TestReconciler_CreatesNamespaceAndRBAC verifies the reconciler creates the user
// namespace, ServiceAccount, Role, and RoleBinding when an MCProject CR is applied.
func TestReconciler_CreatesNamespaceAndRBAC(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-1"
	projectID := "reconciler-proj-1"
	userNs := "mclaude-" + userID

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for user namespace
	waitForCondition(t, 15*time.Second, "user namespace created", func() bool {
		ns := &corev1.Namespace{}
		err := c.Get(ctx, types.NamespacedName{Name: userNs}, ns)
		return err == nil
	})

	// Wait for ServiceAccount
	waitForCondition(t, 10*time.Second, "ServiceAccount created", func() bool {
		sa := &corev1.ServiceAccount{}
		err := c.Get(ctx, types.NamespacedName{Name: "mclaude-sa", Namespace: userNs}, sa)
		return err == nil
	})

	// Role
	waitForCondition(t, 10*time.Second, "Role created", func() bool {
		role := &rbacv1.Role{}
		err := c.Get(ctx, types.NamespacedName{Name: "mclaude-role", Namespace: userNs}, role)
		return err == nil
	})

	// RoleBinding
	waitForCondition(t, 10*time.Second, "RoleBinding created", func() bool {
		rb := &rbacv1.RoleBinding{}
		err := c.Get(ctx, types.NamespacedName{Name: "mclaude-role", Namespace: userNs}, rb)
		return err == nil
	})
}

// TestReconciler_CreatesSecretsAndConfigMap verifies the reconciler creates the
// user-secrets Secret (with nats-creds) and user-config ConfigMap.
func TestReconciler_CreatesSecretsAndConfigMap(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-2"
	projectID := "reconciler-proj-2"
	userNs := "mclaude-" + userID

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for user-secrets Secret with nats-creds populated
	waitForCondition(t, 15*time.Second, "user-secrets Secret with nats-creds", func() bool {
		secret := &corev1.Secret{}
		if err := c.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, secret); err != nil {
			return false
		}
		return len(secret.Data["nats-creds"]) > 0
	})

	// user-config ConfigMap
	waitForCondition(t, 10*time.Second, "user-config ConfigMap created", func() bool {
		cm := &corev1.ConfigMap{}
		err := c.Get(ctx, types.NamespacedName{Name: "user-config", Namespace: userNs}, cm)
		return err == nil
	})
}

// TestReconciler_CreatesPVCsAndDeployment verifies the reconciler creates the
// project PVC, nix PVC, and session-agent Deployment.
func TestReconciler_CreatesPVCsAndDeployment(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-3"
	projectID := "reconciler-proj-3"
	userNs := "mclaude-" + userID

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for project PVC
	waitForCondition(t, 20*time.Second, "project PVC created", func() bool {
		pvc := &corev1.PersistentVolumeClaim{}
		err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, pvc)
		return err == nil
	})

	// Nix PVC
	waitForCondition(t, 10*time.Second, "nix PVC created", func() bool {
		pvc := &corev1.PersistentVolumeClaim{}
		err := c.Get(ctx, types.NamespacedName{Name: "nix-" + projectID, Namespace: userNs}, pvc)
		return err == nil
	})

	// Deployment
	waitForCondition(t, 10*time.Second, "Deployment created", func() bool {
		deploy := &appsv1.Deployment{}
		err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy)
		return err == nil
	})
}

// TestReconciler_StatusPhaseReady verifies the MCProject status phase transitions
// to Ready once all resources are provisioned.
func TestReconciler_StatusPhaseReady(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-4"
	projectID := "reconciler-proj-4"

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for status phase = Ready
	waitForCondition(t, 30*time.Second, "MCProject phase = Ready", func() bool {
		updated := &MCProject{}
		if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, updated); err != nil {
			return false
		}
		return updated.Status.Phase == PhaseReady
	})

	// Verify userNamespace is set
	updated := &MCProject{}
	if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, updated); err != nil {
		t.Fatalf("get MCProject: %v", err)
	}
	expectedNs := "mclaude-" + userID
	if updated.Status.UserNamespace != expectedNs {
		t.Errorf("UserNamespace = %q; want %q", updated.Status.UserNamespace, expectedNs)
	}
	if updated.Status.LastReconciledAt == nil {
		t.Error("LastReconciledAt should be set when Ready")
	}
}

// TestReconciler_RepairsDriftedDeployment verifies that if the Deployment is
// deleted after reconciliation, the reconciler recreates it.
func TestReconciler_RepairsDriftedDeployment(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-5"
	projectID := "reconciler-proj-5"
	userNs := "mclaude-" + userID

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for initial reconciliation to complete
	waitForCondition(t, 30*time.Second, "initial Deployment created", func() bool {
		deploy := &appsv1.Deployment{}
		err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy)
		return err == nil
	})

	// Delete the Deployment to simulate drift
	deploy := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy); err != nil {
		t.Fatalf("get Deployment before delete: %v", err)
	}
	// Remove finalizers so deletion proceeds immediately
	deploy.Finalizers = nil
	if err := c.Update(ctx, deploy); err != nil {
		t.Logf("remove finalizers: %v (non-fatal)", err)
	}
	if err := c.Delete(ctx, deploy); err != nil {
		t.Fatalf("delete Deployment: %v", err)
	}

	// Reconciler should detect the drift (via Owns watch) and recreate
	waitForCondition(t, 30*time.Second, "Deployment recreated after drift", func() bool {
		d := &appsv1.Deployment{}
		err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, d)
		return err == nil
	})
}

// TestReconciler_CreateMCProject verifies the CreateMCProject helper creates the CR.
func TestReconciler_CreateMCProject(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-6"
	projectID := "reconciler-proj-6"

	if err := CreateMCProject(ctx, c, "mclaude-system", userID, projectID, ""); err != nil {
		t.Fatalf("CreateMCProject: %v", err)
	}

	// Idempotent: calling again should not error
	if err := CreateMCProject(ctx, c, "mclaude-system", userID, projectID, ""); err != nil {
		t.Fatalf("CreateMCProject idempotent call: %v", err)
	}

	// Verify CR exists
	mcp := &MCProject{}
	if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, mcp); err != nil {
		t.Fatalf("get MCProject: %v", err)
	}
	if mcp.Spec.UserID != userID {
		t.Errorf("UserID = %q; want %q", mcp.Spec.UserID, userID)
	}
	if mcp.Spec.ProjectID != projectID {
		t.Errorf("ProjectID = %q; want %q", mcp.Spec.ProjectID, projectID)
	}
}

// TestReconciler_GitURLPropagated verifies that gitUrl from the MCProject spec
// is passed to the Deployment as GIT_URL env var.
func TestReconciler_GitURLPropagated(t *testing.T) {
	c, cancel := startTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "reconciler-test-user-7"
	projectID := "reconciler-proj-7"
	gitURL := "git@github.com:org/repo.git"
	userNs := "mclaude-" + userID

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
			GitURL:    gitURL,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for Deployment
	waitForCondition(t, 20*time.Second, "Deployment created with git URL", func() bool {
		deploy := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy); err != nil {
			return false
		}
		if len(deploy.Spec.Template.Spec.Containers) == 0 {
			return false
		}
		for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "GIT_URL" && env.Value == gitURL {
				return true
			}
		}
		return false
	})
}

// TestReconciler_DevOAuthTokenInjected verifies that when devOAuthToken is set on the
// reconciler, the oauth-token key is included in the user-secrets Secret.
func TestReconciler_DevOAuthTokenInjected(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run setup-envtest first")
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("add MCProject scheme: %v", err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add rbacv1 scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add appsv1 scheme: %v", err)
	}

	env := &envtest.Environment{Scheme: scheme}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest.Start: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	accountKP, err := GenerateAccountNKey()
	if err != nil {
		t.Fatalf("GenerateAccountNKey: %v", err)
	}

	const testOAuthToken = "test-oauth-token-value"
	reconciler := &MCProjectReconciler{
		client:              mgr.GetClient(),
		scheme:              mgr.GetScheme(),
		controlPlaneNs:      "mclaude-system",
		releaseName:         "mclaude",
		sessionAgentNATSURL: "nats://nats.mclaude-system.svc.cluster.local:4222",
		accountKP:           accountKP.KeyPair,
		devOAuthToken:       testOAuthToken,
		logger:              testLogger(t),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Logf("manager stopped: %v", err)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache sync timeout")
	}

	c := mgr.GetClient()
	// Create control-plane namespace
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "mclaude-system"}}
	_ = c.Create(ctx, ns)

	userID := "oauth-test-user"
	projectID := "oauth-test-proj"
	userNs := "mclaude-" + userID

	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: "mclaude-system",
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
		},
	}
	if err := c.Create(ctx, mcp); err != nil {
		t.Fatalf("create MCProject: %v", err)
	}

	// Wait for user-secrets with both nats-creds and oauth-token
	waitForCondition(t, 15*time.Second, "user-secrets with oauth-token", func() bool {
		secret := &corev1.Secret{}
		if err := c.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, secret); err != nil {
			return false
		}
		return len(secret.Data["nats-creds"]) > 0 && string(secret.Data["oauth-token"]) == testOAuthToken
	})

	// Final assertion
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, secret); err != nil {
		t.Fatalf("get user-secrets: %v", err)
	}
	if got := string(secret.Data["oauth-token"]); got != testOAuthToken {
		t.Errorf("oauth-token = %q; want %q", got, testOAuthToken)
	}
}

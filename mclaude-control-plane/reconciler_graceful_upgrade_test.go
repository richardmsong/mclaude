// reconciler_graceful_upgrade_test.go tests spec changes from docs/plan-graceful-upgrades.md:
// 1. reconcileDeployment sets Recreate strategy on create path.
// 2. reconcileDeployment sets Recreate strategy (and updates image) on update path.
// 3. SetupWithManager ConfigMap watch re-enqueues all MCProject CRs when the
//    session-agent-template ConfigMap changes.
//
// Requires KUBEBUILDER_ASSETS to be set. Skip-guarded if absent.
package main

import (
	"context"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/config"
)

// startGracefulUpgradeTestEnv starts envtest and returns a client and cancel func.
// Extracted to allow multiple tests to share setup logic cleanly.
func startGracefulUpgradeTestEnv(t *testing.T) (client.Client, context.CancelFunc) {
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
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add rbacv1 scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add appsv1 scheme: %v", err)
	}

	env := &envtest.Environment{
		Scheme:            scheme,
		CRDDirectoryPaths: []string{"testdata"},
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

	skipNameVal := true
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                     scheme,
		Metrics:                    metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:     "0",
		Controller:                 config.Controller{SkipNameValidation: &skipNameVal},
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

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		cancel()
		t.Fatal("cache sync timeout")
	}

	// Create control-plane namespace for template lookups
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-system"},
	}
	if err := mgr.GetClient().Create(ctx, ns); err != nil {
		t.Logf("create mclaude-system ns: %v (non-fatal)", err)
	}

	return mgr.GetClient(), cancel
}

// TestReconciler_CreateDeploymentWithRecreateStrategy verifies that reconcileDeployment
// creates a new Deployment with the Recreate strategy (create path).
// Per docs/plan-graceful-upgrades.md: prevents two pods sharing a JetStream consumer.
func TestReconciler_CreateDeploymentWithRecreateStrategy(t *testing.T) {
	c, cancel := startGracefulUpgradeTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "recreate-create-user"
	projectID := "recreate-create-proj"
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

	// Wait for Deployment to be created
	var deploy appsv1.Deployment
	waitForCondition(t, 20*time.Second, "Deployment created", func() bool {
		err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, &deploy)
		return err == nil
	})

	if deploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("Deployment strategy = %q; want %q (Recreate)",
			deploy.Spec.Strategy.Type, appsv1.RecreateDeploymentStrategyType)
	}
}

// TestReconciler_UpdateDeploymentToRecreateStrategy verifies that reconcileDeployment
// migrates an existing RollingUpdate Deployment to Recreate on the next reconcile.
// Per docs/plan-graceful-upgrades.md: "ensures existing Deployments (which defaulted
// to RollingUpdate) are migrated to Recreate on the next reconcile."
func TestReconciler_UpdateDeploymentToRecreateStrategy(t *testing.T) {
	c, cancel := startGracefulUpgradeTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "recreate-update-user"
	projectID := "recreate-update-proj"
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

	// Wait for initial reconcile to create Deployment
	waitForCondition(t, 20*time.Second, "initial Deployment created", func() bool {
		deploy := &appsv1.Deployment{}
		return c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy) == nil
	})

	// Patch the Deployment strategy back to RollingUpdate to simulate a pre-upgrade state.
	existingDeploy := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, existingDeploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	existingDeploy.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
	}
	if err := c.Update(ctx, existingDeploy); err != nil {
		t.Fatalf("patch deployment to RollingUpdate: %v", err)
	}

	// Verify it was set to RollingUpdate
	checkDeploy := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, checkDeploy); err != nil {
		t.Fatalf("get deployment after patch: %v", err)
	}
	if checkDeploy.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Skip("could not set RollingUpdate strategy (envtest behavior) — skipping migration test")
	}

	// Trigger reconciliation by touching the MCProject
	updatedMCP := &MCProject{}
	if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, updatedMCP); err != nil {
		t.Fatalf("get MCProject: %v", err)
	}
	if updatedMCP.Annotations == nil {
		updatedMCP.Annotations = make(map[string]string)
	}
	updatedMCP.Annotations["test/trigger"] = "recreate-migration"
	if err := c.Update(ctx, updatedMCP); err != nil {
		t.Fatalf("touch MCProject: %v", err)
	}

	// Reconciler should update the Deployment to Recreate strategy
	waitForCondition(t, 15*time.Second, "Deployment migrated to Recreate", func() bool {
		deploy := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy); err != nil {
			return false
		}
		return deploy.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType
	})

	// Final assertion
	finalDeploy := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, finalDeploy); err != nil {
		t.Fatalf("get final deployment: %v", err)
	}
	if finalDeploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("final strategy = %q; want %q (Recreate)",
			finalDeploy.Spec.Strategy.Type, appsv1.RecreateDeploymentStrategyType)
	}
}

// TestReconciler_ConfigMapWatchReenqueuesMCProjects verifies that updating the
// session-agent-template ConfigMap triggers reconciliation of all MCProject CRs.
// Per docs/plan-graceful-upgrades.md: ConfigMap change re-enqueues all MCProjects
// so reconcileDeployment compares the new template image against each Deployment.
func TestReconciler_ConfigMapWatchReenqueuesMCProjects(t *testing.T) {
	c, cancel := startGracefulUpgradeTestEnv(t)
	defer cancel()

	ctx := context.Background()

	// Create two MCProject CRs with different users/projects
	for i, userID := range []string{"cmwatch-user-a", "cmwatch-user-b"} {
		projectID := "cmwatch-proj-" + string(rune('a'+i))
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
			t.Fatalf("create MCProject %s: %v", projectID, err)
		}
	}

	// Wait for both MCProjects to reach Ready phase (initial reconcile complete).
	for i, userID := range []string{"cmwatch-user-a", "cmwatch-user-b"} {
		projectID := "cmwatch-proj-" + string(rune('a'+i))
		_ = userID
		waitForCondition(t, 30*time.Second, "MCProject "+projectID+" Ready", func() bool {
			mcp := &MCProject{}
			if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, mcp); err != nil {
				return false
			}
			return mcp.Status.Phase == PhaseReady
		})
	}

	// Create the session-agent-template ConfigMap with an initial image.
	templateNs := "mclaude-system"
	templateName := "mclaude-session-agent-template"
	templCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateName,
			Namespace: templateNs,
		},
		Data: map[string]string{
			"image": "mclaude-session-agent:v1",
		},
	}
	if err := c.Create(ctx, templCM); err != nil {
		t.Fatalf("create template ConfigMap: %v", err)
	}

	// Record the LastReconciledAt timestamps before the ConfigMap update.
	preUpdateTimestamps := make(map[string]*metav1.Time)
	for i := range []string{"cmwatch-user-a", "cmwatch-user-b"} {
		projectID := "cmwatch-proj-" + string(rune('a'+i))
		mcp := &MCProject{}
		if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, mcp); err != nil {
			t.Fatalf("get MCProject %s before update: %v", projectID, err)
		}
		preUpdateTimestamps[projectID] = mcp.Status.LastReconciledAt
	}

	// Update the ConfigMap with a new image — this should trigger reconciliation.
	if err := c.Get(ctx, types.NamespacedName{Name: templateName, Namespace: templateNs}, templCM); err != nil {
		t.Fatalf("get template ConfigMap: %v", err)
	}
	templCM.Data["image"] = "mclaude-session-agent:v2"
	if err := c.Update(ctx, templCM); err != nil {
		t.Fatalf("update template ConfigMap: %v", err)
	}

	// Both MCProjects should be re-reconciled (LastReconciledAt advances or Deployment image updates).
	// We check that each MCProject's Deployment gets the new image within the timeout.
	for i, userID := range []string{"cmwatch-user-a", "cmwatch-user-b"} {
		projectID := "cmwatch-proj-" + string(rune('a'+i))
		userNs := "mclaude-" + userID
		waitForCondition(t, 30*time.Second, "Deployment image updated for "+projectID, func() bool {
			deploy := &appsv1.Deployment{}
			if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy); err != nil {
				return false
			}
			if len(deploy.Spec.Template.Spec.Containers) == 0 {
				return false
			}
			// Image updated to v2 AND strategy is Recreate
			return deploy.Spec.Template.Spec.Containers[0].Image == "mclaude-session-agent:v2" &&
				deploy.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType
		})
	}
}

// TestReconciler_UpdateDeploymentEnvVarsOnGitIdentityChange verifies that when an
// existing Deployment has no GIT_IDENTITY_ID env var and the MCProject spec is updated
// with a gitIdentityId, the reconciler updates the Deployment's env vars.
// Per docs/plan-reconciler-env-sync.md: the update path rebuilds the full container
// spec on every reconcile, so env var drift is corrected without manual restart.
func TestReconciler_UpdateDeploymentEnvVarsOnGitIdentityChange(t *testing.T) {
	c, cancel := startGracefulUpgradeTestEnv(t)
	defer cancel()

	ctx := context.Background()
	userID := "envupdate-user"
	projectID := "envupdate-proj"
	userNs := "mclaude-" + userID
	const gitIdentityID = "oauth-conn-abc123"

	// Create MCProject without a gitIdentityId so the initial Deployment
	// does not include GIT_IDENTITY_ID.
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

	// Wait for the initial Deployment to appear.
	waitForCondition(t, 20*time.Second, "initial Deployment created", func() bool {
		deploy := &appsv1.Deployment{}
		return c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy) == nil
	})

	// Confirm GIT_IDENTITY_ID is absent from the initial Deployment.
	initialDeploy := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, initialDeploy); err != nil {
		t.Fatalf("get initial Deployment: %v", err)
	}
	for _, env := range initialDeploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "GIT_IDENTITY_ID" {
			t.Fatalf("GIT_IDENTITY_ID present in initial Deployment before gitIdentityId was set (value=%q)", env.Value)
		}
	}

	// Now patch the MCProject spec with a gitIdentityId.
	updatedMCP := &MCProject{}
	if err := c.Get(ctx, types.NamespacedName{Name: projectID, Namespace: "mclaude-system"}, updatedMCP); err != nil {
		t.Fatalf("get MCProject for update: %v", err)
	}
	updatedMCP.Spec.GitIdentityID = gitIdentityID
	if err := c.Update(ctx, updatedMCP); err != nil {
		t.Fatalf("update MCProject with gitIdentityId: %v", err)
	}

	// Wait for the reconciler to propagate GIT_IDENTITY_ID into the Deployment.
	waitForCondition(t, 20*time.Second, "GIT_IDENTITY_ID env var updated in Deployment", func() bool {
		deploy := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, deploy); err != nil {
			return false
		}
		if len(deploy.Spec.Template.Spec.Containers) == 0 {
			return false
		}
		for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "GIT_IDENTITY_ID" && env.Value == gitIdentityID {
				return true
			}
		}
		return false
	})

	// Final assertion: verify the env var value is exactly what we set.
	finalDeploy := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: "project-" + projectID, Namespace: userNs}, finalDeploy); err != nil {
		t.Fatalf("get final Deployment: %v", err)
	}
	if len(finalDeploy.Spec.Template.Spec.Containers) == 0 {
		t.Fatal("Deployment has no containers")
	}
	var found string
	for _, env := range finalDeploy.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "GIT_IDENTITY_ID" {
			found = env.Value
			break
		}
	}
	if found != gitIdentityID {
		t.Errorf("GIT_IDENTITY_ID = %q; want %q", found, gitIdentityID)
	}
	// Also verify always-present env vars are still there.
	envMap := make(map[string]string)
	for _, env := range finalDeploy.Spec.Template.Spec.Containers[0].Env {
		envMap[env.Name] = env.Value
	}
	if envMap["USER_ID"] != userID {
		t.Errorf("USER_ID = %q; want %q", envMap["USER_ID"], userID)
	}
	if envMap["PROJECT_ID"] != projectID {
		t.Errorf("PROJECT_ID = %q; want %q", envMap["PROJECT_ID"], projectID)
	}
}

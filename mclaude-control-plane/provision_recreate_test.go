// provision_recreate_test.go tests that ensureDeployment sets the Recreate strategy
// on both create and update paths, as required by docs/plan-graceful-upgrades.md.
package main

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestEnsureDeployment_RecreateStrategyOnCreate verifies that ensureDeployment sets
// the Recreate deployment strategy when creating a new Deployment.
// Per docs/plan-graceful-upgrades.md: Recreate prevents two pods consuming from the
// same JetStream consumer simultaneously during upgrades.
func TestEnsureDeployment_RecreateStrategyOnCreate(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-recreate-create"
	projectID := "proj-recreate-create"

	tpl := &sessionAgentTpl{
		image:                         "mclaude-session-agent:v1",
		imagePullPolicy:               corev1.PullIfNotPresent,
		terminationGracePeriodSeconds: 30,
	}
	applyDefaultResources(tpl)

	if err := p.ensureDeployment(ctx, ns, projectID, "user1", "", tpl); err != nil {
		t.Fatalf("ensureDeployment: %v", err)
	}

	deploy, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-"+projectID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	if deploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy type = %q; want %q",
			deploy.Spec.Strategy.Type, appsv1.RecreateDeploymentStrategyType)
	}
}

// TestEnsureDeployment_RecreateStrategyOnUpdate verifies that ensureDeployment sets
// the Recreate strategy on the update path (existing Deployment with old image).
// This migrates existing Deployments that defaulted to RollingUpdate.
func TestEnsureDeployment_RecreateStrategyOnUpdate(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-recreate-update"
	projectID := "proj-recreate-update"

	// Pre-create a Deployment with the old RollingUpdate strategy (the default).
	replicas := int32(1)
	grace := int64(30)
	runAsUser := int64(1000)
	fsGroup := int64(1000)
	runAsNonRoot := true
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "project-" + projectID, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "mclaude-project"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mclaude-project"}},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &grace,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
						RunAsUser:    &runAsUser,
						FSGroup:      &fsGroup,
					},
					Containers: []corev1.Container{
						{Name: "session-agent", Image: "mclaude-session-agent:v1"},
					},
				},
			},
		},
	}
	if _, err := p.client.AppsV1().Deployments(ns).Create(ctx, existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("pre-create deployment: %v", err)
	}

	tpl := &sessionAgentTpl{
		image:                         "mclaude-session-agent:v2",
		imagePullPolicy:               corev1.PullIfNotPresent,
		terminationGracePeriodSeconds: 30,
	}
	applyDefaultResources(tpl)

	if err := p.ensureDeployment(ctx, ns, projectID, "user1", "", tpl); err != nil {
		t.Fatalf("ensureDeployment update: %v", err)
	}

	updated, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-"+projectID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated deployment: %v", err)
	}

	// Strategy must be Recreate after update.
	if updated.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy type = %q; want %q",
			updated.Spec.Strategy.Type, appsv1.RecreateDeploymentStrategyType)
	}

	// Image must also be updated.
	if got := updated.Spec.Template.Spec.Containers[0].Image; got != "mclaude-session-agent:v2" {
		t.Errorf("image = %q; want %q", got, "mclaude-session-agent:v2")
	}
}

// TestEnsureDeployment_Idempotent verifies that calling ensureDeployment twice
// for the same Deployment does not error (second call is the update path).
func TestEnsureDeployment_Idempotent(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-idempotent"
	projectID := "proj-idempotent"

	tpl := &sessionAgentTpl{
		image:                         "mclaude-session-agent:v1",
		imagePullPolicy:               corev1.PullIfNotPresent,
		terminationGracePeriodSeconds: 30,
	}
	applyDefaultResources(tpl)

	if err := p.ensureDeployment(ctx, ns, projectID, "user1", "", tpl); err != nil {
		t.Fatalf("first ensureDeployment: %v", err)
	}
	if err := p.ensureDeployment(ctx, ns, projectID, "user1", "", tpl); err != nil {
		t.Fatalf("second ensureDeployment: %v", err)
	}

	deploy, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-"+projectID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy type = %q; want %q after idempotent update",
			deploy.Spec.Strategy.Type, appsv1.RecreateDeploymentStrategyType)
	}
}

// newTestProvisionerWithFakeClient returns a K8sProvisioner with a pre-populated
// fake client that has the named ConfigMap in the control-plane namespace.
func newTestProvisionerWithFakeClient(t *testing.T) *K8sProvisioner {
	t.Helper()
	nkp, err := GenerateAccountNKey()
	if err != nil {
		t.Fatalf("GenerateAccountNKey: %v", err)
	}

	// The fake client; ConfigMap is added so loadTemplate works in integration.
	client := fake.NewClientset()
	return &K8sProvisioner{
		client:              client,
		controlPlaneNs:      "mclaude-system",
		releaseName:         "mclaude",
		sessionAgentNATSURL: "nats://nats:4222",
		accountKP:           nkp.KeyPair,
	}
}

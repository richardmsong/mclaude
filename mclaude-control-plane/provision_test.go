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

// --- Corporate CA bundle tests ---

// makeTemplateConfigMap creates the session-agent-template ConfigMap in the
// given provisioner's control-plane namespace. Used by CA bundle tests.
func makeTemplateConfigMap(t *testing.T, p *K8sProvisioner, corporateCAConfigMap string) {
	t.Helper()
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.releaseName + "-session-agent-template",
			Namespace: p.controlPlaneNs,
		},
		Data: map[string]string{
			"image":                         "ghcr.io/mclaude-project/mclaude-session-agent:test",
			"imagePullPolicy":               "IfNotPresent",
			"terminationGracePeriodSeconds": "30",
			"projectPvcSize":                "10Gi",
			"projectPvcStorageClass":        "",
			"nixPvcSize":                    "10Gi",
			"nixPvcStorageClass":            "",
			"corporateCAConfigMap":          corporateCAConfigMap,
		},
	}
	if _, err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create template configmap: %v", err)
	}
}

// makeCorporateCAConfigMap creates the source CA bundle ConfigMap in the control-plane
// namespace, as an operator would do before enabling the feature.
func makeCorporateCAConfigMap(t *testing.T, p *K8sProvisioner, cmName string) {
	t.Helper()
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: p.controlPlaneNs,
		},
		Data: map[string]string{
			"ca-certificates.crt": "-----BEGIN CERTIFICATE-----\nFAKECA\n-----END CERTIFICATE-----\n",
		},
	}
	if _, err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create corporate CA configmap: %v", err)
	}
}

// TestDeployment_WithCorporateCA verifies that when corporateCAConfigMap is set,
// ensureDeployment copies the ConfigMap to the user namespace and the resulting
// Deployment has the corporate-ca volume, the volumeMount at the expected path,
// and NODE_EXTRA_CA_CERTS env var.
func TestDeployment_WithCorporateCA(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-corp"
	cmName := "corporate-ca-bundle"

	// Set up source CA ConfigMap in control-plane namespace.
	makeCorporateCAConfigMap(t, p, cmName)
	makeTemplateConfigMap(t, p, cmName)

	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate: %v", err)
	}
	if tpl.corporateCAConfigMap != cmName {
		t.Fatalf("expected corporateCAConfigMap=%q, got %q", cmName, tpl.corporateCAConfigMap)
	}

	// Create the user namespace so Deployment create has somewhere to land.
	if _, err := p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := p.ensureDeployment(ctx, ns, "proj-corp", "corp-user", "", tpl); err != nil {
		t.Fatalf("ensureDeployment: %v", err)
	}

	// Verify the CA ConfigMap was copied to the user namespace.
	copiedCM, err := p.client.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("CA ConfigMap not found in user namespace: %v", err)
	}
	if copiedCM.Data["ca-certificates.crt"] == "" {
		t.Error("copied CA ConfigMap missing ca-certificates.crt key")
	}

	// Verify the Deployment has the corporate-ca volume.
	deploy, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-proj-corp", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	var foundVolume bool
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "corporate-ca" {
			foundVolume = true
			if v.ConfigMap == nil || v.ConfigMap.Name != cmName {
				t.Errorf("corporate-ca volume references wrong ConfigMap: %v", v.ConfigMap)
			}
			break
		}
	}
	if !foundVolume {
		t.Error("Deployment missing corporate-ca volume")
	}

	// Verify the session-agent container has the volumeMount.
	containers := deploy.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("no containers in Deployment")
	}
	container := containers[0]

	var foundMount bool
	for _, vm := range container.VolumeMounts {
		if vm.Name == "corporate-ca" {
			foundMount = true
			if vm.MountPath != "/etc/ssl/certs/corporate-ca-certificates.crt" {
				t.Errorf("wrong mountPath: %q", vm.MountPath)
			}
			if vm.SubPath != "ca-certificates.crt" {
				t.Errorf("wrong subPath: %q", vm.SubPath)
			}
			if !vm.ReadOnly {
				t.Error("corporate-ca volumeMount should be readOnly")
			}
			break
		}
	}
	if !foundMount {
		t.Error("session-agent container missing corporate-ca volumeMount")
	}

	// Verify NODE_EXTRA_CA_CERTS env var is set.
	var foundEnv bool
	for _, e := range container.Env {
		if e.Name == "NODE_EXTRA_CA_CERTS" {
			foundEnv = true
			if e.Value != "/etc/ssl/certs/corporate-ca-certificates.crt" {
				t.Errorf("wrong NODE_EXTRA_CA_CERTS value: %q", e.Value)
			}
			break
		}
	}
	if !foundEnv {
		t.Error("session-agent container missing NODE_EXTRA_CA_CERTS env var")
	}
}

// TestDeployment_WithoutCorporateCA verifies that when corporateCAConfigMap is empty,
// no corporate-ca volume, volumeMount, or NODE_EXTRA_CA_CERTS env var is added.
func TestDeployment_WithoutCorporateCA(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-nocorp"

	// Template with empty corporateCAConfigMap.
	makeTemplateConfigMap(t, p, "")

	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate: %v", err)
	}
	if tpl.corporateCAConfigMap != "" {
		t.Fatalf("expected empty corporateCAConfigMap, got %q", tpl.corporateCAConfigMap)
	}

	if _, err := p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := p.ensureDeployment(ctx, ns, "proj-nocorp", "nocorp-user", "", tpl); err != nil {
		t.Fatalf("ensureDeployment: %v", err)
	}

	deploy, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-proj-nocorp", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	// No corporate-ca volume.
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "corporate-ca" {
			t.Error("unexpected corporate-ca volume in Deployment")
		}
	}

	if len(deploy.Spec.Template.Spec.Containers) == 0 {
		t.Fatal("no containers in Deployment")
	}
	container := deploy.Spec.Template.Spec.Containers[0]

	// No corporate-ca volumeMount.
	for _, vm := range container.VolumeMounts {
		if vm.Name == "corporate-ca" {
			t.Error("unexpected corporate-ca volumeMount in container")
		}
	}

	// No NODE_EXTRA_CA_CERTS.
	for _, e := range container.Env {
		if e.Name == "NODE_EXTRA_CA_CERTS" {
			t.Error("unexpected NODE_EXTRA_CA_CERTS env var in container")
		}
	}
}

// TestDeployment_UpdatePath_CAAdded verifies that the update path also injects
// the CA volume/mount/env when the template has a corporateCAConfigMap set.
// (Regression guard: old code only patched image on update.)
func TestDeployment_UpdatePath_CAAdded(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-caupdate"
	cmName := "corp-ca"

	// First: create Deployment without CA.
	makeTemplateConfigMap(t, p, "")
	tplNoCA, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate (no CA): %v", err)
	}

	if _, err := p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	if err := p.ensureDeployment(ctx, ns, "proj-caupdate", "caupdate-user", "", tplNoCA); err != nil {
		t.Fatalf("ensureDeployment (no CA): %v", err)
	}

	// Now add the CA ConfigMap and update the template.
	makeCorporateCAConfigMap(t, p, cmName)
	// Delete old template and recreate with CA set.
	if err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Delete(ctx, p.releaseName+"-session-agent-template", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete template cm: %v", err)
	}
	makeTemplateConfigMap(t, p, cmName)

	tplWithCA, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate (with CA): %v", err)
	}

	// Call ensureDeployment again — should hit the update path.
	if err := p.ensureDeployment(ctx, ns, "proj-caupdate", "caupdate-user", "", tplWithCA); err != nil {
		t.Fatalf("ensureDeployment (with CA, update path): %v", err)
	}

	deploy, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-proj-caupdate", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment after update: %v", err)
	}

	var foundVolume bool
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "corporate-ca" {
			foundVolume = true
			break
		}
	}
	if !foundVolume {
		t.Error("Deployment missing corporate-ca volume after update")
	}

	if len(deploy.Spec.Template.Spec.Containers) == 0 {
		t.Fatal("no containers in updated Deployment")
	}
	container := deploy.Spec.Template.Spec.Containers[0]

	var foundMount bool
	for _, vm := range container.VolumeMounts {
		if vm.Name == "corporate-ca" {
			foundMount = true
			break
		}
	}
	if !foundMount {
		t.Error("session-agent container missing corporate-ca volumeMount after update")
	}

	var foundEnv bool
	for _, e := range container.Env {
		if e.Name == "NODE_EXTRA_CA_CERTS" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Error("session-agent container missing NODE_EXTRA_CA_CERTS after update")
	}
}

// TestEnsureCorporateCAConfigMap_Idempotent verifies create-or-update semantics:
// calling ensureCorporateCAConfigMap twice succeeds, and the second call updates
// the data if the source changed.
func TestEnsureCorporateCAConfigMap_Idempotent(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-ca-idempotent"
	cmName := "my-corporate-ca"

	// Create source CM.
	src := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: p.controlPlaneNs},
		Data:       map[string]string{"ca-certificates.crt": "PEM_V1"},
	}
	if _, err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Create(ctx, src, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create source CA CM: %v", err)
	}

	if _, err := p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// First call — creates the copy.
	if err := p.ensureCorporateCAConfigMap(ctx, ns, cmName); err != nil {
		t.Fatalf("first ensureCorporateCAConfigMap: %v", err)
	}
	cm1, err := p.client.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get copied CM after first call: %v", err)
	}
	if cm1.Data["ca-certificates.crt"] != "PEM_V1" {
		t.Errorf("expected PEM_V1, got %q", cm1.Data["ca-certificates.crt"])
	}

	// Update the source.
	src.Data["ca-certificates.crt"] = "PEM_V2"
	if _, err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Update(ctx, src, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update source CA CM: %v", err)
	}

	// Second call — updates the copy.
	if err := p.ensureCorporateCAConfigMap(ctx, ns, cmName); err != nil {
		t.Fatalf("second ensureCorporateCAConfigMap: %v", err)
	}
	cm2, err := p.client.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get copied CM after second call: %v", err)
	}
	if cm2.Data["ca-certificates.crt"] != "PEM_V2" {
		t.Errorf("expected PEM_V2 after update, got %q", cm2.Data["ca-certificates.crt"])
	}
}

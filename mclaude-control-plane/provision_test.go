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
// caEnabled controls whether corporateCAEnabled is "true".
// caConfigMapName is the ConfigMap name trust-manager syncs into user namespaces.
func makeTemplateConfigMap(t *testing.T, p *K8sProvisioner, caEnabled bool, caConfigMapName string) {
	t.Helper()
	ctx := context.Background()
	enabledStr := "false"
	if caEnabled {
		enabledStr = "true"
	}
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
			"corporateCAEnabled":            enabledStr,
			"corporateCAConfigMapName":      caConfigMapName,
			"corporateCAConfigMapKey":       "ca-certificates.crt",
		},
	}
	if _, err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create template configmap: %v", err)
	}
}

// makeCorporateCAConfigMap creates the source CA bundle ConfigMap in the control-plane
// namespace, as an operator would do before enabling the feature.
// NOTE: with trust-manager, the source is in control-plane namespace but the reconciler
// no longer copies it — trust-manager handles cross-namespace sync.
// This helper is retained for reference but tests should use makeCorporateCAConfigMapInNs.
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

// makeCorporateCAConfigMapInNs creates a CA bundle ConfigMap directly in the given namespace,
// simulating what trust-manager would do when it syncs the bundle into a user namespace.
func makeCorporateCAConfigMapInNs(t *testing.T, p *K8sProvisioner, ns, cmName string) {
	t.Helper()
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: ns,
		},
		Data: map[string]string{
			"ca-certificates.crt": "-----BEGIN CERTIFICATE-----\nFAKECA\n-----END CERTIFICATE-----\n",
		},
	}
	if _, err := p.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create corporate CA configmap in %s: %v", ns, err)
	}
}

// TestDeployment_WithCorporateCA verifies that when corporateCAEnabled is true and
// the CA ConfigMap exists in the user namespace (synced by trust-manager), the resulting
// Deployment has the corporate-ca volume, the volumeMount at the expected path,
// NODE_EXTRA_CA_CERTS env var, and the mclaude.io/ca-bundle-hash annotation.
// No cross-namespace copy is performed — trust-manager handles that.
func TestDeployment_WithCorporateCA(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-corp"
	cmName := "corporate-ca-bundle"

	// Create the user namespace.
	if _, err := p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Simulate trust-manager syncing the CA ConfigMap into the user namespace.
	makeCorporateCAConfigMapInNs(t, p, ns, cmName)

	makeTemplateConfigMap(t, p, true, cmName)

	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate: %v", err)
	}
	if !tpl.corporateCAEnabled {
		t.Fatal("expected corporateCAEnabled=true")
	}
	if tpl.corporateCAConfigMapName != cmName {
		t.Fatalf("expected corporateCAConfigMapName=%q, got %q", cmName, tpl.corporateCAConfigMapName)
	}

	if err := p.ensureDeployment(ctx, ns, "proj-corp", "corp-user", "", tpl); err != nil {
		t.Fatalf("ensureDeployment: %v", err)
	}

	// Verify NO cross-namespace copy was made from control-plane namespace — trust-manager owns that.
	// The CA ConfigMap in the user namespace is the one we pre-created (simulating trust-manager).

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

	// Verify the ca-bundle-hash annotation is present on the pod template.
	hashAnnotation := deploy.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]
	if hashAnnotation == "" {
		t.Error("Deployment pod template missing mclaude.io/ca-bundle-hash annotation")
	}
}

// TestDeployment_WithoutCorporateCA verifies that when corporateCAEnabled is false,
// no corporate-ca volume, volumeMount, NODE_EXTRA_CA_CERTS env var, or
// ca-bundle-hash annotation is added.
func TestDeployment_WithoutCorporateCA(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-nocorp"

	// Template with corporateCAEnabled=false.
	makeTemplateConfigMap(t, p, false, "")

	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate: %v", err)
	}
	if tpl.corporateCAEnabled {
		t.Fatal("expected corporateCAEnabled=false")
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

	// No ca-bundle-hash annotation.
	if hash := deploy.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]; hash != "" {
		t.Errorf("unexpected ca-bundle-hash annotation: %q", hash)
	}
}

// TestDeployment_UpdatePath_CAAdded verifies that the update path also injects
// the CA volume/mount/env and hash annotation when the template has corporateCAEnabled=true.
// trust-manager syncs the ConfigMap into the user namespace; no cross-namespace copy needed.
func TestDeployment_UpdatePath_CAAdded(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-caupdate"
	cmName := "corp-ca"

	// First: create Deployment without CA.
	makeTemplateConfigMap(t, p, false, "")
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

	// Simulate trust-manager syncing the CA ConfigMap into the user namespace.
	makeCorporateCAConfigMapInNs(t, p, ns, cmName)

	// Delete old template and recreate with CA enabled.
	if err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Delete(ctx, p.releaseName+"-session-agent-template", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete template cm: %v", err)
	}
	makeTemplateConfigMap(t, p, true, cmName)

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

	// Verify the ca-bundle-hash annotation is present after the update.
	hashAnnotation := deploy.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]
	if hashAnnotation == "" {
		t.Error("Deployment pod template missing mclaude.io/ca-bundle-hash annotation after update")
	}
}

// TestDeployment_CAHashChangeTriggersUpdate verifies that when the CA bundle ConfigMap
// data changes (simulating trust-manager rotating the CA), the Deployment's pod template
// annotation mclaude.io/ca-bundle-hash changes on the next ensureDeployment call.
// This change triggers a Recreate rollout.
func TestDeployment_CAHashChangeTriggersUpdate(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-cahash"
	cmName := "corp-ca-rotating"

	if _, err := p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// Create initial CA ConfigMap (simulating trust-manager sync).
	caCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: ns},
		Data:       map[string]string{"ca-certificates.crt": "-----BEGIN CERTIFICATE-----\nCAv1\n-----END CERTIFICATE-----\n"},
	}
	if _, err := p.client.CoreV1().ConfigMaps(ns).Create(ctx, caCM, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create initial CA CM: %v", err)
	}

	makeTemplateConfigMap(t, p, true, cmName)
	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate: %v", err)
	}

	// First provision.
	if err := p.ensureDeployment(ctx, ns, "proj-cahash", "cahash-user", "", tpl); err != nil {
		t.Fatalf("ensureDeployment (initial): %v", err)
	}

	deploy1, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-proj-cahash", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment (initial): %v", err)
	}
	hash1 := deploy1.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]
	if hash1 == "" {
		t.Fatal("expected ca-bundle-hash annotation after initial provision")
	}

	// Simulate trust-manager rotating the CA — update the ConfigMap data.
	caCM.Data["ca-certificates.crt"] = "-----BEGIN CERTIFICATE-----\nCAv2\n-----END CERTIFICATE-----\n"
	if _, err := p.client.CoreV1().ConfigMaps(ns).Update(ctx, caCM, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update CA CM (rotation): %v", err)
	}

	// Re-reconcile — should update the Deployment with the new hash.
	if err := p.ensureDeployment(ctx, ns, "proj-cahash", "cahash-user", "", tpl); err != nil {
		t.Fatalf("ensureDeployment (after rotation): %v", err)
	}

	deploy2, err := p.client.AppsV1().Deployments(ns).Get(ctx, "project-proj-cahash", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment (after rotation): %v", err)
	}
	hash2 := deploy2.Spec.Template.Annotations["mclaude.io/ca-bundle-hash"]
	if hash2 == "" {
		t.Fatal("expected ca-bundle-hash annotation after rotation")
	}
	if hash1 == hash2 {
		t.Errorf("expected ca-bundle-hash to change after CA rotation; both = %q", hash1)
	}
}

// TestDeployment_NamespaceGetsUserNamespaceLabel verifies that when corporateCAEnabled is true,
// ensureNamespace sets the label mclaude.io/user-namespace: "true" on the namespace so that
// trust-manager targets it for CA bundle sync.
func TestDeployment_NamespaceGetsUserNamespaceLabel(t *testing.T) {
	p := newTestProvisioner(t)
	ctx := context.Background()
	ns := "mclaude-user-nslabel"
	cmName := "corp-ca-label"

	makeTemplateConfigMap(t, p, true, cmName)
	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		t.Fatalf("loadTemplate: %v", err)
	}
	if !tpl.corporateCAEnabled {
		t.Fatal("expected corporateCAEnabled=true")
	}

	// ensureNamespace should set the user-namespace label.
	if err := p.ensureNamespace(ctx, ns, "nslabel-user", tpl); err != nil {
		t.Fatalf("ensureNamespace: %v", err)
	}

	namespace, err := p.client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	if namespace.Labels["mclaude.io/user-namespace"] != "true" {
		t.Errorf("expected mclaude.io/user-namespace=true label, got %q", namespace.Labels["mclaude.io/user-namespace"])
	}
}

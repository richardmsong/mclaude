package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nats-io/nkeys"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// K8sProvisioner creates per-project Kubernetes resources (Namespace, Deployment, PVC)
// when a project is created. It reads image configuration from the session-agent-template
// ConfigMap deployed by Helm into the control-plane namespace.
//
// If the control-plane is not running inside a Kubernetes cluster, NewK8sProvisioner
// returns (nil, nil) — callers treat nil as "provisioning disabled".
type K8sProvisioner struct {
	client              kubernetes.Interface
	controlPlaneNs      string
	releaseName         string
	sessionAgentNATSURL string
	accountKP           nkeys.KeyPair // signs per-user session-agent JWTs
}

// sessionAgentTpl holds parsed values from the session-agent-template ConfigMap.
type sessionAgentTpl struct {
	image                         string
	imagePullPolicy               corev1.PullPolicy
	terminationGracePeriodSeconds int64
	resources                     corev1.ResourceRequirements
	projectPvcSize                string
	projectPvcStorageClass        string
	nixPvcSize                    string
	nixPvcStorageClass            string
	// corporateCAEnabled controls whether the CA bundle volume/mount/env is injected.
	// trust-manager handles cross-namespace sync; the reconciler only adds the label
	// mclaude.io/user-namespace: "true" to user namespaces so trust-manager targets them.
	corporateCAEnabled       bool
	corporateCAConfigMapName string
	corporateCAConfigMapKey  string
}

// NewK8sProvisioner initialises a provisioner using in-cluster service-account credentials.
// accountKP is the NATS account key pair used to sign session-agent JWTs.
// Returns (nil, nil) if not running inside a cluster (e.g., during local dev or tests).
func NewK8sProvisioner(releaseName, natsURL string, accountKP nkeys.KeyPair) (*K8sProvisioner, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Not in cluster — provisioning disabled.
		return nil, nil
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	// Namespace is injected into every pod by Kubernetes.
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return nil, fmt.Errorf("read pod namespace: %w", err)
	}
	controlPlaneNs := strings.TrimSpace(string(nsBytes))

	return &K8sProvisioner{
		client:              client,
		controlPlaneNs:      controlPlaneNs,
		releaseName:         releaseName,
		sessionAgentNATSURL: sessionAgentNATSURL(natsURL, controlPlaneNs),
		accountKP:           accountKP,
	}, nil
}

// sessionAgentNATSURL returns a FQDN NATS URL suitable for pods in other namespaces.
// Input:  nats://mclaude-xxx-nats:4222   (short, resolves only within same namespace)
// Output: nats://mclaude-xxx-nats.mclaude-system.svc.cluster.local:4222
func sessionAgentNATSURL(rawURL, ns string) string {
	withoutScheme := strings.TrimPrefix(rawURL, "nats://")
	parts := strings.SplitN(withoutScheme, ":", 2)
	hostname := parts[0]
	port := ""
	if len(parts) == 2 {
		port = ":" + parts[1]
	}
	if strings.Contains(hostname, ".") {
		return rawURL // already qualified
	}
	return "nats://" + hostname + "." + ns + ".svc.cluster.local" + port
}

// ProvisionProject creates a user namespace, RBAC, PVCs, user config resources,
// and a session-agent Deployment for a newly-created project.
// Idempotent — safe to call again if resources already exist.
func (p *K8sProvisioner) ProvisionProject(ctx context.Context, userID, projectID, gitURL string) error {
	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		return fmt.Errorf("load session-agent template: %w", err)
	}

	userNs := "mclaude-" + userID

	if err := p.ensureNamespace(ctx, userNs, userID, tpl); err != nil {
		return fmt.Errorf("namespace %s: %w", userNs, err)
	}
	if err := p.ensureServiceAccount(ctx, userNs); err != nil {
		return fmt.Errorf("serviceaccount in %s: %w", userNs, err)
	}
	// Per-user resources (idempotent — only created once per user namespace).
	if err := p.ensureUserConfig(ctx, userNs); err != nil {
		return fmt.Errorf("user-config: %w", err)
	}
	if err := p.ensureUserSecrets(ctx, userNs, userID); err != nil {
		return fmt.Errorf("user-secrets: %w", err)
	}
	// Copy registry pull secrets from control-plane namespace so pods can pull images.
	if err := p.ensureImagePullSecrets(ctx, userNs); err != nil {
		return fmt.Errorf("image pull secrets: %w", err)
	}
	// Per-project PVCs.
	if err := p.ensurePVC(ctx, userNs, "project-"+projectID, tpl.projectPvcSize, tpl.projectPvcStorageClass); err != nil {
		return fmt.Errorf("project pvc: %w", err)
	}
	// Nix-store PVC: spec says RWX shared per namespace; dev clusters (local-path)
	// only support RWO so each project gets its own nix PVC.
	if err := p.ensurePVC(ctx, userNs, "nix-"+projectID, tpl.nixPvcSize, tpl.nixPvcStorageClass); err != nil {
		return fmt.Errorf("nix pvc: %w", err)
	}
	if err := p.ensureDeployment(ctx, userNs, projectID, userID, gitURL, tpl); err != nil {
		return fmt.Errorf("deployment: %w", err)
	}
	return nil
}

func (p *K8sProvisioner) loadTemplate(ctx context.Context) (*sessionAgentTpl, error) {
	cmName := p.releaseName + "-session-agent-template"
	cm, err := p.client.CoreV1().ConfigMaps(p.controlPlaneNs).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get configmap %s: %w", cmName, err)
	}

	tpl := &sessionAgentTpl{
		image:                    cm.Data["image"],
		imagePullPolicy:          corev1.PullPolicy(cm.Data["imagePullPolicy"]),
		projectPvcSize:           cm.Data["projectPvcSize"],
		projectPvcStorageClass:   cm.Data["projectPvcStorageClass"],
		nixPvcSize:               cm.Data["nixPvcSize"],
		nixPvcStorageClass:       cm.Data["nixPvcStorageClass"],
		corporateCAConfigMapName: cm.Data["corporateCAConfigMapName"],
		corporateCAConfigMapKey:  cm.Data["corporateCAConfigMapKey"],
	}
	tpl.corporateCAEnabled = cm.Data["corporateCAEnabled"] == "true"

	if tpl.projectPvcSize == "" {
		tpl.projectPvcSize = "10Gi"
	}
	if tpl.nixPvcSize == "" {
		tpl.nixPvcSize = "10Gi"
	}
	if tpl.corporateCAConfigMapKey == "" {
		tpl.corporateCAConfigMapKey = "ca-certificates.crt"
	}

	if v := cm.Data["terminationGracePeriodSeconds"]; v != "" {
		var s int64
		if _, err := fmt.Sscanf(v, "%d", &s); err == nil {
			tpl.terminationGracePeriodSeconds = s
		}
	}
	if tpl.terminationGracePeriodSeconds == 0 {
		tpl.terminationGracePeriodSeconds = 30
	}

	if v := cm.Data["resourcesJson"]; v != "" {
		_ = json.Unmarshal([]byte(v), &tpl.resources)
	}
	// Fallback resource defaults if not set.
	if tpl.resources.Requests == nil {
		tpl.resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		}
	}
	if tpl.resources.Limits == nil {
		tpl.resources.Limits = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2000m"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		}
	}

	return tpl, nil
}

func (p *K8sProvisioner) ensureNamespace(ctx context.Context, ns, userID string, tpl *sessionAgentTpl) error {
	existing, err := p.client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		// Namespace exists — ensure labels are up to date (e.g. add user-namespace label when CA enabled).
		if existing.Labels == nil {
			existing.Labels = make(map[string]string)
		}
		existing.Labels["mclaude.io/user-id"] = userID
		existing.Labels["mclaude.io/managed"] = "true"
		if tpl != nil && tpl.corporateCAEnabled {
			existing.Labels["mclaude.io/user-namespace"] = "true"
		}
		_, updateErr := p.client.CoreV1().Namespaces().Update(ctx, existing, metav1.UpdateOptions{})
		return updateErr
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}

	labels := map[string]string{
		"mclaude.io/user-id": userID,
		"mclaude.io/managed": "true",
	}
	if tpl != nil && tpl.corporateCAEnabled {
		labels["mclaude.io/user-namespace"] = "true"
	}

	_, err = p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   ns,
			Labels: labels,
		},
	}, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (p *K8sProvisioner) ensureServiceAccount(ctx context.Context, ns string) error {
	_, err := p.client.CoreV1().ServiceAccounts(ns).Get(ctx, "mclaude-sa", metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		_, err = p.client.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "mclaude-sa", Namespace: ns},
		}, metav1.CreateOptions{})
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA: %w", err)
		}
	} else if err != nil {
		return err
	}

	_, err = p.client.RbacV1().Roles(ns).Get(ctx, "mclaude-role", metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		_, err = p.client.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: ns},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"configmaps"},
					ResourceNames: []string{"user-config"},
					Verbs:         []string{"get", "watch", "patch"},
				},
				{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: []string{"user-secrets"},
					Verbs:         []string{"get"},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("create Role: %w", err)
		}
	} else if err != nil {
		return err
	}

	_, err = p.client.RbacV1().RoleBindings(ns).Get(ctx, "mclaude-role", metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		_, err = p.client.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: ns},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "mclaude-sa", Namespace: ns}},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "mclaude-role",
			},
		}, metav1.CreateOptions{})
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("create RoleBinding: %w", err)
		}
	} else if err != nil {
		return err
	}

	return nil
}

// ensureUserConfig creates the user-config ConfigMap in the user namespace if it doesn't exist.
// Session-agents mount this read-only at /home/node/.claude-seed/ to seed their Claude home.
// Initially empty — the config-sync sidecar writes to it when the user updates their config.
func (p *K8sProvisioner) ensureUserConfig(ctx context.Context, ns string) error {
	_, err := p.client.CoreV1().ConfigMaps(ns).Get(ctx, "user-config", metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}
	_, err = p.client.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "user-config", Namespace: ns},
		Data:       map[string]string{},
	}, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// ensureUserSecrets creates the user-secrets Secret in the user namespace if it doesn't exist.
// Session-agents mount this read-only at /home/node/.user-secrets/ for API keys and credentials.
// The Secret is populated with a NATS credentials file (key "nats-creds") scoped to
// mclaude.{userID}.> so the session-agent can authenticate to NATS.
func (p *K8sProvisioner) ensureUserSecrets(ctx context.Context, ns, userID string) error {
	existing, err := p.client.CoreV1().Secrets(ns).Get(ctx, "user-secrets", metav1.GetOptions{})
	if err == nil {
		// Secret exists — only return early if nats-creds is already populated.
		if len(existing.Data["nats-creds"]) > 0 {
			return nil
		}
		// Secret exists but is missing nats-creds (created empty by old code).
		// Generate credentials and patch the Secret in place.
		jwt, seed, err := IssueSessionAgentJWT(userID, p.accountKP)
		if err != nil {
			return fmt.Errorf("issue session-agent jwt for %s: %w", userID, err)
		}
		if existing.Data == nil {
			existing.Data = make(map[string][]byte)
		}
		existing.Data["nats-creds"] = FormatNATSCredentials(jwt, seed)
		_, err = p.client.CoreV1().Secrets(ns).Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}

	// Secret does not exist — generate credentials and create it.
	jwt, seed, err := IssueSessionAgentJWT(userID, p.accountKP)
	if err != nil {
		return fmt.Errorf("issue session-agent jwt for %s: %w", userID, err)
	}
	natsCreds := FormatNATSCredentials(jwt, seed)

	_, err = p.client.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: ns},
		Data: map[string][]byte{
			"nats-creds": natsCreds,
		},
	}, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// ensureImagePullSecrets copies registry pull secrets from the control-plane namespace
// to the user namespace. Session-agent pods need these to pull images from private registries.
// Only copies secrets of type kubernetes.io/dockerconfigjson.
func (p *K8sProvisioner) ensureImagePullSecrets(ctx context.Context, destNs string) error {
	secrets, err := p.client.CoreV1().Secrets(p.controlPlaneNs).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list secrets in %s: %w", p.controlPlaneNs, err)
	}
	for _, src := range secrets.Items {
		if src.Type != corev1.SecretTypeDockerConfigJson {
			continue
		}
		_, err := p.client.CoreV1().Secrets(destNs).Get(ctx, src.Name, metav1.GetOptions{})
		if err == nil {
			continue // already exists
		}
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("check secret %s: %w", src.Name, err)
		}
		copySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: src.Name, Namespace: destNs},
			Type:       src.Type,
			Data:       src.Data,
		}
		if _, err := p.client.CoreV1().Secrets(destNs).Create(ctx, copySecret, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("copy secret %s: %w", src.Name, err)
		}
	}
	return nil
}

func (p *K8sProvisioner) ensurePVC(ctx context.Context, ns, name, size, storageClass string) error {
	_, err := p.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}

	qty, err := resource.ParseQuantity(size)
	if err != nil {
		qty = resource.MustParse("10Gi")
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
			},
		},
	}
	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}

	_, err = p.client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// caConfigMapHash computes a deterministic SHA-256 hash of a ConfigMap's string and binary data.
// Used as the mclaude.io/ca-bundle-hash pod template annotation so that CA bundle rotations
// trigger a Recreate rollout.
func caConfigMapHash(cm *corev1.ConfigMap) string {
	h := sha256.New()
	// Sort keys for determinism.
	keys := make([]string, 0, len(cm.Data))
	for k := range cm.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(cm.Data[k]))
	}
	binKeys := make([]string, 0, len(cm.BinaryData))
	for k := range cm.BinaryData {
		binKeys = append(binKeys, k)
	}
	sort.Strings(binKeys)
	for _, k := range binKeys {
		h.Write([]byte(k))
		h.Write(cm.BinaryData[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildPodSpec constructs the pod template spec for a session-agent Deployment.
// Shared between create and update paths to keep both consistent.
func (p *K8sProvisioner) buildPodSpec(
	ctx context.Context,
	ns, projectID, userID, gitURL string,
	tpl *sessionAgentTpl,
	imagePullSecrets []corev1.LocalObjectReference,
) corev1.PodSpec {
	runAsUser := int64(1000)
	fsGroup := int64(1000)
	runAsNonRoot := true
	grace := tpl.terminationGracePeriodSeconds

	env := []corev1.EnvVar{
		{Name: "USER_ID", Value: userID},
		{Name: "PROJECT_ID", Value: projectID},
		{Name: "NATS_URL", Value: p.sessionAgentNATSURL},
	}
	if gitURL != "" {
		env = append(env, corev1.EnvVar{Name: "GIT_URL", Value: gitURL})
	}

	volumes := []corev1.Volume{
		{
			Name: "project-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "project-" + projectID,
				},
			},
		},
		{
			Name: "nix-store",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "nix-" + projectID,
				},
			},
		},
		{
			Name: "claude-home",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "user-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "user-config"},
				},
			},
		},
		{
			Name: "user-secrets",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "user-secrets",
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "project-data", MountPath: "/data"},
		{Name: "nix-store", MountPath: "/nix"},
		{Name: "claude-home", MountPath: "/home/node/.claude"},
		{Name: "user-config", MountPath: "/home/node/.claude-seed", ReadOnly: true},
		{Name: "user-secrets", MountPath: "/home/node/.user-secrets", ReadOnly: true},
	}

	// Corporate CA bundle — only when enabled and the ConfigMap exists in the user namespace.
	// trust-manager syncs the ConfigMap; it may not be present yet on first provision.
	if tpl.corporateCAEnabled && tpl.corporateCAConfigMapName != "" {
		_, err := p.client.CoreV1().ConfigMaps(ns).Get(ctx, tpl.corporateCAConfigMapName, metav1.GetOptions{})
		if err == nil {
			volumes = append(volumes, corev1.Volume{
				Name: "corporate-ca",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: tpl.corporateCAConfigMapName},
					},
				},
			})
			subPath := tpl.corporateCAConfigMapKey
			if subPath == "" {
				subPath = "ca-certificates.crt"
			}
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "corporate-ca",
				MountPath: "/etc/ssl/certs/corporate-ca-certificates.crt",
				SubPath:   subPath,
				ReadOnly:  true,
			})
			env = append(env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/etc/ssl/certs/corporate-ca-certificates.crt",
			})
		}
	}

	return corev1.PodSpec{
		ServiceAccountName:            "mclaude-sa",
		ImagePullSecrets:              imagePullSecrets,
		TerminationGracePeriodSeconds: &grace,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: &runAsNonRoot,
			RunAsUser:    &runAsUser,
			FSGroup:      &fsGroup,
		},
		Volumes: volumes,
		Containers: []corev1.Container{
			{
				Name:            "session-agent",
				Image:           tpl.image,
				ImagePullPolicy: tpl.imagePullPolicy,
				Env:             env,
				Resources:       tpl.resources,
				VolumeMounts:    volumeMounts,
			},
		},
	}
}

// buildPodTemplateAnnotations computes pod template annotations for the session-agent Deployment.
// When corporate CA is enabled and the ConfigMap exists in the user namespace, adds the
// mclaude.io/ca-bundle-hash annotation so that CA bundle rotations trigger Recreate rollout.
func (p *K8sProvisioner) buildPodTemplateAnnotations(ctx context.Context, ns string, tpl *sessionAgentTpl) map[string]string {
	annotations := map[string]string{}
	if tpl.corporateCAEnabled && tpl.corporateCAConfigMapName != "" {
		cm, err := p.client.CoreV1().ConfigMaps(ns).Get(ctx, tpl.corporateCAConfigMapName, metav1.GetOptions{})
		if err == nil {
			annotations["mclaude.io/ca-bundle-hash"] = caConfigMapHash(cm)
		}
	}
	return annotations
}

// ensureDeployment creates or updates the session-agent Deployment for a project.
// Per docs/plan-graceful-upgrades.md: both create and update paths set Recreate strategy
// so the old pod exits before the new pod starts during image upgrades.
//
// The update path rebuilds the full pod template spec (volumes, containers, env, mounts)
// to match the current template — this ensures that CA configuration changes propagate
// to existing Deployments on the next reconcile cycle.
func (p *K8sProvisioner) ensureDeployment(ctx context.Context, ns, projectID, userID, gitURL string, tpl *sessionAgentTpl) error {
	name := "project-" + projectID

	// Collect imagePullSecrets from any docker config secrets in the user namespace.
	var imagePullSecrets []corev1.LocalObjectReference
	secrets, err := p.client.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, s := range secrets.Items {
			if s.Type == corev1.SecretTypeDockerConfigJson {
				imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: s.Name})
			}
		}
	}

	podSpec := p.buildPodSpec(ctx, ns, projectID, userID, gitURL, tpl, imagePullSecrets)
	annotations := p.buildPodTemplateAnnotations(ctx, ns, tpl)

	existing, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		// Update path: rebuild the full pod template spec so that image, strategy,
		// volumes, env vars, and volume mounts all reflect the current template.
		existing.Spec.Template.Spec = podSpec
		existing.Spec.Template.Annotations = annotations
		existing.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		}
		_, err = p.client.AppsV1().Deployments(ns).Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}

	replicas := int32(1)

	// Create path: Recreate strategy so old pod exits before new pod starts.
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app":     "mclaude-project",
				"project": projectID,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":     "mclaude-project",
					"project": projectID,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":     "mclaude-project",
						"project": projectID,
					},
					Annotations: annotations,
				},
				Spec: podSpec,
			},
		},
	}

	_, err = p.client.AppsV1().Deployments(ns).Create(ctx, deploy, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

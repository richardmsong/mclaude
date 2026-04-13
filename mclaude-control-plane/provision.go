package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
	client             *kubernetes.Clientset
	controlPlaneNs     string
	releaseName        string
	sessionAgentNATSURL string
}

// sessionAgentTpl holds parsed values from the session-agent-template ConfigMap.
type sessionAgentTpl struct {
	image                         string
	imagePullPolicy               corev1.PullPolicy
	terminationGracePeriodSeconds int64
	resources                     corev1.ResourceRequirements
	projectPvcSize                string
	projectPvcStorageClass        string
}

// NewK8sProvisioner initialises a provisioner using in-cluster service-account credentials.
// Returns (nil, nil) if not running inside a cluster (e.g., during local dev or tests).
func NewK8sProvisioner(releaseName, natsURL string) (*K8sProvisioner, error) {
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

// ProvisionProject creates a user namespace, RBAC, PVC, and session-agent Deployment
// for a newly-created project. Idempotent — safe to call again if resources already exist.
func (p *K8sProvisioner) ProvisionProject(ctx context.Context, userID, projectID, gitURL string) error {
	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		return fmt.Errorf("load session-agent template: %w", err)
	}

	userNs := "mclaude-" + userID

	if err := p.ensureNamespace(ctx, userNs, userID); err != nil {
		return fmt.Errorf("namespace %s: %w", userNs, err)
	}
	if err := p.ensureServiceAccount(ctx, userNs); err != nil {
		return fmt.Errorf("serviceaccount in %s: %w", userNs, err)
	}
	if err := p.ensurePVC(ctx, userNs, "project-"+projectID, tpl.projectPvcSize, tpl.projectPvcStorageClass); err != nil {
		return fmt.Errorf("project pvc: %w", err)
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
		image:                  cm.Data["image"],
		imagePullPolicy:        corev1.PullPolicy(cm.Data["imagePullPolicy"]),
		projectPvcSize:         cm.Data["projectPvcSize"],
		projectPvcStorageClass: cm.Data["projectPvcStorageClass"],
	}

	if tpl.projectPvcSize == "" {
		tpl.projectPvcSize = "10Gi"
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

func (p *K8sProvisioner) ensureNamespace(ctx context.Context, ns, userID string) error {
	_, err := p.client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}
	_, err = p.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
			Labels: map[string]string{
				"mclaude.io/user-id": userID,
				"mclaude.io/managed": "true",
			},
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

func (p *K8sProvisioner) ensureDeployment(ctx context.Context, ns, projectID, userID, gitURL string, tpl *sessionAgentTpl) error {
	name := "project-" + projectID

	_, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil // already exists
	}
	if !k8serrors.IsNotFound(err) {
		return err
	}

	replicas := int32(1)
	grace := tpl.terminationGracePeriodSeconds
	runAsUser := int64(1000)
	fsGroup := int64(1000)
	runAsNonRoot := true

	env := []corev1.EnvVar{
		{Name: "USER_ID", Value: userID},
		{Name: "PROJECT_ID", Value: projectID},
		{Name: "NATS_URL", Value: p.sessionAgentNATSURL},
	}
	if gitURL != "" {
		env = append(env, corev1.EnvVar{Name: "GIT_URL", Value: gitURL})
	}

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
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            "mclaude-sa",
					TerminationGracePeriodSeconds: &grace,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
						RunAsUser:    &runAsUser,
						FSGroup:      &fsGroup,
					},
					Volumes: []corev1.Volume{
						{
							Name: "project-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: name,
								},
							},
						},
						{
							Name: "claude-home",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "session-agent",
							Image:           tpl.image,
							ImagePullPolicy: tpl.imagePullPolicy,
							Env:             env,
							Resources:       tpl.resources,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "project-data", MountPath: "/data"},
								{Name: "claude-home", MountPath: "/home/node/.claude"},
							},
						},
					},
				},
			},
		},
	}

	_, err = p.client.AppsV1().Deployments(ns).Create(ctx, deploy, metav1.CreateOptions{})
	if k8serrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

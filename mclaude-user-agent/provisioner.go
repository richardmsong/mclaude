package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rs/zerolog"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// sessionAgentTpl holds parsed values from the locally-copied session-agent-template ConfigMap.
// The user-agent reads this from its own namespace (not mclaude-system) — the controller
// copies it there at provisioning time (ADR-0091).
type sessionAgentTpl struct {
	image                         string
	imagePullPolicy               corev1.PullPolicy
	terminationGracePeriodSeconds int64
	resources                     corev1.ResourceRequirements
	projectPvcSize                string
	projectPvcStorageClass        string
	nixPvcSize                    string
	nixPvcStorageClass            string
}

// Provisioner handles project pod lifecycle for the user-agent.
type Provisioner struct {
	k8s          kubernetes.Interface
	namespace    string
	config       *AgentConfig
	log          zerolog.Logger
	tokenBaseDir string // base directory for token files; defaults to "/secrets" in production
}

// NewProvisioner creates a Provisioner using the production token path /secrets.
func NewProvisioner(k8s kubernetes.Interface, namespace string, cfg *AgentConfig, log zerolog.Logger) *Provisioner {
	return &Provisioner{
		k8s:          k8s,
		namespace:    namespace,
		config:       cfg,
		log:          log,
		tokenBaseDir: "/secrets",
	}
}

// NewProvisionerWithTokenDir creates a Provisioner with a custom token base directory.
// Used in tests to redirect token file reads away from /secrets.
func NewProvisionerWithTokenDir(k8s kubernetes.Interface, namespace string, cfg *AgentConfig, tokenBaseDir string, log zerolog.Logger) *Provisioner {
	return &Provisioner{
		k8s:          k8s,
		namespace:    namespace,
		config:       cfg,
		log:          log,
		tokenBaseDir: tokenBaseDir,
	}
}

// ProvisionRequest is the NATS payload for agent.projects.provision.
type ProvisionRequest struct {
	ProjectID     string `json:"projectId"`
	ProjectSlug   string `json:"projectSlug"`
	GitURL        string `json:"gitUrl"`
	GitIdentityID string `json:"gitIdentityId"`
	Backend       string `json:"backend"`
	UserID        string `json:"userId,omitempty"`
	Provider      string `json:"provider,omitempty"` // defaults to "anthropic"
}

// ProvisionReply is the NATS reply for agent.projects.provision.
type ProvisionReply struct {
	OK     bool   `json:"ok,omitempty"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// DeleteRequest is the NATS payload for agent.projects.delete.
type DeleteRequest struct {
	ProjectID   string `json:"projectId"`
	ProjectSlug string `json:"projectSlug"`
}

// Provision creates the project Deployment + PVCs in the user namespace.
// Per spec: verifies token exists, reads template, builds pod spec, creates resources.
func (p *Provisioner) Provision(ctx context.Context, req ProvisionRequest) ProvisionReply {
	provider := req.Provider
	if provider == "" {
		provider = "anthropic"
	}

	// Verify token exists for the requested provider (spec: "verify /secrets/tokens/{provider} non-empty").
	tokenPath := p.tokenBaseDir + "/tokens/" + provider
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil || len(tokenBytes) == 0 {
		p.log.Warn().Str("provider", provider).Msg("provision: token not available")
		return ProvisionReply{Error: "host_not_trusted"}
	}

	// Load session-agent-template ConfigMap from the user's own namespace.
	tpl, err := p.loadTemplate(ctx)
	if err != nil {
		p.log.Error().Err(err).Msg("provision: load session-agent-template failed")
		return ProvisionReply{Error: fmt.Sprintf("template_load_failed: %v", err)}
	}

	// Ensure PVCs exist.
	if err := p.ensurePVC(ctx, "project-"+req.ProjectID, tpl.projectPvcSize, tpl.projectPvcStorageClass); err != nil {
		p.log.Error().Err(err).Str("pvc", "project-"+req.ProjectID).Msg("provision: create project PVC failed")
		return ProvisionReply{Error: fmt.Sprintf("pvc_create_failed: %v", err)}
	}
	if err := p.ensurePVC(ctx, "nix-"+req.ProjectID, tpl.nixPvcSize, tpl.nixPvcStorageClass); err != nil {
		p.log.Error().Err(err).Str("pvc", "nix-"+req.ProjectID).Msg("provision: create nix PVC failed")
		return ProvisionReply{Error: fmt.Sprintf("pvc_create_failed: %v", err)}
	}

	// Build and apply the Deployment.
	deploy := p.buildDeployment(req, tpl, provider)
	existing := &appsv1.Deployment{}
	_, getErr := p.k8s.AppsV1().Deployments(p.namespace).Get(ctx, deploy.Name, metav1.GetOptions{})
	if getErr == nil {
		// Already exists — idempotent update.
		_, updateErr := p.k8s.AppsV1().Deployments(p.namespace).Update(ctx, deploy, metav1.UpdateOptions{})
		if updateErr != nil {
			p.log.Error().Err(updateErr).Str("deploy", deploy.Name).Msg("provision: update deployment failed")
			return ProvisionReply{Error: fmt.Sprintf("deploy_update_failed: %v", updateErr)}
		}
	} else if k8serrors.IsNotFound(getErr) {
		_, createErr := p.k8s.AppsV1().Deployments(p.namespace).Create(ctx, deploy, metav1.CreateOptions{})
		if createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
			p.log.Error().Err(createErr).Str("deploy", deploy.Name).Msg("provision: create deployment failed")
			return ProvisionReply{Error: fmt.Sprintf("deploy_create_failed: %v", createErr)}
		}
	} else {
		p.log.Error().Err(getErr).Str("deploy", deploy.Name).Msg("provision: get deployment failed")
		return ProvisionReply{Error: fmt.Sprintf("deploy_get_failed: %v", getErr)}
	}
	_ = existing

	p.log.Info().
		Str("projectId", req.ProjectID).
		Str("projectSlug", req.ProjectSlug).
		Msg("provision: deployment created/updated")
	return ProvisionReply{OK: true, Status: "provisioning"}
}

// Delete removes the project Deployment. Best-effort; treats NotFound as success.
func (p *Provisioner) Delete(ctx context.Context, req DeleteRequest) {
	deployName := "project-" + req.ProjectID
	err := p.k8s.AppsV1().Deployments(p.namespace).Delete(ctx, deployName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		p.log.Error().Err(err).Str("deploy", deployName).Msg("delete: failed to delete deployment")
		return
	}
	p.log.Info().Str("deploy", deployName).Msg("delete: deployment deleted (or not found)")
}

// loadTemplate reads the session-agent-template ConfigMap from the user's own namespace.
func (p *Provisioner) loadTemplate(ctx context.Context) (*sessionAgentTpl, error) {
	cm, err := p.k8s.CoreV1().ConfigMaps(p.namespace).Get(ctx, "session-agent-template", metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return defaultSessionAgentTpl(), nil
		}
		return nil, fmt.Errorf("get session-agent-template: %w", err)
	}

	tpl := &sessionAgentTpl{
		image:                  cm.Data["image"],
		imagePullPolicy:        corev1.PullPolicy(cm.Data["imagePullPolicy"]),
		projectPvcSize:         cm.Data["projectPvcSize"],
		projectPvcStorageClass: cm.Data["projectPvcStorageClass"],
		nixPvcSize:             cm.Data["nixPvcSize"],
		nixPvcStorageClass:     cm.Data["nixPvcStorageClass"],
	}
	if tpl.image == "" {
		tpl.image = "mclaude-session-agent:latest"
	}
	if tpl.imagePullPolicy == "" {
		tpl.imagePullPolicy = corev1.PullIfNotPresent
	}
	if tpl.projectPvcSize == "" {
		tpl.projectPvcSize = "10Gi"
	}
	if tpl.nixPvcSize == "" {
		tpl.nixPvcSize = "10Gi"
	}
	if v := cm.Data["terminationGracePeriodSeconds"]; v != "" {
		var s int64
		fmt.Sscanf(v, "%d", &s) //nolint:errcheck
		tpl.terminationGracePeriodSeconds = s
	}
	if tpl.terminationGracePeriodSeconds == 0 {
		tpl.terminationGracePeriodSeconds = 30
	}
	if v := cm.Data["resourcesJson"]; v != "" {
		_ = json.Unmarshal([]byte(v), &tpl.resources)
	}
	applyDefaultResources(tpl)
	return tpl, nil
}

func defaultSessionAgentTpl() *sessionAgentTpl {
	tpl := &sessionAgentTpl{
		image:                         "mclaude-session-agent:latest",
		imagePullPolicy:               corev1.PullIfNotPresent,
		projectPvcSize:                "10Gi",
		nixPvcSize:                    "10Gi",
		terminationGracePeriodSeconds: 30,
	}
	applyDefaultResources(tpl)
	return tpl
}

func applyDefaultResources(tpl *sessionAgentTpl) {
	if tpl.resources.Requests == nil {
		tpl.resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		}
	}
	if tpl.resources.Limits == nil {
		tpl.resources.Limits = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2000m"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		}
	}
}

// buildDeployment constructs the project Deployment per ADR-0091 spec.
func (p *Provisioner) buildDeployment(req ProvisionRequest, tpl *sessionAgentTpl, provider string) *appsv1.Deployment {
	deployName := "project-" + req.ProjectID
	replicas := int32(1)
	grace := tpl.terminationGracePeriodSeconds
	runAsUser := int64(1000)
	fsGroup := int64(1000)
	runAsNonRoot := true

	uslug := p.config.UserSlug

	// Env vars per spec (ADR-0091): TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL,
	// HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL.
	env := []corev1.EnvVar{
		{Name: "TOKEN_PROVIDER", Value: provider},
		{Name: "PROJECT_ID", Value: req.ProjectID},
		{Name: "PROJECT_SLUG", Value: req.ProjectSlug},
		{Name: "GIT_URL", Value: req.GitURL},
		{Name: "HOST_SLUG", Value: p.config.HostSlug},
		{Name: "USER_SLUG", Value: uslug},
		{Name: "USER_ID", Value: req.UserID},
		{Name: "GIT_IDENTITY_ID", Value: req.GitIdentityID},
		{Name: "NATS_URL", Value: p.config.NATSURL},
		{Name: "CONTROL_PLANE_URL", Value: p.config.ControlPlaneURL},
	}

	// Volumes per spec (ADR-0091 + spec-user-agent.md):
	//   project-{projectId} PVC at /data
	//   nix-{projectId} PVC at /nix
	//   user-config ConfigMap at /home/node/.claude-seed
	//   user-secrets Secret at /home/node/.user-secrets
	//   user-tokens-{uslug} Secret at /secrets/tokens/ (ADR-0091 addition)
	//   claude-home emptyDir at /home/node/.claude
	volumes := []corev1.Volume{
		{
			Name: "project-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "project-" + req.ProjectID,
				},
			},
		},
		{
			Name: "nix-store",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "nix-" + req.ProjectID,
				},
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
		{
			Name: "user-tokens",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "user-tokens-" + uslug,
				},
			},
		},
		{
			Name:         "claude-home",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "project-data", MountPath: "/data"},
		{Name: "nix-store", MountPath: "/nix"},
		{Name: "user-config", MountPath: "/home/node/.claude-seed", ReadOnly: true},
		{Name: "user-secrets", MountPath: "/home/node/.user-secrets", ReadOnly: true},
		{Name: "user-tokens", MountPath: "/secrets/tokens/", ReadOnly: true},
		{Name: "claude-home", MountPath: "/home/node/.claude"},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: p.namespace,
			Labels: map[string]string{
				"app":           "mclaude-project",
				"project":       req.ProjectID,
				"mclaude/managed": "true",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":     "mclaude-project",
					"project": req.ProjectID,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":     "mclaude-project",
						"project": req.ProjectID,
					},
				},
				Spec: corev1.PodSpec{
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
				},
			},
		},
	}
}

// ensurePVC creates a PVC if it does not already exist.
func (p *Provisioner) ensurePVC(ctx context.Context, name, size, storageClass string) error {
	_, err := p.k8s.CoreV1().PersistentVolumeClaims(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil // already exists
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get pvc %s: %w", name, err)
	}

	qty, parseErr := resource.ParseQuantity(size)
	if parseErr != nil {
		qty = resource.MustParse("10Gi")
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: p.namespace,
			Labels:    map[string]string{"mclaude/managed": "true"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: qty,
				},
			},
		},
	}
	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}

	_, createErr := p.k8s.CoreV1().PersistentVolumeClaims(p.namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create pvc %s: %w", name, createErr)
	}
	return nil
}

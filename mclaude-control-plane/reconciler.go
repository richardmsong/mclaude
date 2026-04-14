// reconciler.go implements the controller-runtime reconciler for MCProject CRDs.
// The reconciler ensures K8s resources match the desired state in the MCProject spec.
// On any drift (deleted namespace, deleted secret, deleted deployment), the reconciler
// recreates the missing resource on the next reconcile cycle.
//
// See docs/plan-k8s-integration.md "Reconciliation Controller" section.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MCProjectReconciler reconciles MCProject CRs.
// It re-uses the same provisioning logic as K8sProvisioner, but drives it from a
// controller-runtime Manager rather than being called imperatively.
type MCProjectReconciler struct {
	client              client.Client
	scheme              *runtime.Scheme
	controlPlaneNs      string
	releaseName         string
	sessionAgentNATSURL string
	accountKP           nkeys.KeyPair
	devOAuthToken       string // optional: injected into user-secrets when DEV_OAUTH_TOKEN is set
	logger              zerolog.Logger
}

// Reconcile is called whenever an MCProject CR changes, or when a watched resource
// (Deployment, Secret, ConfigMap, ServiceAccount) in a user namespace changes.
func (r *MCProjectReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger.With().
		Str("namespace", req.Namespace).
		Str("name", req.Name).
		Logger()

	// Fetch the MCProject CR.
	var mcp MCProject
	if err := r.client.Get(ctx, req.NamespacedName, &mcp); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get MCProject: %w", err)
	}

	// Set phase to Provisioning at the start if not yet set.
	if mcp.Status.Phase == "" {
		if err := r.setPhase(ctx, &mcp, PhaseProvisioning); err != nil {
			log.Warn().Err(err).Msg("set phase Provisioning")
		}
	}

	userNs := "mclaude-" + mcp.Spec.UserID
	log = log.With().Str("userNs", userNs).Str("projectId", mcp.Spec.ProjectID).Logger()

	// Load session-agent template from the control-plane namespace ConfigMap.
	tpl, err := r.loadTemplate(ctx)
	if err != nil {
		log.Error().Err(err).Msg("load session-agent template")
		r.setCondition(ctx, &mcp, string(ConditionDeploymentReady), metav1.ConditionFalse, "TemplateError", err.Error())
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Step 2: Ensure user namespace.
	nsErr := r.reconcileNamespace(ctx, &mcp, userNs)
	r.updateCondition(ctx, &mcp, string(ConditionNamespaceReady), nsErr)
	if nsErr != nil {
		log.Error().Err(nsErr).Msg("ensure namespace")
		r.setPhase(ctx, &mcp, PhaseFailed) //nolint:errcheck
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Steps 3-4: RBAC (ServiceAccount, Role, RoleBinding).
	rbacErr := r.reconcileRBAC(ctx, &mcp, userNs)
	r.updateCondition(ctx, &mcp, string(ConditionRBACReady), rbacErr)
	if rbacErr != nil {
		log.Error().Err(rbacErr).Msg("ensure RBAC")
	}

	// Steps 4-6: ConfigMap, Secrets, imagePullSecrets.
	secretsErr := r.reconcileSecrets(ctx, &mcp, userNs)
	r.updateCondition(ctx, &mcp, string(ConditionSecretsReady), secretsErr)
	if secretsErr != nil {
		log.Error().Err(secretsErr).Msg("ensure secrets")
	}

	// Steps 7-9: PVCs + Deployment.
	deployErr := r.reconcileDeployment(ctx, &mcp, userNs, tpl)
	r.updateCondition(ctx, &mcp, string(ConditionDeploymentReady), deployErr)
	if deployErr != nil {
		log.Error().Err(deployErr).Msg("ensure deployment")
	}

	// Step 10: Update phase based on conditions.
	allReady := nsErr == nil && rbacErr == nil && secretsErr == nil && deployErr == nil
	if allReady {
		now := metav1.Now()
		mcp.Status.LastReconciledAt = &now
		mcp.Status.UserNamespace = userNs
		r.setPhase(ctx, &mcp, PhaseReady) //nolint:errcheck
		log.Info().Msg("MCProject reconciled — all conditions ready")
	} else {
		r.setPhase(ctx, &mcp, PhaseProvisioning) //nolint:errcheck
		return reconcile.Result{RequeueAfter: 15 * time.Second}, nil
	}

	return reconcile.Result{}, nil
}

// reconcileNamespace ensures the user namespace exists with the correct labels.
func (r *MCProjectReconciler) reconcileNamespace(ctx context.Context, mcp *MCProject, userNs string) error {
	ns := &corev1.Namespace{}
	err := r.client.Get(ctx, types.NamespacedName{Name: userNs}, ns)
	if err == nil {
		if ns.Labels == nil {
			ns.Labels = make(map[string]string)
		}
		ns.Labels["mclaude.io/user-id"] = mcp.Spec.UserID
		ns.Labels["mclaude.io/managed"] = "true"
		return r.client.Update(ctx, ns)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get namespace: %w", err)
	}
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: userNs,
			Labels: map[string]string{
				"mclaude.io/user-id": mcp.Spec.UserID,
				"mclaude.io/managed": "true",
			},
		},
	}
	if createErr := r.client.Create(ctx, ns); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create namespace: %w", createErr)
	}
	return nil
}

// reconcileRBAC ensures ServiceAccount, Role, and RoleBinding exist in the user namespace.
func (r *MCProjectReconciler) reconcileRBAC(ctx context.Context, mcp *MCProject, userNs string) error {
	// ServiceAccount
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-sa", Namespace: userNs},
	}
	if err := r.ensureOwned(ctx, mcp, sa, func() error {
		return r.client.Create(ctx, sa)
	}); err != nil {
		return fmt.Errorf("serviceaccount: %w", err)
	}

	// Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: userNs},
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
	}
	if err := r.ensureOwned(ctx, mcp, role, func() error {
		return r.client.Create(ctx, role)
	}); err != nil {
		return fmt.Errorf("role: %w", err)
	}

	// RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: userNs},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "mclaude-sa", Namespace: userNs}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "mclaude-role",
		},
	}
	if err := r.ensureOwned(ctx, mcp, rb, func() error {
		return r.client.Create(ctx, rb)
	}); err != nil {
		return fmt.Errorf("rolebinding: %w", err)
	}
	return nil
}

// reconcileSecrets ensures user-config ConfigMap, user-secrets Secret, and imagePullSecrets.
func (r *MCProjectReconciler) reconcileSecrets(ctx context.Context, mcp *MCProject, userNs string) error {
	// user-config ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "user-config", Namespace: userNs},
		Data:       map[string]string{},
	}
	if err := r.ensureOwned(ctx, mcp, cm, func() error {
		return r.client.Create(ctx, cm)
	}); err != nil {
		return fmt.Errorf("user-config configmap: %w", err)
	}

	// user-secrets Secret (NATS creds)
	existingSecret := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, existingSecret)
	if err == nil {
		if len(existingSecret.Data["nats-creds"]) == 0 {
			jwtStr, seed, issueErr := IssueSessionAgentJWT(mcp.Spec.UserID, r.accountKP)
			if issueErr != nil {
				return fmt.Errorf("issue session-agent jwt: %w", issueErr)
			}
			if existingSecret.Data == nil {
				existingSecret.Data = make(map[string][]byte)
			}
			existingSecret.Data["nats-creds"] = FormatNATSCredentials(jwtStr, seed)
			if r.devOAuthToken != "" && len(existingSecret.Data["oauth-token"]) == 0 {
				existingSecret.Data["oauth-token"] = []byte(r.devOAuthToken)
			}
			if updateErr := r.client.Update(ctx, existingSecret); updateErr != nil {
				return fmt.Errorf("patch user-secrets: %w", updateErr)
			}
		}
	} else if k8serrors.IsNotFound(err) {
		jwtStr, seed, issueErr := IssueSessionAgentJWT(mcp.Spec.UserID, r.accountKP)
		if issueErr != nil {
			return fmt.Errorf("issue session-agent jwt: %w", issueErr)
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: userNs},
			Data: map[string][]byte{
				"nats-creds": FormatNATSCredentials(jwtStr, seed),
			},
		}
		if r.devOAuthToken != "" {
			secret.Data["oauth-token"] = []byte(r.devOAuthToken)
		}
		if createErr := r.client.Create(ctx, secret); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
			return fmt.Errorf("create user-secrets: %w", createErr)
		}
	} else {
		return fmt.Errorf("get user-secrets: %w", err)
	}

	// imagePullSecrets — copy dockerconfigjson secrets from control-plane namespace.
	secretList := &corev1.SecretList{}
	if listErr := r.client.List(ctx, secretList, client.InNamespace(r.controlPlaneNs)); listErr != nil {
		return fmt.Errorf("list secrets in %s: %w", r.controlPlaneNs, listErr)
	}
	for i := range secretList.Items {
		src := &secretList.Items[i]
		if src.Type != corev1.SecretTypeDockerConfigJson {
			continue
		}
		destSecret := &corev1.Secret{}
		if getErr := r.client.Get(ctx, types.NamespacedName{Name: src.Name, Namespace: userNs}, destSecret); getErr == nil {
			continue
		}
		copySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: src.Name, Namespace: userNs},
			Type:       src.Type,
			Data:       src.Data,
		}
		if createErr := r.client.Create(ctx, copySecret); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
			return fmt.Errorf("copy imagePullSecret %s: %w", src.Name, createErr)
		}
	}
	return nil
}

// reconcileDeployment ensures the project PVC, nix PVC, and Deployment exist.
func (r *MCProjectReconciler) reconcileDeployment(ctx context.Context, mcp *MCProject, userNs string, tpl *sessionAgentTpl) error {
	projectID := mcp.Spec.ProjectID
	userID := mcp.Spec.UserID
	gitURL := mcp.Spec.GitURL

	if err := r.ensurePVCCR(ctx, mcp, userNs, "project-"+projectID, tpl.projectPvcSize, tpl.projectPvcStorageClass); err != nil {
		return fmt.Errorf("project pvc: %w", err)
	}
	if err := r.ensurePVCCR(ctx, mcp, userNs, "nix-"+projectID, tpl.nixPvcSize, tpl.nixPvcStorageClass); err != nil {
		return fmt.Errorf("nix pvc: %w", err)
	}

	deployName := "project-" + projectID
	existing := &appsv1.Deployment{}
	err := r.client.Get(ctx, types.NamespacedName{Name: deployName, Namespace: userNs}, existing)
	if err == nil {
		if len(existing.Spec.Template.Spec.Containers) > 0 {
			existing.Spec.Template.Spec.Containers[0].Image = tpl.image
		}
		return r.client.Update(ctx, existing)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get deployment: %w", err)
	}

	replicas := int32(1)
	grace := tpl.terminationGracePeriodSeconds
	runAsUser := int64(1000)
	fsGroup := int64(1000)
	runAsNonRoot := true

	var imagePullSecrets []corev1.LocalObjectReference
	nsList := &corev1.SecretList{}
	if listErr := r.client.List(ctx, nsList, client.InNamespace(userNs)); listErr == nil {
		for _, s := range nsList.Items {
			if s.Type == corev1.SecretTypeDockerConfigJson {
				imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: s.Name})
			}
		}
	}

	env := []corev1.EnvVar{
		{Name: "USER_ID", Value: userID},
		{Name: "PROJECT_ID", Value: projectID},
		{Name: "NATS_URL", Value: r.sessionAgentNATSURL},
	}
	if gitURL != "" {
		env = append(env, corev1.EnvVar{Name: "GIT_URL", Value: gitURL})
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: userNs,
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
					ImagePullSecrets:              imagePullSecrets,
					TerminationGracePeriodSeconds: &grace,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
						RunAsUser:    &runAsUser,
						FSGroup:      &fsGroup,
					},
					Volumes: []corev1.Volume{
						{Name: "project-data", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "project-" + projectID},
						}},
						{Name: "nix-store", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "nix-" + projectID},
						}},
						{Name: "claude-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "user-config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "user-config"},
							},
						}},
						{Name: "user-secrets", VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "user-secrets"},
						}},
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
								{Name: "nix-store", MountPath: "/nix"},
								{Name: "claude-home", MountPath: "/home/node/.claude"},
								{Name: "user-config", MountPath: "/home/node/.claude-seed", ReadOnly: true},
								{Name: "user-secrets", MountPath: "/home/node/.user-secrets", ReadOnly: true},
							},
						},
					},
				},
			},
		},
	}

	if ownerErr := ctrlutil.SetControllerReference(mcp, deploy, r.scheme); ownerErr != nil {
		r.logger.Warn().Err(ownerErr).Msg("set controller ref on deployment")
	}

	if createErr := r.client.Create(ctx, deploy); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create deployment: %w", createErr)
	}
	return nil
}

// ensurePVCCR ensures a PVC exists in the user namespace.
func (r *MCProjectReconciler) ensurePVCCR(ctx context.Context, mcp *MCProject, ns, name, size, storageClass string) error {
	existing := &corev1.PersistentVolumeClaim{}
	err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, existing)
	if err == nil {
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get pvc %s: %w", name, err)
	}

	qty, parseErr := resource.ParseQuantity(size)
	if parseErr != nil {
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
	if ownerErr := ctrlutil.SetControllerReference(mcp, pvc, r.scheme); ownerErr != nil {
		r.logger.Warn().Err(ownerErr).Msg("set controller ref on pvc")
	}
	if createErr := r.client.Create(ctx, pvc); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create pvc %s: %w", name, createErr)
	}
	return nil
}

// ensureOwned creates the object if it doesn't exist, setting controller reference.
func (r *MCProjectReconciler) ensureOwned(ctx context.Context, mcp *MCProject, obj client.Object, create func() error) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	// Get into a fresh typed object to avoid "already set" issues with SetControllerReference.
	if err := r.client.Get(ctx, key, obj); err == nil {
		return nil
	} else if !k8serrors.IsNotFound(err) {
		return err
	}
	if err := ctrlutil.SetControllerReference(mcp, obj, r.scheme); err != nil {
		r.logger.Warn().Err(err).Str("name", obj.GetName()).Msg("set controller ref")
	}
	if err := create(); err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// loadTemplate reads the session-agent-template ConfigMap from the control-plane namespace.
// If the ConfigMap is absent (dev/test), returns safe defaults.
func (r *MCProjectReconciler) loadTemplate(ctx context.Context) (*sessionAgentTpl, error) {
	cmName := r.releaseName + "-session-agent-template"
	cm := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: r.controlPlaneNs}, cm); err != nil {
		if k8serrors.IsNotFound(err) {
			return defaultTemplate(), nil
		}
		return nil, fmt.Errorf("get configmap %s: %w", cmName, err)
	}

	tpl := &sessionAgentTpl{
		image:                  cm.Data["image"],
		imagePullPolicy:        corev1.PullPolicy(cm.Data["imagePullPolicy"]),
		projectPvcSize:         cm.Data["projectPvcSize"],
		projectPvcStorageClass: cm.Data["projectPvcStorageClass"],
		nixPvcSize:             cm.Data["nixPvcSize"],
		nixPvcStorageClass:     cm.Data["nixPvcStorageClass"],
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

// setPhase updates only the phase field of the MCProject status.
func (r *MCProjectReconciler) setPhase(ctx context.Context, mcp *MCProject, phase MCProjectPhase) error {
	mcp.Status.Phase = phase
	return r.client.Status().Update(ctx, mcp)
}

// updateCondition sets a condition based on whether an error occurred.
func (r *MCProjectReconciler) updateCondition(ctx context.Context, mcp *MCProject, condType string, err error) {
	status := metav1.ConditionTrue
	reason := "Reconciled"
	msg := ""
	if err != nil {
		status = metav1.ConditionFalse
		reason = "ReconcileError"
		msg = err.Error()
	}
	r.setCondition(ctx, mcp, condType, status, reason, msg)
}

// setCondition upserts a condition on the MCProject status.
func (r *MCProjectReconciler) setCondition(ctx context.Context, mcp *MCProject, condType string, status metav1.ConditionStatus, reason, message string) {
	cond := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&mcp.Status.Conditions, cond)
	r.client.Status().Update(ctx, mcp) //nolint:errcheck
}

// SetupWithManager registers the reconciler with the controller-runtime Manager.
// Watches MCProject CRs directly. Also watches Deployments, Secrets, ConfigMaps,
// and ServiceAccounts in user namespaces (via owner references) to detect drift.
func (r *MCProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueForOwner := handler.EnqueueRequestForOwner(
		mgr.GetScheme(),
		mgr.GetRESTMapper(),
		&MCProject{},
	)
	return ctrl.NewControllerManagedBy(mgr).
		For(&MCProject{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Watches(&corev1.Secret{}, enqueueForOwner).
		Watches(&corev1.ConfigMap{}, enqueueForOwner).
		Watches(&corev1.ServiceAccount{}, enqueueForOwner).
		Complete(r)
}

// CreateMCProject creates an MCProject CR in the given namespace.
// Used by the NATS projects.create handler and seedDev instead of calling
// ProvisionProject directly.
func CreateMCProject(ctx context.Context, c client.Client, namespace, userID, projectID, gitURL string) error {
	mcp := &MCProject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: SchemeGroupVersion.String(),
			Kind:       "MCProject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      projectID,
			Namespace: namespace,
		},
		Spec: MCProjectSpec{
			UserID:    userID,
			ProjectID: projectID,
			GitURL:    gitURL,
		},
	}
	if err := c.Create(ctx, mcp); err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create MCProject CR: %w", err)
	}
	return nil
}

// defaultTemplate returns a sessionAgentTpl with safe defaults for dev/test environments.
func defaultTemplate() *sessionAgentTpl {
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

// applyDefaultResources fills in resource requests/limits if not set.
func applyDefaultResources(tpl *sessionAgentTpl) {
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
}

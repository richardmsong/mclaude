// reconciler.go implements the controller-runtime reconciler for MCProject CRDs.
// Extracted from mclaude-control-plane per ADR-0035 (stage 5).
// The reconciler ensures K8s resources match the desired state in the MCProject spec.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mclnats "mclaude.io/common/pkg/nats"
)

// MCProjectReconciler reconciles MCProject CRs.
type MCProjectReconciler struct {
	client                 client.Client
	scheme                 *runtime.Scheme
	controlPlaneNs         string
	releaseName            string
	sessionAgentTemplateCM string
	sessionAgentNATSURL    string
	accountKP              nkeys.KeyPair
	devOAuthToken          string
	clusterSlug            string
	logger                 zerolog.Logger
}

// Reconcile is called whenever an MCProject CR changes.
func (r *MCProjectReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger.With().
		Str("namespace", req.Namespace).
		Str("name", req.Name).
		Logger()

	var mcp MCProject
	if err := r.client.Get(ctx, req.NamespacedName, &mcp); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get MCProject: %w", err)
	}

	// Gap 3: Transition through Pending before Provisioning.
	if mcp.Status.Phase == "" {
		if err := r.setPhase(ctx, &mcp, PhasePending); err != nil {
			log.Warn().Err(err).Msg("set phase Pending")
		}
		return reconcile.Result{Requeue: true}, nil
	}
	if mcp.Status.Phase == PhasePending {
		if err := r.setPhase(ctx, &mcp, PhaseProvisioning); err != nil {
			log.Warn().Err(err).Msg("set phase Provisioning")
		}
	}

	userNs := "mclaude-" + mcp.Spec.UserID
	log = log.With().Str("userNs", userNs).Str("projectId", mcp.Spec.ProjectID).Logger()

	tpl, err := r.loadTemplate(ctx)
	if err != nil {
		log.Error().Err(err).Msg("load session-agent template")
		r.setCondition(ctx, &mcp, string(ConditionDeploymentReady), metav1.ConditionFalse, "TemplateError", err.Error())
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	nsErr := r.reconcileNamespace(ctx, &mcp, userNs, tpl)
	r.updateCondition(ctx, &mcp, string(ConditionNamespaceReady), nsErr)
	if nsErr != nil {
		log.Error().Err(nsErr).Msg("ensure namespace")
		r.setPhase(ctx, &mcp, PhaseFailed) //nolint:errcheck
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	rbacErr := r.reconcileRBAC(ctx, &mcp, userNs)
	r.updateCondition(ctx, &mcp, string(ConditionRBACReady), rbacErr)
	if rbacErr != nil {
		log.Error().Err(rbacErr).Msg("ensure RBAC")
	}

	secretsErr := r.reconcileSecrets(ctx, &mcp, userNs)
	r.updateCondition(ctx, &mcp, string(ConditionSecretsReady), secretsErr)
	if secretsErr != nil {
		log.Error().Err(secretsErr).Msg("ensure secrets")
	}

	deployErr := r.reconcileDeployment(ctx, &mcp, userNs, tpl)
	r.updateCondition(ctx, &mcp, string(ConditionDeploymentReady), deployErr)
	if deployErr != nil {
		log.Error().Err(deployErr).Msg("ensure deployment")
	}

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

func (r *MCProjectReconciler) reconcileNamespace(ctx context.Context, mcp *MCProject, userNs string, tpl *sessionAgentTpl) error {
	ns := &corev1.Namespace{}
	err := r.client.Get(ctx, types.NamespacedName{Name: userNs}, ns)
	if err == nil {
		if ns.Labels == nil {
			ns.Labels = make(map[string]string)
		}
		ns.Labels["mclaude.io/user-id"] = mcp.Spec.UserID
		ns.Labels["mclaude.io/managed"] = "true"
		if tpl != nil && tpl.corporateCAEnabled {
			ns.Labels["mclaude.io/user-namespace"] = "true"
		}
		return r.client.Update(ctx, ns)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get namespace: %w", err)
	}
	labels := map[string]string{
		"mclaude.io/user-id": mcp.Spec.UserID,
		"mclaude.io/managed": "true",
	}
	if tpl != nil && tpl.corporateCAEnabled {
		labels["mclaude.io/user-namespace"] = "true"
	}
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: userNs, Labels: labels},
	}
	if createErr := r.client.Create(ctx, ns); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create namespace: %w", createErr)
	}
	return nil
}

func (r *MCProjectReconciler) reconcileRBAC(ctx context.Context, mcp *MCProject, userNs string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-sa", Namespace: userNs},
	}
	if err := r.ensureOwned(ctx, mcp, sa, func() error {
		return r.client.Create(ctx, sa)
	}); err != nil {
		return fmt.Errorf("serviceaccount: %w", err)
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: userNs},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{"user-config"}, Verbs: []string{"get", "watch", "patch"}},
			{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"user-secrets"}, Verbs: []string{"get"}},
		},
	}
	if err := r.ensureOwned(ctx, mcp, role, func() error {
		return r.client.Create(ctx, role)
	}); err != nil {
		return fmt.Errorf("role: %w", err)
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: userNs},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "mclaude-sa", Namespace: userNs}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "mclaude-role"},
	}
	if err := r.ensureOwned(ctx, mcp, rb, func() error {
		return r.client.Create(ctx, rb)
	}); err != nil {
		return fmt.Errorf("rolebinding: %w", err)
	}
	return nil
}

func (r *MCProjectReconciler) reconcileSecrets(ctx context.Context, mcp *MCProject, userNs string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "user-config", Namespace: userNs},
		Data:       map[string]string{},
	}
	if err := r.ensureOwned(ctx, mcp, cm, func() error {
		return r.client.Create(ctx, cm)
	}); err != nil {
		return fmt.Errorf("user-config configmap: %w", err)
	}

	existingSecret := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, existingSecret)
	if err == nil {
		needsUpdate := false
		if existingSecret.Data == nil {
			existingSecret.Data = make(map[string][]byte)
		}
		if len(existingSecret.Data["nats-creds"]) == 0 {
			jwtStr, seed, issueErr := IssueSessionAgentJWT(mcp.Spec.UserID, mcp.Spec.UserSlug, r.accountKP)
			if issueErr != nil {
				return fmt.Errorf("issue session-agent jwt: %w", issueErr)
			}
			existingSecret.Data["nats-creds"] = mclnats.FormatNATSCredentials(jwtStr, seed)
			needsUpdate = true
		}
		if r.devOAuthToken != "" && string(existingSecret.Data["oauth-token"]) != r.devOAuthToken {
			existingSecret.Data["oauth-token"] = []byte(r.devOAuthToken)
			needsUpdate = true
		}
		// Gap 8: Ensure this MCProject is an owner of user-secrets.
		if ownerErr := r.addOwnerIfMissing(ctx, mcp, existingSecret); ownerErr != nil {
			r.logger.Warn().Err(ownerErr).Msg("add owner to user-secrets")
		}
		if needsUpdate {
			if updateErr := r.client.Update(ctx, existingSecret); updateErr != nil {
				return fmt.Errorf("patch user-secrets: %w", updateErr)
			}
		}
	} else if k8serrors.IsNotFound(err) {
		jwtStr, seed, issueErr := IssueSessionAgentJWT(mcp.Spec.UserID, mcp.Spec.UserSlug, r.accountKP)
		if issueErr != nil {
			return fmt.Errorf("issue session-agent jwt: %w", issueErr)
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: userNs},
			Data:       map[string][]byte{"nats-creds": mclnats.FormatNATSCredentials(jwtStr, seed)},
		}
		if r.devOAuthToken != "" {
			secret.Data["oauth-token"] = []byte(r.devOAuthToken)
		}
		// Gap 8: Set this MCProject as owner of the new user-secrets Secret.
		if ownerErr := ctrlutil.SetOwnerReference(mcp, secret, r.scheme); ownerErr != nil {
			r.logger.Warn().Err(ownerErr).Msg("set owner on user-secrets")
		}
		if createErr := r.client.Create(ctx, secret); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
			return fmt.Errorf("create user-secrets: %w", createErr)
		}
	} else {
		return fmt.Errorf("get user-secrets: %w", err)
	}

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

// buildPodTemplate constructs the full PodTemplateSpec for the session-agent Deployment.
// Per ADR-0035: injects USER_SLUG, HOST_SLUG, PROJECT_SLUG env vars from the
// MCProject spec and the controller's configured cluster slug.
func (r *MCProjectReconciler) buildPodTemplate(ctx context.Context, mcp *MCProject, userNs string, tpl *sessionAgentTpl) corev1.PodTemplateSpec {
	projectID := mcp.Spec.ProjectID
	userID := mcp.Spec.UserID
	gitURL := mcp.Spec.GitURL
	gitIdentityID := mcp.Spec.GitIdentityID

	grace := tpl.terminationGracePeriodSeconds
	runAsUser := int64(1000)
	fsGroup := int64(1000)
	runAsNonRoot := true

	var imagePullSecrets []corev1.LocalObjectReference
	secretList := &corev1.SecretList{}
	if listErr := r.client.List(ctx, secretList, client.InNamespace(userNs)); listErr == nil {
		for _, s := range secretList.Items {
			if s.Type == corev1.SecretTypeDockerConfigJson {
				imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: s.Name})
			}
		}
	}

	env := []corev1.EnvVar{
		{Name: "USER_ID", Value: userID},
		{Name: "PROJECT_ID", Value: projectID},
		{Name: "NATS_URL", Value: r.sessionAgentNATSURL},
		// ADR-0035/ADR-0050: slug-based env vars for host-scoped subject construction.
		{Name: "USER_SLUG", Value: mcp.Spec.UserSlug},
		{Name: "HOST_SLUG", Value: tpl.hostSlug},
		{Name: "PROJECT_SLUG", Value: mcp.Spec.ProjectSlug},
		// Gap 4: CLAUDE_CODE_TMPDIR — persistent temp dir on the project-data volume.
		{Name: "CLAUDE_CODE_TMPDIR", Value: "/data/claude-tmp"},
	}
	if gitURL != "" {
		env = append(env, corev1.EnvVar{Name: "GIT_URL", Value: gitURL})
	}
	if gitIdentityID != "" {
		env = append(env, corev1.EnvVar{Name: "GIT_IDENTITY_ID", Value: gitIdentityID})
	}

	volumes := []corev1.Volume{
		{Name: "project-data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "project-" + projectID}}},
		{Name: "nix-store", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "nix-" + projectID}}},
		{Name: "claude-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "user-config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "user-config"}}}},
		{Name: "user-secrets", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "user-secrets"}}},
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "project-data", MountPath: "/data"},
		{Name: "nix-store", MountPath: "/nix"},
		{Name: "claude-home", MountPath: "/home/node/.claude"},
		{Name: "user-config", MountPath: "/home/node/.claude-seed", ReadOnly: true},
		{Name: "user-secrets", MountPath: "/home/node/.user-secrets", ReadOnly: true},
		// Gap 4: CLAUDE_CODE_TMPDIR — SubPath on project-data for persistent temp files.
		{Name: "project-data", MountPath: "/data/claude-tmp", SubPath: "claude-tmp"},
	}

	annotations := map[string]string{}

	if tpl.corporateCAEnabled && tpl.corporateCAConfigMapName != "" {
		caCM := &corev1.ConfigMap{}
		cmErr := r.client.Get(ctx, types.NamespacedName{Name: tpl.corporateCAConfigMapName, Namespace: userNs}, caCM)
		if cmErr == nil {
			volumes = append(volumes, corev1.Volume{
				Name: "corporate-ca",
				VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: tpl.corporateCAConfigMapName},
				}},
			})
			subPath := tpl.corporateCAConfigMapKey
			if subPath == "" {
				subPath = "ca-certificates.crt"
			}
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name: "corporate-ca", MountPath: "/etc/ssl/certs/corporate-ca-certificates.crt", SubPath: subPath, ReadOnly: true,
			})
			env = append(env, corev1.EnvVar{Name: "NODE_EXTRA_CA_CERTS", Value: "/etc/ssl/certs/corporate-ca-certificates.crt"})
			annotations["mclaude.io/ca-bundle-hash"] = reconcilerCAConfigMapHash(caCM)
		}
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"app": "mclaude-project", "project": projectID},
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:            "mclaude-sa",
			ImagePullSecrets:              imagePullSecrets,
			TerminationGracePeriodSeconds: &grace,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &runAsNonRoot, RunAsUser: &runAsUser, FSGroup: &fsGroup,
			},
			Volumes: volumes,
			Containers: []corev1.Container{{
				Name: "session-agent", Image: tpl.image, ImagePullPolicy: tpl.imagePullPolicy,
				Env: env, Resources: tpl.resources, VolumeMounts: volumeMounts,
			}},
		},
	}
}

func reconcilerCAConfigMapHash(cm *corev1.ConfigMap) string {
	h := sha256.New()
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

func (r *MCProjectReconciler) reconcileDeployment(ctx context.Context, mcp *MCProject, userNs string, tpl *sessionAgentTpl) error {
	projectID := mcp.Spec.ProjectID

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
		existing.Spec.Template = r.buildPodTemplate(ctx, mcp, userNs, tpl)
		existing.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		return r.client.Update(ctx, existing)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get deployment: %w", err)
	}

	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: deployName, Namespace: userNs,
			Labels: map[string]string{"app": "mclaude-project", "project": projectID},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mclaude-project", "project": projectID}},
			Template: r.buildPodTemplate(ctx, mcp, userNs, tpl),
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
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: qty}},
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

func (r *MCProjectReconciler) ensureOwned(ctx context.Context, mcp *MCProject, obj client.Object, create func() error) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	if err := r.client.Get(ctx, key, obj); err == nil {
		// Gap 7: Resource exists — add this MCProject as an additional owner if not already present.
		return r.addOwnerIfMissing(ctx, mcp, obj)
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

// addOwnerIfMissing adds the MCProject as an owner reference if it is not already present.
// Uses SetOwnerReference (non-controller) to allow multiple owners on shared resources.
func (r *MCProjectReconciler) addOwnerIfMissing(ctx context.Context, mcp *MCProject, obj client.Object) error {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == mcp.UID {
			return nil // already an owner
		}
	}
	if err := ctrlutil.SetOwnerReference(mcp, obj, r.scheme); err != nil {
		r.logger.Warn().Err(err).Str("name", obj.GetName()).Msg("add owner ref")
		return nil // non-fatal
	}
	return r.client.Update(ctx, obj)
}

func (r *MCProjectReconciler) loadTemplate(ctx context.Context) (*sessionAgentTpl, error) {
	cmName := r.sessionAgentTemplateCM
	cm := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: r.controlPlaneNs}, cm); err != nil {
		if k8serrors.IsNotFound(err) {
			tpl := defaultTemplate()
			tpl.hostSlug = r.clusterSlug
			return tpl, nil
		}
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
	if tpl.corporateCAConfigMapKey == "" {
		tpl.corporateCAConfigMapKey = "ca-certificates.crt"
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
	tpl.hostSlug = r.clusterSlug
	return tpl, nil
}

func (r *MCProjectReconciler) setPhase(ctx context.Context, mcp *MCProject, phase MCProjectPhase) error {
	mcp.Status.Phase = phase
	return r.client.Status().Update(ctx, mcp)
}

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

func (r *MCProjectReconciler) setCondition(ctx context.Context, mcp *MCProject, condType string, status metav1.ConditionStatus, reason, message string) {
	cond := metav1.Condition{
		Type: condType, Status: status, Reason: reason, Message: message,
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&mcp.Status.Conditions, cond)
	r.client.Status().Update(ctx, mcp) //nolint:errcheck
}

// SetupWithManager registers the reconciler with the controller-runtime Manager.
func (r *MCProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueForOwner := handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &MCProject{})

	templateNs := r.controlPlaneNs
	templateCMName := r.sessionAgentTemplateCM

	return ctrl.NewControllerManagedBy(mgr).
		For(&MCProject{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Watches(&corev1.Secret{}, enqueueForOwner).
		Watches(&corev1.ConfigMap{}, enqueueForOwner).
		Watches(&corev1.ServiceAccount{}, enqueueForOwner).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				if obj.GetName() != templateCMName || obj.GetNamespace() != templateNs {
					return nil
				}
				var mcpList MCProjectList
				if err := r.client.List(ctx, &mcpList); err != nil {
					return nil
				}
				reqs := make([]reconcile.Request, 0, len(mcpList.Items))
				for _, mcp := range mcpList.Items {
					reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: mcp.Name, Namespace: mcp.Namespace}})
				}
				return reqs
			}),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == templateCMName && obj.GetNamespace() == templateNs
			})),
		).
		Complete(r)
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
	corporateCAEnabled            bool
	corporateCAConfigMapName      string
	corporateCAConfigMapKey       string
	// ADR-0035: cluster slug configured at deploy time (Helm value clusterSlug).
	hostSlug string
}

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

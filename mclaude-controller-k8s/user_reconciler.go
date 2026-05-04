// user_reconciler.go implements the per-user provisioning reconciler for ADR-0091.
//
// On each reception of mclaude.hosts.{hslug}.grants.create, this reconciler ensures:
//  1. Namespace mclaude-{uslug}
//  2. ServiceAccounts + Roles + RoleBindings for mclaude-sa and mclaude-user-agent
//  3. user-secrets Secret (CLI credential helpers only — no token field)
//  4. user-config ConfigMap
//  5. session-agent-template ConfigMap copied from mclaude-system
//  6. user-agent-key Secret (NKey seed written by controller; idempotent)
//  7. user-tokens-{uslug} Secret (empty; populated by user-agent after token delivery)
//  8. user-agent Deployment
//
// All steps are idempotent — re-publishing the same grant is safe.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// natsConn is the subset of *nats.Conn used by the reconciler.
// Defined as an interface to allow injection of a fake in tests.
type natsConn interface {
	Request(subj string, data []byte, timeout time.Duration) (*nats.Msg, error)
}

// UserReconciler provisions the per-user Kubernetes resources described in ADR-0091.
// It is triggered by messages on mclaude.hosts.{hslug}.grants.create.
type UserReconciler struct {
	client         client.Client
	controlPlaneNs string // namespace where the controller runs (mclaude-system)
	releaseName    string // Helm release name, used to locate session-agent-template CM
	hslug          string // host slug, decoded from the host JWT at boot
	controlPlaneURL string
	hubNATSURL     string
	nc             natsConn
	logger         zerolog.Logger
}

// GrantCreatePayload is the NATS payload for mclaude.hosts.{hslug}.grants.create.
// The uslug field identifies which user the host is granting access to.
type GrantCreatePayload struct {
	USlug string `json:"uslug"`
}

// userAgentRegisterRequest is the NATS payload for
// mclaude.hosts.{hslug}.api.agents.user-agent.register.
type userAgentRegisterRequest struct {
	USlug      string `json:"uslug"`
	NKeyPublic string `json:"nkey_public"`
}

// userAgentRegisterReply is the NATS reply from CP.
type userAgentRegisterReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ReconcileUser runs the full per-user provisioning loop for the given uslug.
// Idempotent: safe to call multiple times for the same user.
func (r *UserReconciler) ReconcileUser(ctx context.Context, uslug string) error {
	log := r.logger.With().Str("uslug", uslug).Logger()
	userNs := "mclaude-" + uslug

	// Step 1: Load session-agent-template from mclaude-system.
	tpl := r.loadUserAgentTemplate(ctx)

	// Step 2: Ensure namespace mclaude-{uslug}.
	if err := r.ensureNamespace(ctx, userNs, tpl); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	log.Info().Str("namespace", userNs).Msg("namespace ensured")

	// Step 3: Ensure ServiceAccounts, Roles, RoleBindings.
	if err := r.ensureRBAC(ctx, userNs, uslug); err != nil {
		return fmt.Errorf("ensure RBAC: %w", err)
	}
	log.Info().Msg("RBAC ensured")

	// Step 4: Ensure user-secrets Secret (CLI credential helpers only).
	if err := r.ensureUserSecrets(ctx, userNs); err != nil {
		return fmt.Errorf("ensure user-secrets: %w", err)
	}
	log.Info().Msg("user-secrets ensured")

	// Step 5: Ensure user-config ConfigMap.
	if err := r.ensureUserConfig(ctx, userNs); err != nil {
		return fmt.Errorf("ensure user-config: %w", err)
	}
	log.Info().Msg("user-config ensured")

	// Step 5b: Copy session-agent-template from mclaude-system into the user namespace.
	if err := r.ensureSessionAgentTemplate(ctx, userNs); err != nil {
		return fmt.Errorf("ensure session-agent-template: %w", err)
	}
	log.Info().Msg("session-agent-template copied")

	// Step 6: Ensure user-agent-key Secret (generate NKey, register pubkey with CP).
	if err := r.ensureUserAgentKey(ctx, userNs, uslug); err != nil {
		return fmt.Errorf("ensure user-agent-key: %w", err)
	}
	log.Info().Msg("user-agent-key ensured")

	// Step 6b: Ensure empty user-tokens-{uslug} Secret.
	if err := r.ensureUserTokensSecret(ctx, userNs, uslug); err != nil {
		return fmt.Errorf("ensure user-tokens secret: %w", err)
	}
	log.Info().Msg("user-tokens secret ensured")

	// Step 7: Copy imagePullSecrets from controller namespace.
	if err := r.copyImagePullSecrets(ctx, userNs); err != nil {
		return fmt.Errorf("copy imagePullSecrets: %w", err)
	}

	// Step 8: Ensure user-agent Deployment.
	if err := r.ensureUserAgentDeployment(ctx, userNs, uslug, tpl); err != nil {
		return fmt.Errorf("ensure user-agent deployment: %w", err)
	}
	log.Info().Msg("user-agent deployment ensured")

	log.Info().Msg("per-user provisioning complete")
	return nil
}

// userAgentTpl holds values read from the {release}-user-agent-template ConfigMap.
type userAgentTpl struct {
	image           string
	imagePullPolicy corev1.PullPolicy
	corporateCAEnabled bool
	resources       corev1.ResourceRequirements
}

// loadUserAgentTemplate reads the {release}-user-agent-template ConfigMap.
// Falls back to defaults if the ConfigMap is not found.
func (r *UserReconciler) loadUserAgentTemplate(ctx context.Context) *userAgentTpl {
	cmName := r.releaseName + "-user-agent-template"
	cm := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: r.controlPlaneNs}, cm); err != nil {
		return defaultUserAgentTemplate()
	}

	tpl := &userAgentTpl{
		image:           cm.Data["image"],
		imagePullPolicy: corev1.PullPolicy(cm.Data["imagePullPolicy"]),
	}
	tpl.corporateCAEnabled = cm.Data["corporateCAEnabled"] == "true"

	if tpl.image == "" {
		tpl.image = "mclaude-user-agent:latest"
	}
	if tpl.imagePullPolicy == "" {
		tpl.imagePullPolicy = corev1.PullIfNotPresent
	}

	if v := cm.Data["resourcesJson"]; v != "" {
		_ = json.Unmarshal([]byte(v), &tpl.resources)
	}
	applyDefaultUserAgentResources(tpl)
	return tpl
}

func defaultUserAgentTemplate() *userAgentTpl {
	tpl := &userAgentTpl{
		image:           "mclaude-user-agent:latest",
		imagePullPolicy: corev1.PullIfNotPresent,
	}
	applyDefaultUserAgentResources(tpl)
	return tpl
}

func applyDefaultUserAgentResources(tpl *userAgentTpl) {
	if tpl.resources.Requests == nil {
		tpl.resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		}
	}
	if tpl.resources.Limits == nil {
		tpl.resources.Limits = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		}
	}
}

// ensureNamespace creates or updates namespace mclaude-{uslug}.
func (r *UserReconciler) ensureNamespace(ctx context.Context, userNs string, tpl *userAgentTpl) error {
	ns := &corev1.Namespace{}
	err := r.client.Get(ctx, types.NamespacedName{Name: userNs}, ns)
	if err == nil {
		if ns.Labels == nil {
			ns.Labels = make(map[string]string)
		}
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
		"mclaude.io/managed": "true",
	}
	if tpl != nil && tpl.corporateCAEnabled {
		labels["mclaude.io/user-namespace"] = "true"
	}
	newNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: userNs, Labels: labels},
	}
	if createErr := r.client.Create(ctx, newNs); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create namespace: %w", createErr)
	}
	return nil
}

// ensureRBAC creates/updates both service accounts and their RBAC:
//   - mclaude-sa: used by future project pods (basic access to user-config and user-secrets)
//   - mclaude-user-agent: used by the user-agent Deployment (Deployment, PVC, Secret, ConfigMap access)
func (r *UserReconciler) ensureRBAC(ctx context.Context, userNs, uslug string) error {
	// --- mclaude-sa (for project pods) ---
	if err := r.ensureServiceAccount(ctx, "mclaude-sa", userNs); err != nil {
		return fmt.Errorf("mclaude-sa serviceaccount: %w", err)
	}

	mcladeSARole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-role", Namespace: userNs},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{"user-config"}, Verbs: []string{"get", "watch", "patch"}},
			{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"user-secrets"}, Verbs: []string{"get"}},
		},
	}
	if err := r.ensureRole(ctx, mcladeSARole); err != nil {
		return fmt.Errorf("mclaude-role role: %w", err)
	}
	if err := r.ensureRoleBinding(ctx, "mclaude-role", userNs, "mclaude-sa", "mclaude-role"); err != nil {
		return fmt.Errorf("mclaude-role rolebinding: %w", err)
	}

	// --- mclaude-user-agent (for the user-agent Deployment) ---
	if err := r.ensureServiceAccount(ctx, "mclaude-user-agent", userNs); err != nil {
		return fmt.Errorf("mclaude-user-agent serviceaccount: %w", err)
	}

	userTokensSecretName := "user-tokens-" + uslug
	userAgentRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "mclaude-user-agent-role", Namespace: userNs},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{userTokensSecretName},
				Verbs:         []string{"get", "update", "patch"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{"session-agent-template"},
				Verbs:         []string{"get"},
			},
		},
	}
	if err := r.ensureRole(ctx, userAgentRole); err != nil {
		return fmt.Errorf("mclaude-user-agent-role role: %w", err)
	}
	if err := r.ensureRoleBinding(ctx, "mclaude-user-agent-role", userNs, "mclaude-user-agent", "mclaude-user-agent-role"); err != nil {
		return fmt.Errorf("mclaude-user-agent-role rolebinding: %w", err)
	}

	return nil
}

func (r *UserReconciler) ensureServiceAccount(ctx context.Context, name, namespace string) error {
	sa := &corev1.ServiceAccount{}
	err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sa)
	if err == nil {
		return nil // already exists
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get serviceaccount %s: %w", name, err)
	}
	newSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if createErr := r.client.Create(ctx, newSA); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create serviceaccount %s: %w", name, createErr)
	}
	return nil
}

func (r *UserReconciler) ensureRole(ctx context.Context, role *rbacv1.Role) error {
	existing := &rbacv1.Role{}
	err := r.client.Get(ctx, types.NamespacedName{Name: role.Name, Namespace: role.Namespace}, existing)
	if err == nil {
		existing.Rules = role.Rules
		return r.client.Update(ctx, existing)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get role %s: %w", role.Name, err)
	}
	if createErr := r.client.Create(ctx, role); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create role %s: %w", role.Name, createErr)
	}
	return nil
}

func (r *UserReconciler) ensureRoleBinding(ctx context.Context, name, namespace, saName, roleName string) error {
	existing := &rbacv1.RoleBinding{}
	err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, existing)
	if err == nil {
		return nil // already exists, idempotent
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get rolebinding %s: %w", name, err)
	}
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: namespace}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: roleName},
	}
	if createErr := r.client.Create(ctx, rb); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create rolebinding %s: %w", name, createErr)
	}
	return nil
}

// ensureUserSecrets creates the user-secrets Secret with empty data (CLI credential helpers only).
// Per ADR-0091: no oauth-token field; no DEV_OAUTH_TOKEN.
func (r *UserReconciler) ensureUserSecrets(ctx context.Context, userNs string) error {
	existing := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "user-secrets", Namespace: userNs}, existing)
	if err == nil {
		// Secret exists. Remove stale oauth-token or nats-creds if present from prior controller versions.
		needsUpdate := false
		if existing.Data == nil {
			existing.Data = make(map[string][]byte)
		}
		for _, staleKey := range []string{"oauth-token", "nats-creds"} {
			if _, has := existing.Data[staleKey]; has {
				delete(existing.Data, staleKey)
				needsUpdate = true
			}
		}
		if needsUpdate {
			return r.client.Update(ctx, existing)
		}
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get user-secrets: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-secrets", Namespace: userNs},
		Data:       map[string][]byte{},
	}
	if createErr := r.client.Create(ctx, secret); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create user-secrets: %w", createErr)
	}
	return nil
}

// ensureUserConfig creates the user-config ConfigMap if absent.
func (r *UserReconciler) ensureUserConfig(ctx context.Context, userNs string) error {
	existing := &corev1.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "user-config", Namespace: userNs}, existing)
	if err == nil {
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get user-config: %w", err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "user-config", Namespace: userNs},
		Data:       map[string]string{},
	}
	if createErr := r.client.Create(ctx, cm); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create user-config: %w", createErr)
	}
	return nil
}

// ensureSessionAgentTemplate copies {release}-session-agent-template from mclaude-system
// into the user namespace as session-agent-template. On changes (Helm upgrade), re-copies.
func (r *UserReconciler) ensureSessionAgentTemplate(ctx context.Context, userNs string) error {
	sourceName := r.releaseName + "-session-agent-template"
	src := &corev1.ConfigMap{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: sourceName, Namespace: r.controlPlaneNs}, src); err != nil {
		if k8serrors.IsNotFound(err) {
			// Source template doesn't exist — skip. Not an error; may not be deployed yet.
			return nil
		}
		return fmt.Errorf("get source session-agent-template: %w", err)
	}

	dest := &corev1.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "session-agent-template", Namespace: userNs}, dest)
	if err == nil {
		// Update if data changed.
		if configMapDataEqual(dest.Data, src.Data) && configMapBinaryDataEqual(dest.BinaryData, src.BinaryData) {
			return nil
		}
		dest.Data = src.Data
		dest.BinaryData = src.BinaryData
		return r.client.Update(ctx, dest)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get dest session-agent-template: %w", err)
	}

	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "session-agent-template", Namespace: userNs},
		Data:       src.Data,
		BinaryData: src.BinaryData,
	}
	if createErr := r.client.Create(ctx, newCM); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create session-agent-template: %w", createErr)
	}
	return nil
}

// configMapDataEqual returns true if two ConfigMap string data maps are equal.
func configMapDataEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// configMapBinaryDataEqual returns true if two ConfigMap binary data maps are equal.
func configMapBinaryDataEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || len(bv) != len(v) {
			return false
		}
		for i := range v {
			if v[i] != bv[i] {
				return false
			}
		}
	}
	return true
}

// ensureUserAgentKey implements Reconciler Loop step 6:
// If user-agent-key Secret is absent or missing nkey-seed, generates a new NKey,
// writes the decorated seed, and registers the pubkey with CP via NATS.
// Idempotent: if the seed already exists, skips re-generation and re-registration.
func (r *UserReconciler) ensureUserAgentKey(ctx context.Context, userNs, uslug string) error {
	existing := &corev1.Secret{}
	getErr := r.client.Get(ctx, types.NamespacedName{Name: "user-agent-key", Namespace: userNs}, existing)
	secretExists := getErr == nil
	if getErr != nil && !k8serrors.IsNotFound(getErr) {
		return fmt.Errorf("get user-agent-key: %w", getErr)
	}

	if secretExists && len(existing.Data["nkey-seed"]) > 0 {
		// Seed already present — idempotent, skip generation and registration.
		return nil
	}

	// Generate a new NKey user pair for the user-agent.
	kp, err := nkeys.CreateUser()
	if err != nil {
		return fmt.Errorf("generate user-agent NKey: %w", err)
	}
	pubKey, err := kp.PublicKey()
	if err != nil {
		return fmt.Errorf("get user-agent NKey public key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return fmt.Errorf("get user-agent NKey seed: %w", err)
	}

	// Register the user-agent's public key with CP via NATS.
	// nc is nil in unit tests — skip the NATS call in that case.
	if r.nc != nil {
		if regErr := r.registerUserAgentNKey(ctx, uslug, pubKey); regErr != nil {
			return regErr
		}
	}

	if !secretExists {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "user-agent-key", Namespace: userNs},
			Data: map[string][]byte{
				"nkey-seed": seed,
			},
		}
		if createErr := r.client.Create(ctx, secret); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
			return fmt.Errorf("create user-agent-key: %w", createErr)
		}
	} else {
		if existing.Data == nil {
			existing.Data = make(map[string][]byte)
		}
		existing.Data["nkey-seed"] = seed
		if updateErr := r.client.Update(ctx, existing); updateErr != nil {
			return fmt.Errorf("update user-agent-key: %w", updateErr)
		}
	}

	r.logger.Info().
		Str("uslug", uslug).
		Str("pubKey", pubKey).
		Msg("user-agent NKey generated and registered")
	return nil
}

// registerUserAgentNKey sends the NATS request to register the user-agent's public key
// with the control-plane.
// Subject: mclaude.hosts.{hslug}.api.agents.user-agent.register
// Payload: {uslug, nkey_public}
func (r *UserReconciler) registerUserAgentNKey(ctx context.Context, uslug, pubKey string) error {
	subject := fmt.Sprintf("mclaude.hosts.%s.api.agents.user-agent.register", r.hslug)
	payload, err := json.Marshal(userAgentRegisterRequest{
		USlug:      uslug,
		NKeyPublic: pubKey,
	})
	if err != nil {
		return fmt.Errorf("marshal user-agent register payload: %w", err)
	}

	reply, err := r.nc.Request(subject, payload, 10*time.Second)
	if err != nil {
		return fmt.Errorf("NATS user-agent.register request: %w", err)
	}

	var resp userAgentRegisterReply
	if unmarshalErr := json.Unmarshal(reply.Data, &resp); unmarshalErr != nil {
		return fmt.Errorf("unmarshal user-agent.register reply: %w", unmarshalErr)
	}
	if !resp.OK {
		return fmt.Errorf("user-agent.register failed: %s", resp.Error)
	}
	return nil
}

// ensureUserTokensSecret creates an empty user-tokens-{uslug} Secret if absent.
// The user-agent populates per-provider keys after the first successful token delivery.
// The controller never writes token material here.
func (r *UserReconciler) ensureUserTokensSecret(ctx context.Context, userNs, uslug string) error {
	secretName := "user-tokens-" + uslug
	existing := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: userNs}, existing)
	if err == nil {
		// Already exists (with or without populated keys) — idempotent, skip.
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get user-tokens secret: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: userNs},
		// No data keys — user-agent populates after first token delivery.
	}
	if createErr := r.client.Create(ctx, secret); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create user-tokens secret: %w", createErr)
	}
	return nil
}

// copyImagePullSecrets copies kubernetes.io/dockerconfigjson Secrets from the controller
// namespace into the user namespace so the user-agent pod can pull its image.
func (r *UserReconciler) copyImagePullSecrets(ctx context.Context, userNs string) error {
	secretList := &corev1.SecretList{}
	if err := r.client.List(ctx, secretList, client.InNamespace(r.controlPlaneNs)); err != nil {
		return fmt.Errorf("list secrets in %s: %w", r.controlPlaneNs, err)
	}
	for i := range secretList.Items {
		src := &secretList.Items[i]
		if src.Type != corev1.SecretTypeDockerConfigJson {
			continue
		}
		dest := &corev1.Secret{}
		if getErr := r.client.Get(ctx, types.NamespacedName{Name: src.Name, Namespace: userNs}, dest); getErr == nil {
			continue // already exists
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

// ensureUserAgentDeployment creates or updates the user-agent Deployment in the user namespace.
// The Deployment uses a single replica with RollingUpdate strategy.
// Mounts: user-agent-key at /secrets/agent/, user-tokens-{uslug} at /secrets/tokens/.
// Env: USER_SLUG, HOST_SLUG, NAMESPACE, NATS_URL, CONTROL_PLANE_URL.
func (r *UserReconciler) ensureUserAgentDeployment(ctx context.Context, userNs, uslug string, tpl *userAgentTpl) error {
	replicas := int32(1)
	userTokensSecretName := "user-tokens-" + uslug

	// Collect imagePullSecrets from the user namespace.
	var imagePullSecrets []corev1.LocalObjectReference
	secretList := &corev1.SecretList{}
	if err := r.client.List(ctx, secretList, client.InNamespace(userNs)); err == nil {
		for _, s := range secretList.Items {
			if s.Type == corev1.SecretTypeDockerConfigJson {
				imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: s.Name})
			}
		}
	}

	podTpl := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app":     "mclaude-user-agent",
				"user":    uslug,
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "mclaude-user-agent",
			ImagePullSecrets:   imagePullSecrets,
			Volumes: []corev1.Volume{
				{
					Name: "agent-key",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: "user-agent-key"},
					},
				},
				{
					Name: "user-tokens",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: userTokensSecretName},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:            "user-agent",
					Image:           tpl.image,
					ImagePullPolicy: tpl.imagePullPolicy,
					Resources:       tpl.resources,
					Env: []corev1.EnvVar{
						{Name: "USER_SLUG", Value: uslug},
						{Name: "HOST_SLUG", Value: r.hslug},
						{Name: "NAMESPACE", Value: userNs},
						{Name: "NATS_URL", Value: r.hubNATSURL},
						{Name: "CONTROL_PLANE_URL", Value: r.controlPlaneURL},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "agent-key", MountPath: "/secrets/agent/", ReadOnly: true},
						{Name: "user-tokens", MountPath: "/secrets/tokens/", ReadOnly: false},
					},
				},
			},
		},
	}

	existing := &appsv1.Deployment{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "user-agent", Namespace: userNs}, existing)
	if err == nil {
		// Update the pod template if needed.
		existing.Spec.Template = podTpl
		return r.client.Update(ctx, existing)
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get user-agent deployment: %w", err)
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-agent",
			Namespace: userNs,
			Labels:    map[string]string{"app": "mclaude-user-agent", "user": uslug},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "mclaude-user-agent", "user": uslug},
			},
			Template: podTpl,
		},
	}

	if createErr := r.client.Create(ctx, deploy); createErr != nil && !k8serrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create user-agent deployment: %w", createErr)
	}
	return nil
}

// ReconcileSecretsForUser re-reconciles user-secrets for the given uslug.
// Called by the users.*.projects.*.update handler for OAuth token rotation.
func (r *UserReconciler) ReconcileSecretsForUser(ctx context.Context, uslug string) error {
	userNs := "mclaude-" + uslug
	return r.ensureUserSecrets(ctx, userNs)
}

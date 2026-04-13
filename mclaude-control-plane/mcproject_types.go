// mcproject_types.go defines the MCProject Custom Resource Definition (CRD) types.
// MCProject is the spec-driven representation of a project in Kubernetes.
// The reconciler reads MCProject CRs and ensures K8s resources match the spec.
//
// Group: mclaude.io  Version: v1alpha1  Kind: MCProject
package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion is the group/version for MCProject CRs.
var SchemeGroupVersion = k8sschema.GroupVersion{
	Group:   "mclaude.io",
	Version: "v1alpha1",
}

// MCProjectGVK is the GroupVersionKind for MCProject.
var MCProjectGVK = SchemeGroupVersion.WithKind("MCProject")

// AddToScheme registers MCProject types into the given runtime.Scheme.
// Called during Manager setup.
func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&MCProject{},
		&MCProjectList{},
	)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
}

// MCProjectSpec defines the desired state of an MCProject.
type MCProjectSpec struct {
	// UserID is the mclaude user ID that owns this project.
	UserID string `json:"userId"`
	// ProjectID is the stable project identifier (matches the Postgres row).
	ProjectID string `json:"projectId"`
	// GitURL is an optional git remote for the project repository.
	// Empty for scratch (no-repo) projects.
	GitURL string `json:"gitUrl,omitempty"`
}

// MCProjectConditionType is a well-known condition name for MCProject status.
type MCProjectConditionType string

const (
	// ConditionNamespaceReady indicates the user namespace was created/confirmed.
	ConditionNamespaceReady MCProjectConditionType = "NamespaceReady"
	// ConditionRBACReady indicates ServiceAccount, Role, and RoleBinding exist.
	ConditionRBACReady MCProjectConditionType = "RBACReady"
	// ConditionSecretsReady indicates user-secrets (NATS creds) exist.
	ConditionSecretsReady MCProjectConditionType = "SecretsReady"
	// ConditionDeploymentReady indicates the session-agent Deployment exists.
	ConditionDeploymentReady MCProjectConditionType = "DeploymentReady"
)

// MCProjectPhase is the high-level provisioning phase.
type MCProjectPhase string

const (
	PhasePending      MCProjectPhase = "Pending"
	PhaseProvisioning MCProjectPhase = "Provisioning"
	PhaseReady        MCProjectPhase = "Ready"
	PhaseFailed       MCProjectPhase = "Failed"
)

// MCProjectStatus is the observed state of an MCProject.
type MCProjectStatus struct {
	// Phase is the high-level provisioning phase: Pending, Provisioning, Ready, Failed.
	Phase MCProjectPhase `json:"phase,omitempty"`
	// UserNamespace is the K8s namespace created for this user (mclaude-{userId}).
	UserNamespace string `json:"userNamespace,omitempty"`
	// Conditions are the detailed status conditions (NamespaceReady, RBACReady, etc.).
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// LastReconciledAt is the timestamp of the last successful reconcile.
	LastReconciledAt *metav1.Time `json:"lastReconciledAt,omitempty"`
}

// MCProject is a Kubernetes Custom Resource that represents a provisioned project.
// The reconciler ensures K8s resources (namespace, RBAC, secrets, PVCs, Deployment)
// match the desired state declared in the spec.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=mcp
type MCProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MCProjectSpec   `json:"spec,omitempty"`
	Status MCProjectStatus `json:"status,omitempty"`
}

// DeepCopyObject implements runtime.Object.
func (m *MCProject) DeepCopyObject() runtime.Object {
	if m == nil {
		return nil
	}
	out := new(MCProject)
	*out = *m
	out.TypeMeta = m.TypeMeta
	out.ObjectMeta = *m.ObjectMeta.DeepCopy()
	out.Spec = m.Spec
	if m.Status.LastReconciledAt != nil {
		t := *m.Status.LastReconciledAt
		out.Status.LastReconciledAt = &t
	}
	if m.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(m.Status.Conditions))
		copy(out.Status.Conditions, m.Status.Conditions)
	}
	out.Status.Phase = m.Status.Phase
	out.Status.UserNamespace = m.Status.UserNamespace
	return out
}

// MCProjectList is a list of MCProject resources.
//
// +kubebuilder:object:root=true
type MCProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MCProject `json:"items"`
}

// DeepCopyObject implements runtime.Object.
func (m *MCProjectList) DeepCopyObject() runtime.Object {
	if m == nil {
		return nil
	}
	out := new(MCProjectList)
	out.TypeMeta = m.TypeMeta
	out.ListMeta = m.ListMeta
	out.Items = make([]MCProject, len(m.Items))
	for i := range m.Items {
		obj := m.Items[i].DeepCopyObject().(*MCProject)
		out.Items[i] = *obj
	}
	return out
}

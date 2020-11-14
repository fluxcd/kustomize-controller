/*
Copyright 2020 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/dependency"
)

const (
	KustomizationKind         = "Kustomization"
	KustomizationFinalizer    = "finalizers.fluxcd.io"
	MaxConditionMessageLength = 4000
)

// KustomizationSpec defines the desired state of a kustomization.
type KustomizationSpec struct {
	// DependsOn may contain a dependency.CrossNamespaceDependencyReference slice
	// with references to Kustomization resources that must be ready before this
	// Kustomization can be reconciled.
	// +optional
	DependsOn []dependency.CrossNamespaceDependencyReference `json:"dependsOn,omitempty"`

	// Decrypt Kubernetes secrets before applying them on the cluster.
	// +optional
	Decryption *Decryption `json:"decryption,omitempty"`

	// The interval at which to reconcile the kustomization.
	// +required
	Interval metav1.Duration `json:"interval"`

	// The KubeConfig for reconciling the Kustomization on a remote cluster.
	// +optional
	KubeConfig *KubeConfig `json:"kubeConfig,omitempty"`

	// Path to the directory containing the kustomization file.
	// +kubebuilder:validation:Pattern="^\\./"
	// +required
	Path string `json:"path"`

	// Path to the directory containing the plugins.
	// +kubebuilder:validation:Pattern="^\\./"
	// +optional
	PluginHome string `json:"pluginHome,omitempty"`

	// Prune enables garbage collection.
	// +required
	Prune bool `json:"prune"`

	// A list of resources to be included in the health assessment.
	// +optional
	HealthChecks []CrossNamespaceObjectReference `json:"healthChecks,omitempty"`

	// The Kubernetes service account used for applying the kustomization.
	// +optional
	ServiceAccount *ServiceAccount `json:"serviceAccount,omitempty"`

	// Reference of the source where the kustomization file is.
	// +required
	SourceRef CrossNamespaceSourceReference `json:"sourceRef"`

	// This flag tells the controller to suspend subsequent kustomize executions,
	// it does not apply to already started executions. Defaults to false.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// TargetNamespace sets or overrides the namespace in the
	// kustomization.yaml file.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Optional
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// Timeout for validation, apply and health checking operations.
	// Defaults to 'Interval' duration.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Validate the Kubernetes objects before applying them on the cluster.
	// The validation strategy can be 'client' (local dry-run), 'server' (APIServer dry-run) or 'none'.
	// +kubebuilder:validation:Enum=none;client;server
	// +optional
	Validation string `json:"validation,omitempty"`
}

// ServiceAccount defines a reference to a Kubernetes service account.
type ServiceAccount struct {
	// Name is the name of the service account being referenced.
	// +required
	Name string `json:"name"`

	// Namespace is the namespace of the service account being referenced.
	// +required
	Namespace string `json:"namespace"`
}

// Decryption defines how decryption is handled for Kubernetes manifests.
type Decryption struct {
	// Provider is the name of the decryption engine.
	// +kubebuilder:validation:Enum=sops
	// +required
	Provider string `json:"provider"`

	// The secret name containing the private OpenPGP keys used for decryption.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// KubeConfig references a Kubernetes secret that contains a kubeconfig file.
type KubeConfig struct {
	// SecretRef holds the name to a secret that contains a 'value' key with
	// the kubeconfig file as the value. It must be in the same namespace as
	// the Kustomization.
	// It is recommended that the kubeconfig is self-contained, and the secret
	// is regularly updated if credentials such as a cloud-access-token expire.
	// Cloud specific `cmd-path` auth helpers will not function without adding
	// binaries and credentials to the Pod that is responsible for reconciling
	// the Kustomization.
	// +required
	SecretRef corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// KustomizationStatus defines the observed state of a kustomization.
type KustomizationStatus struct {
	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	Conditions []meta.Condition `json:"conditions,omitempty"`

	// The last successfully applied revision.
	// The revision format for Git sources is <branch|tag>/<commit-sha>.
	// +optional
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty"`

	// LastAttemptedRevision is the revision of the last reconciliation attempt.
	// +optional
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty"`

	meta.ReconcileRequestStatus `json:",inline"`

	// The last successfully applied revision metadata.
	// +optional
	Snapshot *Snapshot `json:"snapshot,omitempty"`
}

// KustomizationProgressing resets the conditions of the given Kustomization to a single
// ReadyCondition with status ConditionUnknown.
func KustomizationProgressing(k Kustomization) Kustomization {
	k.Status.Conditions = []meta.Condition{
		{
			Type:               meta.ReadyCondition,
			Status:             corev1.ConditionUnknown,
			LastTransitionTime: metav1.Now(),
			Reason:             meta.ProgressingReason,
			Message:            "reconciliation in progress",
		},
	}
	return k
}

// SetKustomizationCondition sets the given condition with the given status, reason and message
// on the Kustomization.
func SetKustomizationCondition(k *Kustomization, condition string, status corev1.ConditionStatus, reason, message string) {
	k.Status.Conditions = meta.FilterOutCondition(k.Status.Conditions, condition)
	k.Status.Conditions = append(k.Status.Conditions, meta.Condition{
		Type:               condition,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            trimString(message, MaxConditionMessageLength),
	})
}

// SetKustomizeReadiness sets the ReadyCondition, ObservedGeneration, and LastAttemptedRevision,
// on the Kustomization.
func SetKustomizationReadiness(k *Kustomization, status corev1.ConditionStatus, reason, message string, revision string) {
	SetKustomizationCondition(k, meta.ReadyCondition, status, reason, message)
	k.Status.ObservedGeneration = k.Generation
	k.Status.LastAttemptedRevision = revision
}

// KustomizationNotReady registers a failed apply attempt of the given Kustomization.
func KustomizationNotReady(k Kustomization, revision, reason, message string) Kustomization {
	SetKustomizationReadiness(&k, corev1.ConditionFalse, reason, message, revision)
	if revision != "" {
		k.Status.LastAttemptedRevision = revision
	}
	return k
}

// KustomizationNotReady registers a failed apply attempt of the given Kustomization,
// including a Snapshot.
func KustomizationNotReadySnapshot(k Kustomization, snapshot *Snapshot, revision, reason, message string) Kustomization {
	SetKustomizationReadiness(&k, corev1.ConditionFalse, reason, message, revision)
	k.Status.Snapshot = snapshot
	k.Status.LastAttemptedRevision = revision
	return k
}

// KustomizationReady registers a successful apply attempt of the given Kustomization.
func KustomizationReady(k Kustomization, snapshot *Snapshot, revision, reason, message string) Kustomization {
	SetKustomizationReadiness(&k, corev1.ConditionTrue, reason, message, revision)
	k.Status.Snapshot = snapshot
	k.Status.LastAppliedRevision = revision
	return k
}

// GetTimeout returns the timeout with default.
func (in Kustomization) GetTimeout() time.Duration {
	duration := in.Spec.Interval.Duration
	if in.Spec.Timeout != nil {
		duration = in.Spec.Timeout.Duration
	}
	if duration < time.Minute {
		return time.Minute
	}
	return duration
}

func (in Kustomization) GetDependsOn() (types.NamespacedName, []dependency.CrossNamespaceDependencyReference) {
	return types.NamespacedName{
		Namespace: in.Namespace,
		Name:      in.Name,
	}, in.Spec.DependsOn
}

const (
	// SourceIndexKey is the key used for indexing kustomizations
	// based on their Git sources.
	SourceIndexKey string = ".metadata.source"
	// BucketIndexKey is the key used for indexing kustomizations
	// based on their S3 sources.
	BucketIndexKey string = ".metadata.bucket"
)

// +genclient
// +genclient:Namespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=ks
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""

// Kustomization is the Schema for the kustomizations API.
type Kustomization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KustomizationSpec   `json:"spec,omitempty"`
	Status KustomizationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KustomizationList contains a list of kustomizations.
type KustomizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kustomization `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Kustomization{}, &KustomizationList{})
}

func trimString(str string, limit int) string {
	result := str
	chars := 0
	for i := range str {
		if chars >= limit {
			result = str[:i] + "..."
			break
		}
		chars++
	}
	return result
}

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
	MaxConditionMessageLength = 20000
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

	// The interval at which to reconcile the Kustomization.
	// +required
	Interval metav1.Duration `json:"interval"`

	// The KubeConfig for reconciling the Kustomization on a remote cluster.
	// When specified, KubeConfig takes precedence over ServiceAccountName.
	// +optional
	KubeConfig *KubeConfig `json:"kubeConfig,omitempty"`

	// Path to the directory containing the kustomization.yaml file, or the
	// set of plain YAMLs a kustomization.yaml should be generated for.
	// Defaults to 'None', which translates to the root path of the SourceRef.
	// +optional
	Path string `json:"path,omitempty"`

	// Prune enables garbage collection.
	// +required
	Prune bool `json:"prune"`

	// A list of resources to be included in the health assessment.
	// +optional
	HealthChecks []CrossNamespaceObjectReference `json:"healthChecks,omitempty"`

	// A list of images used to override or set the name and tag for container images.
	// +optional
	Images []Image `json:"images,omitempty"`

	// The name of the Kubernetes service account to impersonate
	// when reconciling this Kustomization.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

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

// Image contains the name, new name and new tag that will replace the original container image.
type Image struct {
	// Name of the image to be replaced.
	// +required
	Name string `json:"name"`

	// NewName is the name of the image used to replace the original one.
	// +required
	NewName string `json:"newName"`

	// NewTag is the image tag used to replace the original tag.
	// +required
	NewTag string `json:"newTag"`
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
	Conditions []metav1.Condition `json:"conditions,omitempty"`

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
	meta.SetResourceCondition(&k, meta.ReadyCondition, metav1.ConditionUnknown, meta.ProgressingReason, "reconciliation in progress")
	return k
}

// SetKustomizeReadiness sets the ReadyCondition, ObservedGeneration, and LastAttemptedRevision,
// on the Kustomization.
func SetKustomizationReadiness(k *Kustomization, status metav1.ConditionStatus, reason, message string, revision string) {
	meta.SetResourceCondition(k, meta.ReadyCondition, status, reason, trimString(message, MaxConditionMessageLength))
	k.Status.ObservedGeneration = k.Generation
	k.Status.LastAttemptedRevision = revision
}

// KustomizationNotReady registers a failed apply attempt of the given Kustomization.
func KustomizationNotReady(k Kustomization, revision, reason, message string) Kustomization {
	SetKustomizationReadiness(&k, metav1.ConditionFalse, reason, trimString(message, MaxConditionMessageLength), revision)
	if revision != "" {
		k.Status.LastAttemptedRevision = revision
	}
	return k
}

// KustomizationNotReady registers a failed apply attempt of the given Kustomization,
// including a Snapshot.
func KustomizationNotReadySnapshot(k Kustomization, snapshot *Snapshot, revision, reason, message string) Kustomization {
	SetKustomizationReadiness(&k, metav1.ConditionFalse, reason, trimString(message, MaxConditionMessageLength), revision)
	k.Status.Snapshot = snapshot
	k.Status.LastAttemptedRevision = revision
	return k
}

// KustomizationReady registers a successful apply attempt of the given Kustomization.
func KustomizationReady(k Kustomization, snapshot *Snapshot, revision, reason, message string) Kustomization {
	SetKustomizationReadiness(&k, metav1.ConditionTrue, reason, trimString(message, MaxConditionMessageLength), revision)
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

// GetStatusConditions returns a pointer to the Status.Conditions slice
func (in *Kustomization) GetStatusConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

const (
	// GitRepositoryIndexKey is the key used for indexing kustomizations
	// based on their Git sources.
	GitRepositoryIndexKey string = ".metadata.git"
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

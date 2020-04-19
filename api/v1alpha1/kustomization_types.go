/*
Copyright 2020 The Flux CD contributors.

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

package v1alpha1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KustomizationSpec defines the desired state of a kustomization.
type KustomizationSpec struct {
	// A list of kustomization that must be ready before this
	// kustomization can be applied.
	// +optional
	DependsOn []string `json:"dependsOn,omitempty"`

	// The interval at which to apply the kustomization.
	// +required
	Interval metav1.Duration `json:"interval"`

	// Path to the directory containing the kustomization file.
	// +kubebuilder:validation:Pattern="^\\./"
	// +required
	Path string `json:"path"`

	// Label selector used for garbage collection.
	// +kubebuilder:validation:Pattern="^.*=.*$"
	// +optional
	Prune string `json:"prune,omitempty"`

	// A list of workloads (Deployments, DaemonSets and StatefulSets)
	// to be included in the health assessment.
	// +optional
	HealthChecks []WorkloadReference `json:"healthChecks,omitempty"`

	// Reference of the source where the kustomization file is.
	// +required
	SourceRef corev1.TypedLocalObjectReference `json:"sourceRef"`

	// This flag tells the controller to suspend subsequent kustomize executions,
	// it does not apply to already started executions. Defaults to false.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// Timeout for validation, apply and health checking operations.
	// Defaults to 'Interval' duration.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Validate the Kubernetes objects before applying them on the cluster.
	// The validation strategy can be 'client' (local dry-run) or 'server' (APIServer dry-run).
	// +kubebuilder:validation:Enum=client;server
	// +optional
	Validation string `json:"validation,omitempty"`
}

// WorkloadReference defines a reference to a Deployment, DaemonSet or StatefulSet.
type WorkloadReference struct {
	// Kind is the type of resource being referenced.
	// +kubebuilder:validation:Enum=Deployment;DaemonSet;StatefulSet
	// +required
	Kind string `json:"kind"`

	// Name is the name of resource being referenced.
	// +required
	Name string `json:"name"`

	// Namespace is the namespace of resource being referenced.
	// +required
	Namespace string `json:"namespace,omitempty"`
}

// KustomizationStatus defines the observed state of a kustomization.
type KustomizationStatus struct {
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

func KustomizationReady(kustomization Kustomization, reason, message string) Kustomization {
	kustomization.Status.Conditions = []Condition{
		{
			Type:               ReadyCondition,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		},
	}
	return kustomization
}

func KustomizationNotReady(kustomization Kustomization, reason, message string) Kustomization {
	kustomization.Status.Conditions = []Condition{
		{
			Type:               ReadyCondition,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		},
	}
	return kustomization
}

// GetTimeout returns the timeout with default
func (in *Kustomization) GetTimeout() time.Duration {
	duration := in.Spec.Interval.Duration
	if in.Spec.Timeout != nil {
		duration = in.Spec.Timeout.Duration
	}
	if duration < time.Minute {
		return time.Minute
	}
	return duration
}

const (
	// SyncAtAnnotation is the annotation used for triggering a
	// sync outside of the specified schedule.
	SyncAtAnnotation string = "kustomize.fluxcd.io/syncAt"

	// SourceIndexKey is the key used for indexing kustomizations
	// based on their sources.
	SourceIndexKey string = ".metadata.source"

	// DependencyIndexKey is the key used for indexing kustomizations
	// based on their dependencies.
	DependencyIndexKey string = ".metadata.dependency"
)

// +kubebuilder:object:root=true
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

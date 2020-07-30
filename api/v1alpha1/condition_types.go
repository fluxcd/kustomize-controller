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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition contains condition information for a kustomization.
type Condition struct {
	// Type of the condition, currently ('Ready').
	// +required
	Type string `json:"type"`

	// Status of the condition, one of ('True', 'False', 'Unknown').
	// +required
	Status corev1.ConditionStatus `json:"status"`

	// LastTransitionTime is the timestamp corresponding to the last status
	// change of this condition.
	// +required
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// Reason is a brief machine readable explanation for the condition's last
	// transition.
	// +required
	Reason string `json:"reason,omitempty"`

	// Message is a human readable description of the details of the last
	// transition, complementing reason.
	// +optional
	Message string `json:"message,omitempty"`
}

const (
	// ReadyCondition represents the fact that a given kustomization has passed
	// validation and was successfully applied on the cluster.
	ReadyCondition string = "Ready"
)

const (
	// ApplySucceededReason represents the fact that the kustomization apply succeeded.
	ApplySucceededReason string = "ApplySucceeded"

	// ApplyFailedReason represents the fact that the kustomization apply failed.
	ApplyFailedReason string = "ApplyFailed"

	// PruneFailedReason represents the fact that the kustomization pruning failed.
	PruneFailedReason string = "PruneFailed"

	// ArtifactFailedReason represents the fact that the artifact download failed.
	ArtifactFailedReason string = "ArtifactFailed"

	// BuildFailedReason represents the fact that the kustomize build command failed.
	BuildFailedReason string = "BuildFailed"

	// DependencyNotReady represents the fact that the one of the dependencies is not ready.
	DependencyNotReadyReason string = "DependencyNotReady"

	// HealthCheckFailedReason represents the fact that the one of the health check failed.
	HealthCheckFailedReason string = "HealthCheckFailed"

	// InitializedReason represents the fact that a given resource has been initialized.
	InitializedReason string = "Initialized"

	// ProgressingReason represents the fact that a kustomization reconciliation
	// is underway.
	ProgressingReason string = "Progressing"

	// SuspendedReason represents the fact that the kustomization execution is suspended.
	SuspendedReason string = "Suspended"

	// ValidationFailedReason represents the fact that the dry-run apply failed.
	ValidationFailedReason string = "ValidationFailed"
)

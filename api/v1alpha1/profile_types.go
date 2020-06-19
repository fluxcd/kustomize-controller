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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProfileSpec defines the desired state of Profile
type ProfileSpec struct {
	// Alerting configuration of the kustomizations targeted by this profile.
	// +optional
	Alert *AlertProvider `json:"alert"`

	// The list of kustomizations that this profile applies to.
	// +required
	Kustomizations []string `json:"kustomizations"`
}

// Alert is the configuration of alerting for a specific provider
type AlertProvider struct {
	// HTTP(S) webhook address of this provider
	// +required
	Address string `json:"address"`

	// Alert channel for this provider
	// +required
	Channel string `json:"channel"`

	// Bot username for this provider
	// +required
	Username string `json:"username"`

	// Filter alerts based on verbosity level, defaults to ('error').
	// +kubebuilder:validation:Enum=info;error
	// +optional
	Verbosity string `json:"verbosity,omitempty"`

	// Type of provider
	// +kubebuilder:validation:Enum=slack;discord
	// +required
	Type string `json:"type"`
}

// ProfileStatus defines the observed state of Profile
type ProfileStatus struct {
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +genclient
// +genclient:Namespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""

// Profile is the Schema for the profiles API
type Profile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProfileSpec   `json:"spec,omitempty"`
	Status ProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProfileList contains a list of Profile
type ProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Profile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Profile{}, &ProfileList{})
}

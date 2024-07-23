/*
Copyright 2023 The Flux authors

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

package v1

import (
	"fmt"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/openfluxcd/artifact/utils"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// CrossNamespaceSourceReference contains enough information to let you locate the
// typed Kubernetes resource object at cluster level.
type CrossNamespaceSourceReference struct {
	// API version of the referent.
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind of the referent.
	// +required
	Kind string `json:"kind"`

	// Name of the referent.
	// +required
	Name string `json:"name"`

	// Namespace of the referent, defaults to the namespace of the Kubernetes
	// resource object that contains the reference.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

func (s *CrossNamespaceSourceReference) GetObjectKey() ctrlclient.ObjectKey {
	return ctrlclient.ObjectKey{
		Namespace: s.Namespace,
		Name:      s.Name,
	}
}

func (s *CrossNamespaceSourceReference) GetGroupKind() schema.GroupKind {
	if s.APIVersion == "" {
		return schema.GroupKind{
			Group: sourcev1.GroupVersion.Group,
			Kind:  s.Kind,
		}
	}

	return schema.GroupKind{
		Group: utils.ExtractGroupName(s.APIVersion),
		Kind:  s.Kind,
	}
}

func (s *CrossNamespaceSourceReference) GetName() string {
	return s.Name
}

func (s *CrossNamespaceSourceReference) GetNamespace() string {
	return s.Namespace
}

func (s *CrossNamespaceSourceReference) String() string {
	if s.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", s.Kind, s.Namespace, s.Name)
	}
	return fmt.Sprintf("%s/%s", s.Kind, s.Name)
}

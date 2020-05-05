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
	"bytes"
	"io"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Snapshot holds the metadata of the Kubernetes objects
// generated for a source revision
type Snapshot struct {
	// The source revision.
	// +required
	Revision string `json:"revision"`

	// A list of Kubernetes kinds grouped by namespace.
	// +required
	Entries []SnapshotEntry `json:"entries"`
}

// Snapshot holds the metadata of namespaced
// Kubernetes objects
type SnapshotEntry struct {
	// The namespace of this entry.
	// +optional
	Namespace string `json:"namespace"`

	// The list of Kubernetes kinds.
	// +required
	Kinds map[string]string `json:"kinds"`
}

func NewSnapshot(manifests []byte, revision string) (*Snapshot, error) {
	snapshot := Snapshot{
		Revision: revision,
		Entries:  []SnapshotEntry{},
	}

	reader := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifests), 2048)
	for {
		var obj unstructured.Unstructured
		err := reader.Decode(&obj)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if obj.IsList() {
			err := obj.EachListItem(func(item runtime.Object) error {
				snapshot.addEntry(item.(*unstructured.Unstructured))
				return nil
			})
			if err != nil {
				return nil, err
			}
		} else {
			snapshot.addEntry(&obj)
		}
	}

	return &snapshot, nil
}

func (s *Snapshot) addEntry(item *unstructured.Unstructured) {
	found := false
	for _, tracker := range s.Entries {
		if tracker.Namespace == item.GetNamespace() {
			tracker.Kinds[item.GetKind()] = item.GetAPIVersion()
			found = true
			break
		}
	}
	if !found {
		s.Entries = append(s.Entries, SnapshotEntry{
			Namespace: item.GetNamespace(),
			Kinds: map[string]string{
				item.GetKind(): item.GetAPIVersion(),
			},
		})
	}
}

func (s *Snapshot) NonNamespacedKinds() []string {
	kinds := make([]string, 0)
	for _, tracker := range s.Entries {
		if tracker.Namespace == "" {
			for k, _ := range tracker.Kinds {
				kinds = append(kinds, k)
			}
		}
	}
	return kinds
}

func (s *Snapshot) NamespacedKinds() map[string][]string {
	nsk := make(map[string][]string)
	for _, tracker := range s.Entries {
		if tracker.Namespace != "" {
			var kinds []string
			for k, _ := range tracker.Kinds {
				kinds = append(kinds, k)
			}
			nsk[tracker.Namespace] = kinds
		}
	}
	return nsk
}

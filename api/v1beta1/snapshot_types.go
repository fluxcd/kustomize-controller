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

package v1beta1

import (
	"bytes"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Snapshot holds the metadata of the Kubernetes objects
// generated for a source revision
type Snapshot struct {
	// The manifests sha1 checksum.
	// +required
	Checksum string `json:"checksum"`

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

func NewSnapshot(manifests []byte, checksum string) (*Snapshot, error) {
	snapshot := Snapshot{
		Checksum: checksum,
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
			tracker.Kinds[item.GroupVersionKind().String()] = item.GetKind()
			found = true
			break
		}
	}
	if !found {
		s.Entries = append(s.Entries, SnapshotEntry{
			Namespace: item.GetNamespace(),
			Kinds: map[string]string{
				item.GroupVersionKind().String(): item.GetKind(),
			},
		})
	}
}

func (s *Snapshot) NonNamespacedKinds() []schema.GroupVersionKind {
	kinds := make([]schema.GroupVersionKind, 0)

	for _, tracker := range s.Entries {
		if tracker.Namespace == "" {
			for gvk, kind := range tracker.Kinds {
				if strings.Contains(gvk, ",") {
					gv, err := schema.ParseGroupVersion(strings.Split(gvk, ",")[0])
					if err == nil {
						kinds = append(kinds, gv.WithKind(kind))
					}
				}
			}
		}
	}
	return kinds
}

func (s *Snapshot) NamespacedKinds() map[string][]schema.GroupVersionKind {
	nsk := make(map[string][]schema.GroupVersionKind)
	for _, tracker := range s.Entries {
		if tracker.Namespace != "" {
			var kinds []schema.GroupVersionKind
			for gvk, kind := range tracker.Kinds {
				if strings.Contains(gvk, ",") {
					gv, err := schema.ParseGroupVersion(strings.Split(gvk, ",")[0])
					if err == nil {
						kinds = append(kinds, gv.WithKind(kind))
					}
				}
			}
			nsk[tracker.Namespace] = kinds
		}
	}
	return nsk
}

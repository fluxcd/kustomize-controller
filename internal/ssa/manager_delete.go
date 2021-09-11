/*
Copyright 2021 Stefan Prodan
Copyright 2021 The Flux authors

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

package ssa

import (
	"context"
	"fmt"
	"sort"

	"github.com/fluxcd/kustomize-controller/internal/objectutil"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete deletes the given object (not found errors are ignored).
func (m *ResourceManager) Delete(ctx context.Context, object *unstructured.Unstructured, skipFor map[string]string) (*ChangeSetEntry, error) {
	existingObject := object.DeepCopy()
	err := m.client.Get(ctx, client.ObjectKeyFromObject(object), existingObject)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return m.changeSetEntry(object, UnknownAction),
				fmt.Errorf("%s query failed, error: %w", objectutil.FmtUnstructured(object), err)
		}
		return m.changeSetEntry(object, DeletedAction), nil
	}

	for n, s := range skipFor {
		if existingObject.GetLabels()[n] == s || existingObject.GetAnnotations()[n] == s {
			return m.changeSetEntry(object, UnchangedAction), nil
		}
	}

	if err := m.client.Delete(ctx, existingObject); err != nil {
		return m.changeSetEntry(object, UnknownAction),
			fmt.Errorf("%s delete failed, error: %w", objectutil.FmtUnstructured(object), err)
	}

	return m.changeSetEntry(object, DeletedAction), nil
}

// DeleteAll deletes the given set of objects (not found errors are ignored).
func (m *ResourceManager) DeleteAll(ctx context.Context, objects []*unstructured.Unstructured, skipFor map[string]string) (*ChangeSet, error) {
	sort.Sort(sort.Reverse(objectutil.SortableUnstructureds(objects)))
	changeSet := NewChangeSet()

	var errors string
	for _, object := range objects {
		cse, err := m.Delete(ctx, object, skipFor)
		if cse != nil {
			changeSet.Add(*cse)
		}
		if err != nil {
			errors += err.Error() + ";"
		}
	}

	if errors != "" {
		return changeSet, fmt.Errorf("delete failed, errors: %s", errors)
	}

	return changeSet, nil
}

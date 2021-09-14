/*
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

package controllers

import (
	"github.com/fluxcd/pkg/apis/meta"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/cli-utils/pkg/object"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/kustomize-controller/internal/objectutil"
)

func NewInventory() *kustomizev1.ResourceInventory {
	return &kustomizev1.ResourceInventory{
		Entries: []kustomizev1.ResourceRef{},
	}
}

// AddObjectsToInventory extracts the metadata from the given objects and adds it to the inventory.
func AddObjectsToInventory(inv *kustomizev1.ResourceInventory, objects []*unstructured.Unstructured) error {
	sort.Sort(objectutil.SortableUnstructureds(objects))
	for _, om := range objects {
		objMetadata := object.UnstructuredToObjMeta(om)
		gv, err := schema.ParseGroupVersion(om.GetAPIVersion())
		if err != nil {
			return err
		}

		inv.Entries = append(inv.Entries, kustomizev1.ResourceRef{
			ID:      objMetadata.String(),
			Version: gv.Version,
		})
	}

	return nil
}

// ListObjectsInInventory returns the inventory entries as unstructured.Unstructured objects.
func ListObjectsInInventory(inv *kustomizev1.ResourceInventory) ([]*unstructured.Unstructured, error) {
	objects := make([]*unstructured.Unstructured, 0)

	for _, entry := range inv.Entries {
		objMetadata, err := object.ParseObjMetadata(entry.ID)
		if err != nil {
			return nil, err
		}

		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   objMetadata.GroupKind.Group,
			Kind:    objMetadata.GroupKind.Kind,
			Version: entry.Version,
		})
		u.SetName(objMetadata.Name)
		u.SetNamespace(objMetadata.Namespace)
		objects = append(objects, u)
	}

	sort.Sort(objectutil.SortableUnstructureds(objects))
	return objects, nil
}

// ListMetaInInventory returns the inventory entries as object.ObjMetadata objects.
func ListMetaInInventory(inv *kustomizev1.ResourceInventory) ([]object.ObjMetadata, error) {
	var metas []object.ObjMetadata
	for _, e := range inv.Entries {
		m, err := object.ParseObjMetadata(e.ID)
		if err != nil {
			return metas, err
		}
		metas = append(metas, m)
	}

	return metas, nil
}

// DiffInventory returns the slice of objects that do not exist in the target inventory.
func DiffInventory(inv *kustomizev1.ResourceInventory, target *kustomizev1.ResourceInventory) ([]*unstructured.Unstructured, error) {
	versionOf := func(i *kustomizev1.ResourceInventory, objMetadata object.ObjMetadata) string {
		for _, entry := range i.Entries {
			if entry.ID == objMetadata.String() {
				return entry.Version
			}
		}
		return ""
	}

	objects := make([]*unstructured.Unstructured, 0)
	aList, err := ListMetaInInventory(inv)
	if err != nil {
		return nil, err
	}

	bList, err := ListMetaInInventory(target)
	if err != nil {
		return nil, err
	}

	list := object.SetDiff(aList, bList)
	if len(list) == 0 {
		return objects, nil
	}

	for _, metadata := range list {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   metadata.GroupKind.Group,
			Kind:    metadata.GroupKind.Kind,
			Version: versionOf(inv, metadata),
		})
		u.SetName(metadata.Name)
		u.SetNamespace(metadata.Namespace)
		objects = append(objects, u)
	}

	sort.Sort(objectutil.SortableUnstructureds(objects))
	return objects, nil
}

func referenceToUnstructured(cr []meta.NamespacedObjectKindReference) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured

	for _, c := range cr {
		// For backwards compatibility with Kustomization v1beta1
		if c.APIVersion == "" {
			c.APIVersion = "apps/v1"
		}

		gv, err := schema.ParseGroupVersion(c.APIVersion)
		if err != nil {
			return objects, err
		}

		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gv.Group,
			Kind:    c.Kind,
			Version: gv.Version,
		})
		u.SetName(c.Name)
		u.SetNamespace(c.Namespace)
		objects = append(objects, u)

	}

	return objects, nil
}

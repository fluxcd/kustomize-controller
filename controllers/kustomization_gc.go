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

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

type KustomizeGarbageCollector struct {
	snapshot    kustomizev1.Snapshot
	newChecksum string
	log         logr.Logger
	client.Client
}

func NewGarbageCollector(kubeClient client.Client, snapshot kustomizev1.Snapshot, newChecksum string, log logr.Logger) *KustomizeGarbageCollector {
	return &KustomizeGarbageCollector{
		Client:      kubeClient,
		snapshot:    snapshot,
		newChecksum: newChecksum,
		log:         log,
	}
}

// Prune deletes Kubernetes objects removed from source.
// Namespaced objects are removed before global ones, as in CRs before CRDs.
// The garbage collector determines what objects to prune based on
// a label selector that contains the previously applied revision.
// The garbage collector ignores objects that are no longer present
// on the cluster or if they are marked for deleting using Kubernetes finalizers.
func (kgc *KustomizeGarbageCollector) Prune(timeout time.Duration, name string, namespace string) (string, bool) {
	changeSet := ""
	outErr := ""

	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()

	for ns, gvks := range kgc.snapshot.NamespacedKinds() {
		for _, gvk := range gvks {
			ulist := &unstructured.UnstructuredList{}
			ulist.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   gvk.Group,
				Kind:    gvk.Kind + "List",
				Version: gvk.Version,
			})

			err := kgc.List(ctx, ulist, client.InNamespace(ns), kgc.matchingLabels(name, namespace))
			if err == nil {
				for _, item := range ulist.Items {
					id := fmt.Sprintf("%s/%s/%s", item.GetKind(), item.GetNamespace(), item.GetName())
					if kgc.shouldSkip(item) {
						kgc.log.V(1).Info(fmt.Sprintf("gc is disabled for '%s'", id))
						continue
					}

					if kgc.isStale(item) && item.GetDeletionTimestamp().IsZero() {
						err = kgc.Delete(ctx, &item)
						if err != nil {
							outErr += fmt.Sprintf("delete failed for %s: %v\n", id, err)
						} else {
							if len(item.GetFinalizers()) > 0 {
								changeSet += fmt.Sprintf("%s marked for deletion\n", id)
							} else {
								changeSet += fmt.Sprintf("%s deleted\n", id)
							}
						}
					}
				}
			} else {
				kgc.log.V(1).Info(fmt.Sprintf("gc query failed for %s: %v", gvk.Kind, err))
			}
		}
	}

	for _, gvk := range kgc.snapshot.NonNamespacedKinds() {
		ulist := &unstructured.UnstructuredList{}
		ulist.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Kind:    gvk.Kind + "List",
			Version: gvk.Version,
		})

		err := kgc.List(ctx, ulist, kgc.matchingLabels(name, namespace))
		if err == nil {
			for _, item := range ulist.Items {
				id := fmt.Sprintf("%s/%s", item.GetKind(), item.GetName())

				if kgc.shouldSkip(item) {
					kgc.log.V(1).Info(fmt.Sprintf("gc is disabled for '%s'", id))
					continue
				}

				if kgc.isStale(item) && item.GetDeletionTimestamp().IsZero() {
					err = kgc.Delete(ctx, &item)
					if err != nil {
						outErr += fmt.Sprintf("delete failed for %s: %v\n", id, err)
					} else {
						if len(item.GetFinalizers()) > 0 {
							changeSet += fmt.Sprintf("%s/%s marked for deletion\n", item.GetKind(), item.GetName())
						} else {
							changeSet += fmt.Sprintf("%s/%s deleted\n", item.GetKind(), item.GetName())
						}
					}
				}
			}
		} else {
			kgc.log.V(1).Info(fmt.Sprintf("gc query failed for %s: %v", gvk.Kind, err))
		}
	}

	if outErr != "" {
		return outErr, false
	}
	return changeSet, true
}

// Determine staleness by checking if the annotation matches the latest checksum
func (kgc *KustomizeGarbageCollector) isStale(obj unstructured.Unstructured) bool {
	itemAnnotationChecksum := obj.GetAnnotations()[fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group)]

	switch kgc.newChecksum {
	case "":
		return true
	case itemAnnotationChecksum:
		return false
	default:
		return true
	}
}

func (kgc *KustomizeGarbageCollector) shouldSkip(obj unstructured.Unstructured) bool {
	key := fmt.Sprintf("%s/prune", kustomizev1.GroupVersion.Group)

	return obj.GetLabels()[key] == kustomizev1.DisabledValue || obj.GetAnnotations()[key] == kustomizev1.DisabledValue
}

func (kgc *KustomizeGarbageCollector) matchingLabels(name, namespace string) client.MatchingLabels {
	return selectorLabels(name, namespace)
}

func gcLabels(name, namespace, checksum string) map[string]string {
	return map[string]string{
		fmt.Sprintf("%s/name", kustomizev1.GroupVersion.Group):      name,
		fmt.Sprintf("%s/namespace", kustomizev1.GroupVersion.Group): namespace,
	}
}
func gcAnnotation(checksum string) map[string]string {
	return map[string]string{
		fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group): checksum,
	}
}

func selectorLabels(name, namespace string) map[string]string {
	return map[string]string{
		fmt.Sprintf("%s/name", kustomizev1.GroupVersion.Group):      name,
		fmt.Sprintf("%s/namespace", kustomizev1.GroupVersion.Group): namespace,
	}

}

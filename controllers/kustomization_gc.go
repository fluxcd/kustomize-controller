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
	snapshot kustomizev1.Snapshot
	log      logr.Logger
	client.Client
}

func NewGarbageCollector(kubeClient client.Client, snapshot kustomizev1.Snapshot, log logr.Logger) *KustomizeGarbageCollector {
	return &KustomizeGarbageCollector{
		Client:   kubeClient,
		snapshot: snapshot,
		log:      log,
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

			err := kgc.List(ctx, ulist, client.InNamespace(ns), kgc.matchingLabels(name, namespace, kgc.snapshot.Checksum))
			if err == nil {
				for _, item := range ulist.Items {
					if item.GetDeletionTimestamp().IsZero() {
						name := fmt.Sprintf("%s/%s/%s", item.GetKind(), item.GetNamespace(), item.GetName())
						err = kgc.Delete(ctx, &item)
						if err != nil {
							outErr += fmt.Sprintf("delete failed for %s: %v\n", name, err)
						} else {
							if len(item.GetFinalizers()) > 0 {
								changeSet += fmt.Sprintf("%s marked for deletion\n", name)
							} else {
								changeSet += fmt.Sprintf("%s deleted\n", name)
							}
						}
					}
				}
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

		err := kgc.List(ctx, ulist, kgc.matchingLabels(name, namespace, kgc.snapshot.Checksum))
		if err == nil {
			for _, item := range ulist.Items {
				if item.GetDeletionTimestamp().IsZero() {
					name := fmt.Sprintf("%s/%s", item.GetKind(), item.GetName())
					err = kgc.Delete(ctx, &item)
					if err != nil {
						outErr += fmt.Sprintf("delete failed for %s: %v\n", name, err)
					} else {
						if len(item.GetFinalizers()) > 0 {
							changeSet += fmt.Sprintf("%s/%s marked for deletion\n", item.GetKind(), item.GetName())
						} else {
							changeSet += fmt.Sprintf("%s/%s deleted\n", item.GetKind(), item.GetName())
						}
					}
				}
			}
		}
	}

	if outErr != "" {
		return outErr, false
	}
	return changeSet, true
}

func (kgc *KustomizeGarbageCollector) matchingLabels(name, namespace, checksum string) client.MatchingLabels {
	return gcLabels(name, namespace, checksum)
}

func gcLabels(name, namespace, checksum string) map[string]string {
	return map[string]string{
		fmt.Sprintf("%s/name", kustomizev1.GroupVersion.Group):      name,
		fmt.Sprintf("%s/namespace", kustomizev1.GroupVersion.Group): namespace,
		fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group):  checksum,
	}
}

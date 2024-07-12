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

package controller

import (
	"context"
	"fmt"
	"github.com/fluxcd/pkg/runtime/conditions"
	artifactv1 "github.com/openfluxcd/artifact/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"

	"github.com/fluxcd/pkg/runtime/dependency"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func (r *KustomizationReconciler) requestsForRevisionChangeOf(indexKey string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)
		repo, ok := obj.(interface {
			GetArtifact() *sourcev1.Artifact
		})
		if !ok {
			log.Error(fmt.Errorf("expected an object conformed with GetArtifact() method, but got a %T", obj),
				"failed to get reconcile requests for revision change")
			return nil
		}
		// If we do not have an artifact, we have no requests to make
		if repo.GetArtifact() == nil {
			return nil
		}

		var list kustomizev1.KustomizationList
		if err := r.List(ctx, &list, client.MatchingFields{
			indexKey: client.ObjectKeyFromObject(obj).String(),
		}); err != nil {
			log.Error(err, "failed to list objects for revision change")
			return nil
		}
		var dd []dependency.Dependent
		for i, d := range list.Items {
			// If the Kustomization is ready and the revision of the artifact equals
			// to the last attempted revision, we should not make a request for this Kustomization
			if conditions.IsReady(&list.Items[i]) && repo.GetArtifact().HasRevision(d.Status.LastAttemptedRevision) {
				continue
			}
			dd = append(dd, d.DeepCopy())
		}
		sorted, err := dependency.Sort(dd)
		if err != nil {
			log.Error(err, "failed to sort dependencies for revision change")
			return nil
		}
		reqs := make([]reconcile.Request, len(sorted))
		for i := range sorted {
			reqs[i].NamespacedName.Name = sorted[i].Name
			reqs[i].NamespacedName.Namespace = sorted[i].Namespace
		}
		return reqs
	}
}

func (r *KustomizationReconciler) indexBy(kind string) func(o client.Object) []string {
	return func(o client.Object) []string {
		k, ok := o.(*kustomizev1.Kustomization)
		if !ok {
			panic(fmt.Sprintf("Expected a Kustomization, got %T", o))
		}

		if k.Spec.SourceRef.Kind == kind {
			namespace := k.GetNamespace()
			if k.Spec.SourceRef.Namespace != "" {
				namespace = k.Spec.SourceRef.Namespace
			}
			return []string{fmt.Sprintf("%s/%s", namespace, k.Spec.SourceRef.Name)}
		}

		return nil
	}
}

func (r *KustomizationReconciler) indirectRequestsForRevisionChangeOf(indexKey string) handler.MapFunc {
	// Queues requests for all kustomization resources that either reference the artifact resource directly in their
	// source ref or that reference another resource that is referenced by the owner reference of the artifact in their
	// source ref.
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)
		art, ok := obj.(*artifactv1.Artifact)
		if !ok {
			log.Error(fmt.Errorf("expected an object conformed with GetArtifact() method, but got a %T", obj),
				"failed to get reconcile requests for revision change")
			return nil
		}
		// If we do not have an artifact, we have no requests to make
		if art.GetArtifact() == nil {
			return nil
		}

		dd := r.Lookup(ctx, art, indexKey, client.ObjectKeyFromObject(obj).String())

		for _, ref := range art.OwnerReferences {
			dd = append(dd, r.Lookup(ctx, art, indexKey, fmt.Sprintf("%s/%s/%s/%s", ExtractGroupName(ref.APIVersion), ref.Kind, art.Namespace, ref.Name))...)
		}

		sorted, err := dependency.Sort(dd)
		if err != nil {
			log.Error(err, "failed to sort dependencies for revision change")
			return nil
		}
		reqs := make([]reconcile.Request, len(sorted))
		for i := range sorted {
			reqs[i].NamespacedName.Name = sorted[i].Name
			reqs[i].NamespacedName.Namespace = sorted[i].Namespace
		}
		return reqs
	}
}

func (r *KustomizationReconciler) indirectIndex() func(o client.Object) []string {
	return func(o client.Object) []string {
		k, ok := o.(*kustomizev1.Kustomization)
		if !ok {
			panic(fmt.Sprintf("Expected a Kustomization, got %T", o))
		}

		key := KeyForReference(k, k.Spec.SourceRef)
		if key != "" {
			return []string{key}
		}

		return nil
	}
}

func KeyForReference(o metav1.Object, ref kustomizev1.CrossNamespaceSourceReference) string {
	if ref.Kind == "" {
		return ""
	}
	var group string
	if ref.APIVersion == "" {
		group = sourcev1.GroupVersion.Group
	} else {
		group = ExtractGroupName(ref.APIVersion)
	}

	if group == sourcev1.GroupVersion.Group {
		// Could be generalized later
		return ""
	}
	namespace := o.GetNamespace()
	if ref.Namespace != "" {
		namespace = ref.Namespace
	}
	return fmt.Sprintf("%s/%s/%s/%s", group, ref.Kind, namespace, ref.Name)
}

// Extracts the group name from an API version string.
// If the API version belongs to the core group, returns an empty string.
func ExtractGroupName(apiVersion string) string {
	parts := strings.Split(apiVersion, "/")
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}

func (r *KustomizationReconciler) Lookup(ctx context.Context, art *artifactv1.Artifact, indexKey string, objectKey string) []dependency.Dependent {
	log := ctrl.LoggerFrom(ctx)

	var list kustomizev1.KustomizationList
	if err := r.List(ctx, &list, client.MatchingFields{
		indexKey: objectKey,
	}); err != nil {
		log.Error(err, "failed to list objects for revision change")
		return nil
	}
	var dd []dependency.Dependent
	for i, d := range list.Items {
		// If the Kustomization is ready and the revision of the artifact equals
		// to the last attempted revision, we should not make a request for this Kustomization
		if conditions.IsReady(&list.Items[i]) && art.GetArtifact().HasRevision(d.Status.LastAttemptedRevision) {
			continue
		}
		dd = append(dd, d.DeepCopy())
	}
	return dd
}

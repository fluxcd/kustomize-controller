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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/dependency"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

const (
	dependsOnIndexKey string = ".metadata.dependsOn"
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

func isNotReadyForDependency(k *kustomizev1.Kustomization) bool {
	c := conditions.Get(k, meta.ReadyCondition)
	if c == nil {
		return false
	}
	return c.Status == metav1.ConditionFalse && c.Reason == meta.DependencyNotReadyReason
}

func (r *KustomizationReconciler) requestsForDependents(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrl.LoggerFrom(ctx)

	var list kustomizev1.KustomizationList
	if err := r.List(ctx, &list, client.MatchingFields{
		dependsOnIndexKey: client.ObjectKeyFromObject(obj).String(),
	}); err != nil {
		log.Error(err, "failed to list objects for dependency change")
		return nil
	}
	var dd []dependency.Dependent
	for _, d := range list.Items {
		if isNotReadyForDependency(&d) {
			dd = append(dd, &d)
		}
	}
	sorted, err := dependency.Sort(dd)
	if err != nil {
		log.Error(err, "failed to sort dependents for dependency change")
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(sorted))
	debugLog := log.V(1).WithValues("dependency", map[string]string{
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
	})
	for _, d := range sorted {
		debugLog.Info("requesting reconciliation of dependent", "dependent", map[string]string{
			"name":      d.Name,
			"namespace": d.Namespace,
		})
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      d.Name,
			Namespace: d.Namespace,
		}})
	}
	return reqs
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

func (r *KustomizationReconciler) indexDependsOn(o client.Object) []string {
	k, ok := o.(*kustomizev1.Kustomization)
	if !ok {
		panic(fmt.Sprintf("Expected a Kustomization, got %T", o))
	}

	deps := make([]string, len(k.Spec.DependsOn))
	for i, dep := range k.Spec.DependsOn {
		namespace := k.GetNamespace()
		if dep.Namespace != "" {
			namespace = dep.Namespace
		}
		deps[i] = fmt.Sprintf("%s/%s", namespace, dep.Name)
	}

	return deps
}

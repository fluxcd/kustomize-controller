/*
Copyright 2025 The Flux authors

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
	"time"

	"github.com/fluxcd/pkg/runtime/predicates"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

// KustomizationReconcilerOptions contains options for the KustomizationReconciler.
type KustomizationReconcilerOptions struct {
	HTTPRetry                 int
	DependencyRequeueInterval time.Duration
	RateLimiter               workqueue.TypedRateLimiter[reconcile.Request]
	WatchConfigsPredicate     predicate.Predicate
}

// SetupWithManager sets up the controller with the Manager.
// It indexes the Kustomizations by the source references, and sets up watches for
// changes in those sources, as well as for ConfigMaps and Secrets that the Kustomizations depend on.
func (r *KustomizationReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts KustomizationReconcilerOptions) error {
	const (
		indexOCIRepository = ".metadata.ociRepository"
		indexGitRepository = ".metadata.gitRepository"
		indexBucket        = ".metadata.bucket"
		indexConfigMap     = ".metadata.configMap"
		indexSecret        = ".metadata.secret"
	)

	// Index the Kustomizations by the OCIRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, indexOCIRepository,
		r.indexBy(sourcev1.OCIRepositoryKind)); err != nil {
		return fmt.Errorf("failed creating index %s: %w", indexOCIRepository, err)
	}

	// Index the Kustomizations by the GitRepository references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, indexGitRepository,
		r.indexBy(sourcev1.GitRepositoryKind)); err != nil {
		return fmt.Errorf("failed creating index %s: %w", indexGitRepository, err)
	}

	// Index the Kustomizations by the Bucket references they (may) point at.
	if err := mgr.GetCache().IndexField(ctx, &kustomizev1.Kustomization{}, indexBucket,
		r.indexBy(sourcev1.BucketKind)); err != nil {
		return fmt.Errorf("failed creating index %s: %w", indexBucket, err)
	}

	// Index the Kustomization by the ConfigMap references they point to.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &kustomizev1.Kustomization{}, indexConfigMap,
		func(o client.Object) []string {
			obj := o.(*kustomizev1.Kustomization)
			namespace := obj.GetNamespace()
			var keys []string
			if kc := obj.Spec.KubeConfig; kc != nil && kc.ConfigMapRef != nil {
				keys = append(keys, fmt.Sprintf("%s/%s", namespace, kc.ConfigMapRef.Name))
			}
			if pb := obj.Spec.PostBuild; pb != nil {
				for _, ref := range pb.SubstituteFrom {
					if ref.Kind == "ConfigMap" {
						keys = append(keys, fmt.Sprintf("%s/%s", namespace, ref.Name))
					}
				}
			}
			return keys
		},
	); err != nil {
		return fmt.Errorf("failed creating index %s: %w", indexConfigMap, err)
	}

	// Index the Kustomization by the Secret references they point to.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &kustomizev1.Kustomization{}, indexSecret,
		func(o client.Object) []string {
			obj := o.(*kustomizev1.Kustomization)
			namespace := obj.GetNamespace()
			var keys []string
			if dec := obj.Spec.Decryption; dec != nil && dec.SecretRef != nil {
				keys = append(keys, fmt.Sprintf("%s/%s", namespace, dec.SecretRef.Name))
			}
			if kc := obj.Spec.KubeConfig; kc != nil && kc.SecretRef != nil {
				keys = append(keys, fmt.Sprintf("%s/%s", namespace, kc.SecretRef.Name))
			}
			if pb := obj.Spec.PostBuild; pb != nil {
				for _, ref := range pb.SubstituteFrom {
					if ref.Kind == "Secret" {
						keys = append(keys, fmt.Sprintf("%s/%s", namespace, ref.Name))
					}
				}
			}
			return keys
		},
	); err != nil {
		return fmt.Errorf("failed creating index %s: %w", indexSecret, err)
	}

	r.requeueDependency = opts.DependencyRequeueInterval
	r.statusManager = fmt.Sprintf("gotk-%s", r.ControllerName)
	r.artifactFetchRetries = opts.HTTPRetry

	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Kustomization{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicates.ReconcileRequestedPredicate{}),
		)).
		Watches(
			&sourcev1.OCIRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(indexOCIRepository)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1.GitRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(indexGitRepository)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1.Bucket{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf(indexBucket)),
			builder.WithPredicates(SourceRevisionChangePredicate{}),
		).
		WatchesMetadata(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForConfigDependency(indexConfigMap)),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}, opts.WatchConfigsPredicate),
		).
		WatchesMetadata(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForConfigDependency(indexSecret)),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}, opts.WatchConfigsPredicate),
		).
		WithOptions(controller.Options{
			RateLimiter: opts.RateLimiter,
		}).
		Complete(r)
}

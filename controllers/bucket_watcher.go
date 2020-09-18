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

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxcd/pkg/runtime/dependency"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
	consts "github.com/fluxcd/pkg/runtime"
	sourcev1 "github.com/fluxcd/source-controller/api/v1alpha1"
)

// BucketWatcher watches Bucket objects for revision changes
// and triggers a reconcile for all the Kustomizations that reference a changed source
type BucketWatcher struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=buckets/status,verbs=get

func (r *BucketWatcher) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var repo sourcev1.Bucket
	if err := r.Get(ctx, req.NamespacedName, &repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues(strings.ToLower(repo.Kind), req.NamespacedName)
	log.Info("new artifact detected")

	// get the list of kustomizations that are using this Bucket
	var list kustomizev1.KustomizationList
	if err := r.List(ctx, &list,
		client.MatchingFields{kustomizev1.BucketIndexKey: fmt.Sprintf("%s/%s", req.Namespace, req.Name)}); err != nil {
		log.Error(err, "unable to list kustomizations")
		return ctrl.Result{}, err
	}

	var dd []dependency.Dependent
	for _, d := range list.Items {
		dd = append(dd, d)
	}
	sorted, err := dependency.Sort(dd)
	if err != nil {
		log.Error(err, "unable to dependency sort kustomizations")
		return ctrl.Result{}, err
	}

	// trigger apply for each kustomization using this Bucket taking into account the dependency order
	for _, k := range sorted {
		name := types.NamespacedName(k)
		if err := r.requestReconciliation(name); err != nil {
			log.Error(err, "unable to annotate Kustomization", "kustomization", name)
			continue
		}
		log.Info("requested immediate reconciliation", "kustomization", name)
	}

	return ctrl.Result{}, nil
}

func (r *BucketWatcher) SetupWithManager(mgr ctrl.Manager) error {
	// create a kustomization index based on Bucket name
	err := mgr.GetFieldIndexer().IndexField(context.TODO(), &kustomizev1.Kustomization{}, kustomizev1.BucketIndexKey,
		func(rawObj runtime.Object) []string {
			k := rawObj.(*kustomizev1.Kustomization)
			if k.Spec.SourceRef.Kind == sourcev1.BucketKind {
				namespace := k.GetNamespace()
				if k.Spec.SourceRef.Namespace != "" {
					namespace = k.Spec.SourceRef.Namespace
				}
				return []string{fmt.Sprintf("%s/%s", namespace, k.Spec.SourceRef.Name)}
			}
			return nil
		},
	)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sourcev1.Bucket{}).
		WithEventFilter(BucketRevisionChangePredicate{}).
		Complete(r)
}

func (r *BucketWatcher) requestReconciliation(name types.NamespacedName) error {
	var kustomization kustomizev1.Kustomization
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		if err := r.Get(context.TODO(), name, &kustomization); err != nil {
			return err
		}

		if kustomization.Annotations == nil {
			kustomization.Annotations = make(map[string]string)
		}
		kustomization.Annotations[consts.ReconcileAtAnnotation] = metav1.Now().String()
		err = r.Update(context.TODO(), &kustomization)
		return
	})
}

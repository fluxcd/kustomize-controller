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
	"strings"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1alpha1"
)

// KustomizationReconciler watches a GitRepository object
type GitRepositoryWatcher struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=source.fluxcd.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.fluxcd.io,resources=gitrepositories/status,verbs=get

func (r *GitRepositoryWatcher) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var repo sourcev1.GitRepository
	if err := r.Get(ctx, req.NamespacedName, &repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues(strings.ToLower(repo.Kind), req.NamespacedName)
	log.Info("New artifact detected")

	// get the list of kustomizations that are using this Git repository
	var list kustomizev1.KustomizationList
	if err := r.List(ctx, &list, client.InNamespace(req.Namespace),
		client.MatchingFields{kustomizev1.SourceIndexKey: req.Name}); err != nil {
		log.Error(err, "unable to list kustomizations")
		return ctrl.Result{}, err
	}

	// trigger apply for each kustomization using this Git repository
	for _, kustomization := range list.Items {
		kustomization.Annotations[kustomizev1.SyncAtAnnotation] = metav1.Now().String()
		if kustomization.Spec.SourceRef.APIGroup == nil {
			emptyAPIGroup := ""
			kustomization.Spec.SourceRef.APIGroup = &emptyAPIGroup
		}
		if err := r.Update(ctx, &kustomization); err != nil {
			log.Error(err, "unable to annotate kustomization", "kustomization", kustomization.GetName())
		}
		log.Info("Run kustomization", "kustomization", kustomization.GetName())
	}

	return ctrl.Result{}, nil
}

func (r *GitRepositoryWatcher) SetupWithManager(mgr ctrl.Manager) error {
	// create a kustomization index based on Git repository name
	err := mgr.GetFieldIndexer().IndexField(&kustomizev1.Kustomization{}, kustomizev1.SourceIndexKey,
		func(rawObj runtime.Object) []string {
			k := rawObj.(*kustomizev1.Kustomization)
			if k.Spec.SourceRef.Kind == "GitRepository" {
				return []string{k.Spec.SourceRef.Name}
			}
			return nil
		},
	)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sourcev1.GitRepository{}).
		WithEventFilter(GitRepositoryRevisionChangePredicate{}).
		Complete(r)
}

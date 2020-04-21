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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
)

// ProfileReconciler reconciles a Profile object
type ProfileReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kustomize.fluxcd.io,resources=profiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kustomize.fluxcd.io,resources=profiles/status,verbs=get;update;patch

func (r *ProfileReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()

	var profile kustomizev1.Profile
	if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Log.WithValues(strings.ToLower(profile.Kind), req.NamespacedName)

	init := true
	for _, condition := range profile.Status.Conditions {
		if condition.Type == kustomizev1.ReadyCondition && condition.Status == corev1.ConditionTrue {
			init = false
			break
		}
	}

	if init {
		profile.Status.Conditions = []kustomizev1.Condition{
			{
				Type:               kustomizev1.ReadyCondition,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             kustomizev1.InitializedReason,
				Message:            kustomizev1.InitializedReason,
			},
		}
		if err := r.Status().Update(ctx, &profile); err != nil {
			return ctrl.Result{Requeue: true}, err
		}
		log.Info("Profile initialised")
	}

	return ctrl.Result{}, nil
}

func (r *ProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kustomizev1.Profile{}).
		Complete(r)
}

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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
)

func requestReconciliation(r client.Client, name types.NamespacedName) error {
	var kustomization kustomizev1.Kustomization
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		if err := r.Get(context.TODO(), name, &kustomization); err != nil {
			return err
		}

		if kustomization.Annotations == nil {
			kustomization.Annotations = make(map[string]string)
		}
		kustomization.Annotations[meta.ReconcileRequestAnnotation] = metav1.Now().String()
		err = r.Update(context.TODO(), &kustomization)
		return
	})
}

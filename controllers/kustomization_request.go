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

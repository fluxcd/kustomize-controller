/*
Copyright 2023 The Flux authors

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
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_StagedApply(t *testing.T) {
	g := NewWithT(t)

	namespaceName := "kust-" + randStringRunes(5)
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
	}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})

	err := createKubeConfigSecret(namespaceName)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata/crds", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: namespaceName,
	}

	err = applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	kustomization := &kustomizev1.Kustomization{}
	kustomization.Name = "test-kust"
	kustomization.Namespace = namespaceName
	kustomization.Spec = kustomizev1.KustomizationSpec{
		Interval: metav1.Duration{Duration: 10 * time.Minute},
		Prune:    true,
		Path:     "./",
		SourceRef: kustomizev1.CrossNamespaceSourceReference{
			Name:      repositoryName.Name,
			Namespace: repositoryName.Namespace,
			Kind:      sourcev1.GitRepositoryKind,
		},
		KubeConfig: &meta.KubeConfigReference{
			SecretRef: &meta.SecretKeyReference{
				Name: "kubeconfig",
			},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return isReconcileSuccess(&obj) && obj.Status.LastAttemptedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return errors.IsNotFound(err)
	}, timeout, time.Second).Should(BeTrue())
}

func TestKustomizationReconciler_deleteBeforeFinalizer(t *testing.T) {
	g := NewWithT(t)

	namespaceName := "kust-" + randStringRunes(5)
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
	}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})

	kustomization := &kustomizev1.Kustomization{}
	kustomization.Name = "test-kust"
	kustomization.Namespace = namespaceName
	kustomization.Spec = kustomizev1.KustomizationSpec{
		Interval: metav1.Duration{Duration: interval},
		Prune:    true,
		SourceRef: kustomizev1.CrossNamespaceSourceReference{
			Kind: "Bucket",
			Name: "foo",
		},
	}
	// Add a test finalizer to prevent the object from getting deleted.
	kustomization.SetFinalizers([]string{"test-finalizer"})
	g.Expect(k8sClient.Create(ctx, kustomization)).NotTo(HaveOccurred())
	// Add deletion timestamp by deleting the object.
	g.Expect(k8sClient.Delete(ctx, kustomization)).NotTo(HaveOccurred())

	r := &KustomizationReconciler{
		Client:        k8sClient,
		EventRecorder: record.NewFakeRecorder(32),
	}
	// NOTE: Only a real API server responds with an error in this scenario.
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(kustomization)})
	g.Expect(err).NotTo(HaveOccurred())
}

/*
Copyright 2022 The Flux authors

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
	"testing"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKustomizationReconciler_Impersonation(t *testing.T) {
	g := NewWithT(t)
	id := "imp-" + randStringRunes(5)
	revision := "v1.0.0"

	// reset default account
	defer func() {
		reconciler.DefaultServiceAccount = ""
	}()

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "config.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  key: "%[2]s"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: time.Minute},
			Path:     "./",
			KubeConfig: &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
					Name: "kubeconfig",
				},
			},
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Name:      repositoryName.Name,
				Namespace: repositoryName.Namespace,
				Kind:      sourcev1.GitRepositoryKind,
			},
			TargetNamespace: id,
			Prune:           true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	readyCondition := &metav1.Condition{}

	t.Run("reconciles as cluster admin", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(readyCondition.Reason).To(Equal(kustomizev1.ReconciliationSucceededReason))
	})

	t.Run("fails to reconcile impersonating the default service account", func(t *testing.T) {
		reconciler.DefaultServiceAccount = "default"
		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return apimeta.IsStatusConditionFalse(resultK.Status.Conditions, meta.ReadyCondition)
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(readyCondition.Reason).To(Equal(kustomizev1.ReconciliationFailedReason))
		g.Expect(readyCondition.Message).To(ContainSubstring("system:serviceaccount:%s:default", id))
	})

	t.Run("reconciles impersonating service account", func(t *testing.T) {
		sa := corev1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ServiceAccount",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: id,
			},
		}
		g.Expect(k8sClient.Create(context.Background(), &sa)).To(Succeed())

		crb := rbacv1.ClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: id,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "test",
					Namespace: id,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "cluster-admin",
			},
		}
		g.Expect(k8sClient.Create(context.Background(), &crb)).To(Succeed())

		saK := &kustomizev1.Kustomization{}
		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), saK)
		g.Expect(err).NotTo(HaveOccurred())
		saK.Spec.ServiceAccountName = "test"
		err = k8sClient.Update(context.Background(), saK)
		g.Expect(err).NotTo(HaveOccurred())

		revision = "v3.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(readyCondition.Reason).To(Equal(kustomizev1.ReconciliationSucceededReason))
	})

	t.Run("can finalize impersonating service account", func(t *testing.T) {
		saK := &kustomizev1.Kustomization{}
		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), saK)
		g.Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Delete(context.Background(), saK)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())

		resultConfig := &corev1.ConfigMap{}
		err := k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultConfig)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
}

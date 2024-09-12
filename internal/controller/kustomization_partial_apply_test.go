/*
Copyright 2021 The Flux authors

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
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_PartialApply(t *testing.T) {
	g := NewWithT(t)
	id := "partial-apply-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "custom-resource.yaml",
				Body: fmt.Sprintf(`---
apiVersion: example.com/v1
kind: CustomResource
metadata:
  name: %[1]s
spec:
  exampleField: "%[2]s"
`, name, data),
			},
			{
				Name: "secret.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: "%[2]s"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("partial-apply-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("partial-apply-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Path:     "./",
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: meta.SecretKeyReference{
					Name: "kubeconfig",
				},
			},
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Name:      repositoryName.Name,
				Namespace: repositoryName.Namespace,
				Kind:      sourcev1.GitRepositoryKind,
			},
			HealthChecks: []meta.NamespacedObjectKindReference{
				{
					APIVersion: "v1",
					Kind:       "Secret",
					Name:       id,
					Namespace:  id,
				},
			},
			TargetNamespace: id,
			PartialApply:    false,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}

	t.Run("fails to apply resources", func(t *testing.T) {
		artifact, err = testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v1.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return isReconcileFailure(resultK)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)

		t.Run("emits dry-run error event", func(t *testing.T) {
			events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
			g.Expect(len(events) > 0).To(BeTrue())
			g.Expect(events[0].Type).To(BeIdenticalTo("Warning"))
			g.Expect(events[0].Message).To(ContainSubstring("dry-run failed: no matches for kind"))
		})
	})

	t.Run("partially applies secret", func(t *testing.T) {
		artifact, err = testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.PartialApply = true
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			hasCreatedSecret := !apierrors.IsNotFound(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, &corev1.Secret{}))
			return hasCreatedSecret && apimeta.IsStatusConditionFalse(resultK.Status.Conditions, meta.ReadyCondition)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		t.Run("emits partial apply error event", func(t *testing.T) {
			events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
			g.Expect(len(events) > 0).To(BeTrue())
			g.Expect(events[0].Type).To(BeIdenticalTo("Warning"))
			g.Expect(events[0].Message).To(ContainSubstring("error during apply"))
			g.Expect(events[0].Message).To(ContainSubstring("dry-run failed: no matches for kind"))
		})

		g.Expect(resultK.Status.Inventory.Entries).To(ContainElement(kustomizev1.ResourceRef{ID: fmt.Sprintf("%s_%s__Secret", id, id), Version: "v1"}))

		kstatusCheck.CheckErr(ctx, resultK)
	})
}

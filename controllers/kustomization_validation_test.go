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

package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKustomizationReconciler_Validation(t *testing.T) {
	g := NewWithT(t)

	id := "val-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := createArtifact(testServer, "testdata/invalid/plain", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	overlayArtifactName := "val-" + randStringRunes(5)
	overlayChecksum, err := createArtifact(testServer, "testdata/invalid/overlay", overlayArtifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: id,
	}

	overlayRepositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	err = applyGitRepository(overlayRepositoryName, overlayArtifactName, "main/"+overlayChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
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
		},
	}
	g.Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return obj.Status.LastAttemptedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	overlayKustomizationName := fmt.Sprintf("val-%s", randStringRunes(5))
	overlayKs := kustomization.DeepCopy()
	overlayKs.ResourceVersion = ""
	overlayKs.Name = overlayKustomizationName
	overlayKs.Spec.SourceRef.Name = overlayRepositoryName.Name
	overlayKs.Spec.SourceRef.Namespace = overlayRepositoryName.Namespace

	g.Expect(k8sClient.Create(context.TODO(), overlayKs)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(overlayKs), &obj)
		return obj.Status.LastAttemptedRevision == "main/"+overlayChecksum
	}, timeout, time.Second).Should(BeTrue())

	t.Run("fails to build invalid plain yamls", func(t *testing.T) {
		var resultK kustomizev1.Kustomization
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &resultK)
			for _, c := range resultK.Status.Conditions {
				if c.Reason == kustomizev1.BuildFailedReason {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})

	t.Run("fails to build invalid overlay", func(t *testing.T) {
		var resultK kustomizev1.Kustomization
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(overlayKs), &resultK)
			for _, c := range resultK.Status.Conditions {
				if c.Reason == kustomizev1.BuildFailedReason {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})
}

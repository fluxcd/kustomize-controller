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

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKustomizationReconciler_Varsub(t *testing.T) {
	g := NewWithT(t)
	id := "vars-" + randStringRunes(5)
	revision := "v1.0.0/" + randStringRunes(7)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "service-account.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    environment: ${env:=dev}
    region: "${_Region}" 
    zone: "${zone}"
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	url := fmt.Sprintf("%s/%s", testServer.URL(), artifact)

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, url, revision, "")
	g.Expect(err).NotTo(HaveOccurred())

	configName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}
	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configName.Name,
			Namespace: configName.Namespace,
		},
		Data: map[string]string{"zone": "\naz-1a\n"},
	}
	g.Expect(k8sClient.Create(context.Background(), config)).Should(Succeed())

	secretName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName.Name,
			Namespace: secretName.Namespace,
		},
		StringData: map[string]string{"zone": "\naz-1b\n"},
	}
	g.Expect(k8sClient.Create(context.Background(), secret)).Should(Succeed())

	inputK := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: id,
		},
		Spec: kustomizev1.KustomizationSpec{
			KubeConfig: &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
					Name: "kubeconfig",
				},
			},
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Path:     "./",
			Prune:    true,
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Kind: sourcev1.GitRepositoryKind,
				Name: repositoryName.Name,
			},
			PostBuild: &kustomizev1.PostBuild{
				Substitute: map[string]string{"_Region": "eu-central-1"},
				SubstituteFrom: []kustomizev1.SubstituteReference{
					{
						Kind: "ConfigMap",
						Name: configName.Name,
					},
					{
						Kind: "Secret",
						Name: secretName.Name,
					},
				},
			},
			HealthChecks: []meta.NamespacedObjectKindReference{
				{
					APIVersion: "v1",
					Kind:       "ServiceAccount",
					Name:       id,
					Namespace:  id,
				},
			},
		},
	}
	g.Expect(k8sClient.Create(context.Background(), inputK)).Should(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultSA := &corev1.ServiceAccount{}

	t.Run("reconciles successfully", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(inputK), resultK)
			for _, c := range resultK.Status.Conditions {
				if c.Reason == meta.ReconciliationSucceededReason {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultSA)).Should(Succeed())
	})

	t.Run("sets status", func(t *testing.T) {
		g.Expect(resultK.Status.LastAppliedRevision).To(Equal(revision))
		g.Expect(apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)).To(BeTrue())
		g.Expect(apimeta.IsStatusConditionTrue(resultK.Status.Conditions, kustomizev1.HealthyCondition)).To(BeTrue())
	})

	t.Run("replaces vars", func(t *testing.T) {
		g.Expect(resultSA.Labels["environment"]).To(Equal("dev"))
		g.Expect(resultSA.Labels["region"]).To(Equal("eu-central-1"))
		g.Expect(resultSA.Labels["zone"]).To(Equal("az-1b"))
	})

	t.Run("sets owner labels", func(t *testing.T) {
		g.Expect(resultSA.Labels[fmt.Sprintf("%s/name", kustomizev1.GroupVersion.Group)]).To(Equal(client.ObjectKeyFromObject(resultK).Name))
		g.Expect(resultSA.Labels[fmt.Sprintf("%s/namespace", kustomizev1.GroupVersion.Group)]).To(Equal(client.ObjectKeyFromObject(resultK).Namespace))
	})
}

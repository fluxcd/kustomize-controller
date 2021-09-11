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
	"github.com/fluxcd/pkg/apis/kustomize"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"strings"
	"testing"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKustomizationReconciler_KustomizeTransformer(t *testing.T) {
	g := NewWithT(t)
	id := "transformers-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	deployNamespace := "transformers-inline"
	err = createNamespace(deployNamespace)
	g.Expect(err).NotTo(HaveOccurred())

	artifactFile := "patch-" + randStringRunes(5)
	artifactChecksum, err := createArtifact(testServer, "testdata/transformers", artifactFile)
	g.Expect(err).ToNot(HaveOccurred())
	artifactURL, err := testServer.URLForFile(artifactFile)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactURL, "main/"+artifactChecksum, artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      "patch-" + randStringRunes(5),
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
		},
	}

	g.Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	quota := &corev1.ResourceQuota{}

	t.Run("namespace and prefix transformers", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.TODO(), client.ObjectKey{
			Name:      "test-common-transform",
			Namespace: deployNamespace,
		}, quota)).To(Succeed())
	})

	t.Run("replacement transformer", func(t *testing.T) {
		val := quota.Spec.Hard[corev1.ResourceLimitsMemory]
		g.Expect(val.String()).To(Equal("24Gi"))
	})

	t.Run("annotations transformer", func(t *testing.T) {
		g.Expect(quota.Annotations["test"]).To(Equal("annotations"))
	})

	t.Run("label transformer", func(t *testing.T) {
		g.Expect(quota.Labels["test"]).To(Equal("labels"))
	})

	t.Run("configmap generator", func(t *testing.T) {
		var configMapList corev1.ConfigMapList
		g.Expect(k8sClient.List(context.TODO(), &configMapList)).To(Succeed())
		g.Expect(checkConfigMap(&configMapList, "test-metas-transform")).To(Equal(true))
	})

	t.Run("secret generator", func(t *testing.T) {
		var secretList corev1.SecretList
		g.Expect(k8sClient.List(context.TODO(), &secretList)).To(Succeed())
		g.Expect(checkSecret(&secretList, "test-secret-transform")).To(Equal(true))
	})

	deployment := &appsv1.Deployment{}
	g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{
		Name:      "test-podinfo-transform",
		Namespace: deployNamespace,
	}, deployment)).To(Succeed())

	t.Run("patch6902 transformer", func(t *testing.T) {
		g.Expect(deployment.Labels["patch"]).To(Equal("json6902"))
	})

	t.Run("patches transformer", func(t *testing.T) {
		g.Expect(deployment.Labels["app.kubernetes.io/version"]).To(Equal("1.21.0"))
	})

	t.Run("patchStrategicMerge transformer", func(t *testing.T) {
		g.Expect(deployment.Spec.Template.Spec.ServiceAccountName).
			To(Equal("test"))
	})

	t.Run("image transformer", func(t *testing.T) {
		g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).
			To(Equal("podinfo:6.0.0"))
	})

	t.Run("replica transformer", func(t *testing.T) {
		g.Expect(int(*deployment.Spec.Replicas)).
			To(Equal(2))
	})
}

func TestKustomizationReconciler_KustomizeTransformerFiles(t *testing.T) {
	g := NewWithT(t)
	id := "transformers-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	deployNamespace := "transformer-files"
	err = createNamespace(deployNamespace)
	g.Expect(err).NotTo(HaveOccurred())

	artifactFile := "patch-" + randStringRunes(5)
	artifactChecksum, err := createArtifact(testServer, "testdata/file-transformer", artifactFile)
	g.Expect(err).ToNot(HaveOccurred())
	artifactURL, err := testServer.URLForFile(artifactFile)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactURL, "main/"+artifactChecksum, artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      "patch-" + randStringRunes(5),
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
		},
	}

	g.Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	quota := &corev1.ResourceQuota{}

	t.Run("namespace and prefix transformers", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.TODO(), client.ObjectKey{
			Name:      "test-common-transform",
			Namespace: deployNamespace,
		}, quota)).To(Succeed())
	})

	t.Run("replacement transformer", func(t *testing.T) {
		val := quota.Spec.Hard[corev1.ResourceLimitsMemory]
		g.Expect(val.String()).To(Equal("24Gi"))
	})

	t.Run("annotations transformer", func(t *testing.T) {
		g.Expect(quota.Annotations["test"]).To(Equal("annotations"))
	})

	t.Run("label transformer", func(t *testing.T) {
		g.Expect(quota.Labels["test"]).To(Equal("labels"))
	})

	t.Run("configmap generator", func(t *testing.T) {
		var configMapList corev1.ConfigMapList
		g.Expect(k8sClient.List(context.TODO(), &configMapList)).To(Succeed())
		g.Expect(checkConfigMap(&configMapList, "test-metas-transform")).To(Equal(true))
	})

	t.Run("secret generator", func(t *testing.T) {
		var secretList corev1.SecretList
		g.Expect(k8sClient.List(context.TODO(), &secretList)).To(Succeed())
		g.Expect(checkSecret(&secretList, "test-secret-transform")).To(Equal(true))
	})

	deployment := &appsv1.Deployment{}
	g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{
		Name:      "test-podinfo-transform",
		Namespace: deployNamespace,
	}, deployment)).To(Succeed())

	t.Run("patch6902 transformer", func(t *testing.T) {
		g.Expect(deployment.Labels["patch"]).To(Equal("json6902"))
	})

	t.Run("patches transformer", func(t *testing.T) {
		g.Expect(deployment.Labels["app.kubernetes.io/version"]).To(Equal("1.21.0"))
	})

	t.Run("patchStrategicMerge transformer", func(t *testing.T) {
		g.Expect(deployment.Spec.Template.Spec.ServiceAccountName).
			To(Equal("test"))
	})

	t.Run("image transformer", func(t *testing.T) {
		g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).
			To(Equal("podinfo:6.0.0"))
	})

	t.Run("replica transformer", func(t *testing.T) {
		g.Expect(int(*deployment.Spec.Replicas)).
			To(Equal(2))
	})
}

func TestKustomizationReconciler_FluxTransformers(t *testing.T) {
	g := NewWithT(t)
	id := "transformers-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactFile := "patch-" + randStringRunes(5)
	artifactChecksum, err := createArtifact(testServer, "testdata/patch", artifactFile)
	g.Expect(err).ToNot(HaveOccurred())
	artifactURL, err := testServer.URLForFile(artifactFile)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactURL, "main/"+artifactChecksum, artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      "patch-" + randStringRunes(5),
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
			Images: []kustomize.Image{
				{
					Name:    "podinfo",
					NewName: "ghcr.io/stefanprodan/podinfo",
					NewTag:  "5.2.0",
				},
			},
			Patches: []kustomize.Patch{
				{
					Patch: `
- op: add
  path: /metadata/labels/patch1
  value: inline-json
						`,
					Target: kustomize.Selector{
						LabelSelector: "app=podinfo",
					},
				},
				{
					Patch: `
apiVersion: v1
kind: Pod
metadata:
  name: podinfo
  labels:
    patch2: inline-yaml
						`,
				},
			},
			PatchesJSON6902: []kustomize.JSON6902Patch{
				{
					Patch: []kustomize.JSON6902{
						{Op: "add", Path: "/metadata/labels/patch3", Value: &apiextensionsv1.JSON{Raw: []byte(`"json6902"`)}},
						{Op: "replace", Path: "/spec/replicas", Value: &apiextensionsv1.JSON{Raw: []byte("2")}},
					},
					Target: kustomize.Selector{
						Group:   "apps",
						Version: "v1",
						Kind:    "Deployment",
						Name:    "podinfo",
					},
				},
			},
			PatchesStrategicMerge: []apiextensionsv1.JSON{
				{
					Raw: []byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"podinfo","labels":{"patch4":"strategic-merge"}}}`),
				},
			},
		},
	}

	g.Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	var deployment appsv1.Deployment
	g.Expect(k8sClient.Get(context.TODO(), client.ObjectKey{Name: "podinfo", Namespace: id}, &deployment)).To(Succeed())

	t.Run("applies patches", func(t *testing.T) {
		g.Expect(deployment.ObjectMeta.Labels["patch1"]).To(Equal("inline-json"))
		g.Expect(deployment.ObjectMeta.Labels["patch2"]).To(Equal("inline-yaml"))
		g.Expect(deployment.ObjectMeta.Labels["patch3"]).To(Equal("json6902"))
		g.Expect(deployment.ObjectMeta.Labels["patch4"]).To(Equal("strategic-merge"))
		g.Expect(*deployment.Spec.Replicas).To(Equal(int32(2)))
	})
}

func checkConfigMap(list *corev1.ConfigMapList, name string) bool {
	if list == nil {
		return false
	}

	for _, configMap := range list.Items {
		if strings.Contains(configMap.Name, name) {
			return true
		}
	}

	return false
}

func checkSecret(list *corev1.SecretList, name string) bool {
	if list == nil {
		return false
	}

	for _, secret := range list.Items {
		if strings.Contains(secret.Name, name) {
			return true
		}
	}

	return false
}

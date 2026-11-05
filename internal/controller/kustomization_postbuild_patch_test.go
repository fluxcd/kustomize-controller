/*
Copyright 2026 The Flux authors

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

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_PostBuildPatch(t *testing.T) {
	g := NewWithT(t)
	id := "pb-patch-" + randStringRunes(5)
	revision := "v1.0.0/" + randStringRunes(7)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "deployment.yaml",
				Body: fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    app: podinfo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: podinfo
  template:
    metadata:
      labels:
        app: podinfo
    spec:
      containers:
        - name: podinfo
          image: podinfo
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	inputK := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: id,
		},
		Spec: kustomizev1.KustomizationSpec{
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
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
				Patches: []kustomize.Patch{
					{
						Patch: `
- op: add
  path: /metadata/labels/patched
  value: post-build
`,
						Target: &kustomize.Selector{
							LabelSelector: "app=podinfo",
						},
					},
					{
						Patch: fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[1]s
spec:
  replicas: 3
`, id),
					},
				},
			},
		},
	}
	g.Expect(k8sClient.Create(context.Background(), inputK)).Should(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultDeployment := &appsv1.Deployment{}

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

		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultDeployment)).Should(Succeed())
	})

	t.Run("applies strategic merge patch", func(t *testing.T) {
		g.Expect(resultDeployment.Spec.Replicas).NotTo(BeNil())
		g.Expect(*resultDeployment.Spec.Replicas).To(Equal(int32(3)))
	})

	t.Run("applies json6902 patch targeted by label selector", func(t *testing.T) {
		g.Expect(resultDeployment.Labels).To(HaveKeyWithValue("patched", "post-build"))
	})
}

func TestKustomizationReconciler_PostBuildPatchPrecedence(t *testing.T) {
	g := NewWithT(t)
	id := "pb-patch-prec-" + randStringRunes(5)
	revision := "v1.0.0/" + randStringRunes(7)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "configmap.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  namespace: %[1]s
data:
  value: from-source
`, name),
			},
			{
				Name: "kustomization.yaml",
				Body: `---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - configmap.yaml
commonLabels:
  stage: source-transform
`,
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	inputK := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: id,
		},
		Spec: kustomizev1.KustomizationSpec{
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
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
				Patches: []kustomize.Patch{
					{
						Patch: fmt.Sprintf(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    stage: post-build-override
data:
  value: from-post-build
`, id),
					},
				},
			},
		},
	}
	g.Expect(k8sClient.Create(context.Background(), inputK)).Should(Succeed())

	resultK := &kustomizev1.Kustomization{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(inputK), resultK)
		for _, c := range resultK.Status.Conditions {
			if c.Reason == meta.ReconciliationSucceededReason {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())

	cm := &corev1.ConfigMap{}
	g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, cm)).Should(Succeed())
	g.Expect(cm.Data["value"]).To(Equal("from-post-build"))
	g.Expect(cm.Labels).To(HaveKeyWithValue("stage", "post-build-override"))
}

func TestKustomizationReconciler_PostBuildPatchFailure(t *testing.T) {
	g := NewWithT(t)
	id := "pb-patch-fail-" + randStringRunes(5)
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
    app: podinfo
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	inputK := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: id,
		},
		Spec: kustomizev1.KustomizationSpec{
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
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
				Patches: []kustomize.Patch{
					{
						Patch: `
- op: test
  path: /metadata/labels/app
  value: not-podinfo
`,
						Target: &kustomize.Selector{
							LabelSelector: "app=podinfo",
						},
					},
				},
			},
		},
	}
	g.Expect(k8sClient.Create(context.Background(), inputK)).Should(Succeed())

	resultK := &kustomizev1.Kustomization{}
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(inputK), resultK)
		for _, c := range resultK.Status.Conditions {
			if c.Reason == meta.BuildFailedReason {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())

	ready := apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
	g.Expect(ready).NotTo(BeNil())
	g.Expect(ready.Message).To(ContainSubstring("post build patches failed"))
	g.Expect(k8sClient.Delete(context.Background(), resultK)).To(Succeed())
}

func TestKustomizationReconciler_PostBuildPatchOverridesReplacements(t *testing.T) {
	g := NewWithT(t)
	id := "pb-patch-repl-" + randStringRunes(5)
	revision := "v1.0.0/" + randStringRunes(7)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "source.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s-source
  namespace: %[1]s
data:
  value: from-replacement
`, name),
			},
			{
				Name: "target.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s-target
  namespace: %[1]s
data:
  value: placeholder
`, name),
			},
			{
				Name: "kustomization.yaml",
				Body: fmt.Sprintf(`---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - source.yaml
  - target.yaml
replacements:
  - source:
      kind: ConfigMap
      name: %[1]s-source
      fieldPath: data.value
    targets:
      - select:
          kind: ConfigMap
          name: %[1]s-target
        fieldPaths:
          - data.value
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	inputK := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: id,
		},
		Spec: kustomizev1.KustomizationSpec{
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
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
			Patches: []kustomize.Patch{
				{
					Patch: fmt.Sprintf(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s-target
  namespace: %[1]s
data:
  value: from-spec-patch
`, id),
				},
			},
			PostBuild: &kustomizev1.PostBuild{
				Patches: []kustomize.Patch{
					{
						Patch: fmt.Sprintf(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s-target
  namespace: %[1]s
data:
  value: from-post-build
`, id),
					},
				},
			},
		},
	}
	g.Expect(k8sClient.Create(context.Background(), inputK)).Should(Succeed())

	resultK := &kustomizev1.Kustomization{}
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(inputK), resultK)
		for _, c := range resultK.Status.Conditions {
			if c.Reason == meta.ReconciliationSucceededReason {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())

	target := &corev1.ConfigMap{}
	g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id + "-target", Namespace: id}, target)).Should(Succeed())
	g.Expect(target.Data["value"]).To(Equal("from-post-build"))
}

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

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
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
			{
				Name: "secret.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
  namespace: %[1]s
stringData:
  zone: ${zone}
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

	configName := types.NamespacedName{
		Name:      randStringRunes(5),
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
		Name:      randStringRunes(5),
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
	resultSecret := &corev1.Secret{}

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
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultSecret)).Should(Succeed())
	})

	t.Run("sets status", func(t *testing.T) {
		g.Expect(resultK.Status.LastAppliedRevision).To(Equal(revision))
		g.Expect(apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)).To(BeTrue())
		g.Expect(apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.HealthyCondition)).To(BeTrue())
	})

	t.Run("replaces vars", func(t *testing.T) {
		g.Expect(resultSA.Labels["environment"]).To(Equal("dev"))
		g.Expect(resultSA.Labels["region"]).To(Equal("eu-central-1"))
		g.Expect(resultSA.Labels["zone"]).To(Equal("az-1b"))
		g.Expect(string(resultSecret.Data["zone"])).To(Equal("az-1b"))
	})

	t.Run("sets owner labels", func(t *testing.T) {
		g.Expect(resultSA.Labels[fmt.Sprintf("%s/name", kustomizev1.GroupVersion.Group)]).To(Equal(client.ObjectKeyFromObject(resultK).Name))
		g.Expect(resultSA.Labels[fmt.Sprintf("%s/namespace", kustomizev1.GroupVersion.Group)]).To(Equal(client.ObjectKeyFromObject(resultK).Namespace))
	})
}

func TestKustomizationReconciler_VarsubOptional(t *testing.T) {
	ctx := context.Background()

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
				Body: fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    color: "${color:=blue}"
    shape: "${shape:=square}"
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

	configName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configName.Name,
			Namespace: configName.Namespace,
		},
		Data: map[string]string{"color": "\nred\n"},
	}
	g.Expect(k8sClient.Create(ctx, configMap)).Should(Succeed())

	secretName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName.Name,
			Namespace: secretName.Namespace,
		},
		StringData: map[string]string{"shape": "\ntriangle\n"},
	}
	g.Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

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
				Substitute: map[string]string{"var_substitution_enabled": "true"},
				SubstituteFrom: []kustomizev1.SubstituteReference{
					{
						Kind:     "ConfigMap",
						Name:     configName.Name,
						Optional: true,
					},
					{
						Kind:     "Secret",
						Name:     secretName.Name,
						Optional: true,
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
	g.Expect(k8sClient.Create(ctx, inputK)).Should(Succeed())

	resultSA := &corev1.ServiceAccount{}

	ensureReconciles := func(nameSuffix string) {
		t.Run("reconciles successfully"+nameSuffix, func(t *testing.T) {
			g.Eventually(func() bool {
				resultK := &kustomizev1.Kustomization{}
				_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(inputK), resultK)
				for _, c := range resultK.Status.Conditions {
					if c.Reason == meta.ReconciliationSucceededReason {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: id, Namespace: id}, resultSA)).Should(Succeed())
		})
	}

	ensureReconciles(" with optional ConfigMap")
	t.Run("replaces vars from optional ConfigMap", func(t *testing.T) {
		g.Expect(resultSA.Labels["color"]).To(Equal("red"))
		g.Expect(resultSA.Labels["shape"]).To(Equal("triangle"))
	})

	for _, o := range []client.Object{
		configMap,
		secret,
	} {
		g.Expect(k8sClient.Delete(ctx, o)).Should(Succeed())
	}

	// Force a second detectable reconciliation of the Kustomization.
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(inputK), inputK)).Should(Succeed())
	inputK.Status.Conditions = nil
	g.Expect(k8sClient.Status().Update(ctx, inputK)).Should(Succeed())
	ensureReconciles(" without optional ConfigMap")
	t.Run("replaces vars tolerating absent ConfigMap", func(t *testing.T) {
		g.Expect(resultSA.Labels["color"]).To(Equal("blue"))
		g.Expect(resultSA.Labels["shape"]).To(Equal("square"))
	})
}

func TestKustomizationReconciler_VarsubNumberBool(t *testing.T) {
	ctx := context.Background()

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
				Name: "templates.yaml",
				Body: fmt.Sprintf(`
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    id: ${numberStr}
    enabled: ${booleanStr}
  annotations:
    id: ${q}${number}${q}
    enabled: ${q}${boolean}${q}
spec:
  interval: ${number}m
  url: https://host/repo
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  namespace: %[1]s
data:
  id: ${q}${number}${q}
  text: |
    This variable is escaped $${var}

    Lorem ipsum dolor sit amet, consectetur adipiscing elit. Vivamus at
    nisl sem. Nullam nec dui ipsum. Nam vehicula volutpat ipsum, ac fringilla
    nisl convallis sed. Aliquam porttitor turpis finibus, finibus velit ut,
    imperdiet mauris. Cras nec neque nulla. Maecenas semper nulla et elit
    dictum sagittis. Quisque tincidunt non diam non ullamcorper. Curabitur
    pretium urna odio, vitae ullamcorper purus mollis sit amet. Nam ac lectus
    ac arcu varius feugiat id fringilla massa.

    \?
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
				Substitute: map[string]string{
					"q": `"`,

					"numberStr":  "!!str 123",
					"number":     "123",
					"booleanStr": "!!str true",
					"boolean":    "true",
				},
			},
			Wait: false,
		},
	}
	g.Expect(k8sClient.Create(ctx, inputK)).Should(Succeed())

	g.Eventually(func() bool {
		resultK := &kustomizev1.Kustomization{}
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(inputK), resultK)
		for _, c := range resultK.Status.Conditions {
			if c.Reason == meta.ReconciliationSucceededReason {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())

	resultRepo := &sourcev1.GitRepository{}
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: id, Namespace: id}, resultRepo)).Should(Succeed())
	g.Expect(resultRepo.Labels["id"]).To(Equal("123"))
	g.Expect(resultRepo.Annotations["id"]).To(Equal("123"))
	g.Expect(resultRepo.Labels["enabled"]).To(Equal("true"))
	g.Expect(resultRepo.Annotations["enabled"]).To(Equal("true"))

	resultCM := &corev1.ConfigMap{}
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: id, Namespace: id}, resultCM)).Should(Succeed())
	g.Expect(resultCM.Data["id"]).To(Equal("123"))
	g.Expect(resultCM.Data["text"]).To(ContainSubstring(`${var}`))
	g.Expect(resultCM.Data["text"]).ToNot(ContainSubstring(`$${var}`))
	g.Expect(resultCM.Data["text"]).To(ContainSubstring(`\?`))
}

func TestKustomizationReconciler_VarsubStrict(t *testing.T) {
	reconciler.StrictSubstitutions = true
	defer func() {
		reconciler.StrictSubstitutions = false
	}()

	ctx := context.Background()

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
				Body: fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    default: ${default:=test}
    missing: ${missing}
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
				Substitute: map[string]string{
					"test": "test",
				},
			},
			Wait: true,
		},
	}
	g.Expect(k8sClient.Create(ctx, inputK)).Should(Succeed())

	var resultK kustomizev1.Kustomization
	t.Run("fails to reconcile", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(inputK), &resultK)
			for _, c := range resultK.Status.Conditions {
				if c.Reason == meta.BuildFailedReason {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})

	ready := apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
	g.Expect(ready.Message).To(ContainSubstring("variable not set"))
	g.Expect(k8sClient.Delete(context.Background(), &resultK)).To(Succeed())
}

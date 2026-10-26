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

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	"github.com/opencontainers/go-digest"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_ArtifactDownload(t *testing.T) {
	g := NewWithT(t)
	id := "fetch-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
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
		Name:      fmt.Sprintf("fetch-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("fetch-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		Name:      kustomizationKey.Name,
		Namespace: kustomizationKey.Namespace,
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Path:     "./",
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
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
			Force:           false,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	repo := &sourcev1.GitRepository{
		Kind:       sourcev1.GitRepositoryKind,
		APIVersion: sourcev1.GroupVersion.String(),
	}
	g.Expect(k8sClient.Get(context.Background(), repositoryName, repo)).Should(Succeed())
	repoURL := repo.Status.Artifact.URL
	repoDigest := repo.Status.Artifact.Digest

	t.Run("downloads artifact", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition) &&
				resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("retries on not found errors", func(t *testing.T) {
		repo.Status.Artifact.URL = repoURL + "not-found"
		repo.ManagedFields = nil
		g.Expect(k8sClient.Status().Update(context.Background(), repo)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return strings.Contains(ready.Message, "artifact not found")
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("recovers after not found errors", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), repositoryName, repo)).Should(Succeed())
		repo.Status.Artifact.URL = repoURL
		repo.ManagedFields = nil
		g.Expect(k8sClient.Status().Update(context.Background(), repo)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
		}, timeout, time.Second).Should(BeTrue())
	})

	// Advertise the digest of a rebuilt artifact while the old one is still served.
	rebuilt, err := testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")
	rebuiltData, err := os.ReadFile(filepath.Join(testServer.Root(), rebuilt))
	g.Expect(err).NotTo(HaveOccurred())
	rebuiltDigest := digest.SHA256.FromBytes(rebuiltData).String()
	g.Expect(rebuiltDigest).NotTo(Equal(repoDigest))

	t.Run("retries on digest mismatch errors", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), repositoryName, repo)).Should(Succeed())
		repo.Status.Artifact.Digest = rebuiltDigest
		repo.ManagedFields = nil
		g.Expect(k8sClient.Status().Update(context.Background(), repo)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return ready != nil &&
				ready.Status == metav1.ConditionFalse &&
				ready.Reason == meta.ArtifactFailedReason &&
				strings.Contains(ready.Message, "artifact digest changed, retrying in")
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("recovers after digest mismatch errors", func(t *testing.T) {
		// Serve the rebuilt artifact at the old URL without changing the source,
		// the Kustomization must recover on requeue.
		artifactName := strings.TrimPrefix(repoURL, testServer.URL()+"/")
		g.Expect(os.WriteFile(filepath.Join(testServer.Root(), artifactName), rebuiltData, 0o644)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
		}, timeout, time.Second).Should(BeTrue())
	})
}

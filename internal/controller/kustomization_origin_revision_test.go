/*
Copyright 2025 The Flux authors

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

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_OriginRevision(t *testing.T) {
	g := NewWithT(t)
	id := "force-" + randStringRunes(5)
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
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision,
		withGitRepoArtifactMetadata(OCIArtifactOriginRevisionAnnotation, "orev"))
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("force-%s", randStringRunes(5)),
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
				SecretRef: &meta.SecretKeyReference{
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

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	readyCondition := &metav1.Condition{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	g.Expect(readyCondition.Reason).To(Equal(meta.ReconciliationSucceededReason))

	g.Expect(resultK.Status.LastAppliedOriginRevision).To(Equal("orev"))

	g.Expect(resultK.Status.History).To(HaveLen(1))
	g.Expect(resultK.Status.History[0].Digest).ToNot(BeEmpty())
	g.Expect(resultK.Status.History[0].TotalReconciliations).To(BeEquivalentTo(1))
	g.Expect(resultK.Status.History[0].FirstReconciled.Time).ToNot(BeZero())
	g.Expect(resultK.Status.History[0].LastReconciled.Time).ToNot(BeZero())
	g.Expect(resultK.Status.History[0].LastReconciledStatus).To(Equal(meta.ReconciliationSucceededReason))
	g.Expect(resultK.Status.History[0].LastReconciledDuration.Duration).To(BeNumerically(">", 0))
	g.Expect(resultK.Status.History[0].Metadata).To(ContainElements(revision, "orev"))

	events := getEvents(kustomizationKey.Name, nil)
	g.Expect(events).To(Not(BeEmpty()))

	annotationKey := kustomizev1.GroupVersion.Group + "/" + eventv1.MetaOriginRevisionKey
	for _, e := range events {
		g.Expect(e.GetAnnotations()).To(HaveKeyWithValue(annotationKey, "orev"))
	}
}

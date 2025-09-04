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
	"os"
	"path/filepath"
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

func TestKustomizationReconciler_ExternalArtifact(t *testing.T) {
	g := NewWithT(t)
	id := "ea-" + randStringRunes(5)
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

	eaName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyExternalArtifact(eaName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("ea-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: time.Hour},
			Path:     "./",
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
					Name: "kubeconfig",
				},
			},
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Name:      eaName.Name,
				Namespace: eaName.Namespace,
				Kind:      sourcev1.ExternalArtifactKind,
			},
			TargetNamespace: id,
			Wait:            true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	readyCondition := &metav1.Condition{}

	t.Run("reconciles from external artifact source", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(readyCondition.Reason).To(Equal(meta.ReconciliationSucceededReason))
		g.Expect(resultK.Status.LastAppliedRevision).To(Equal(revision))

		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events) > 2).To(BeTrue())
		g.Expect(events[0].Reason).To(BeIdenticalTo(meta.ProgressingReason))
		g.Expect(events[0].Message).To(ContainSubstring("created"))
		g.Expect(events[1].Reason).To(BeIdenticalTo(meta.ProgressingReason))
		g.Expect(events[1].Message).To(ContainSubstring("check passed"))
		g.Expect(events[2].Reason).To(BeIdenticalTo(meta.ReconciliationSucceededReason))
		g.Expect(events[2].Message).To(ContainSubstring("finished"))
	})

	t.Run("watches for external artifact revision change", func(t *testing.T) {
		newRev := "v2.0.0"
		err = applyExternalArtifact(eaName, artifact, newRev)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == newRev
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(resultK.Status.History).To(HaveLen(1))
		g.Expect(resultK.Status.History[0].TotalReconciliations).To(BeEquivalentTo(2))
		g.Expect(resultK.Status.History[0].LastReconciledStatus).To(Equal(meta.ReconciliationSucceededReason))
		g.Expect(resultK.Status.History[0].Metadata).To(ContainElements(newRev))
	})
}

func applyExternalArtifact(objKey client.ObjectKey, artifactName string, revision string) error {
	ea := &sourcev1.ExternalArtifact{
		TypeMeta: metav1.TypeMeta{
			Kind:       sourcev1.ExternalArtifactKind,
			APIVersion: sourcev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      objKey.Name,
			Namespace: objKey.Namespace,
		},
	}

	b, _ := os.ReadFile(filepath.Join(testServer.Root(), artifactName))
	dig := digest.SHA256.FromBytes(b)

	url := fmt.Sprintf("%s/%s", testServer.URL(), artifactName)

	status := sourcev1.ExternalArtifactStatus{
		Conditions: []metav1.Condition{
			{
				Type:               meta.ReadyCondition,
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             meta.SucceededReason,
			},
		},
		Artifact: &meta.Artifact{
			Path:           url,
			URL:            url,
			Revision:       revision,
			Digest:         dig.String(),
			LastUpdateTime: metav1.Now(),
		},
	}

	patchOpts := []client.PatchOption{
		client.ForceOwnership,
		client.FieldOwner("kustomize-controller"),
	}

	if err := k8sClient.Patch(context.Background(), ea, client.Apply, patchOpts...); err != nil {
		return err
	}

	ea.ManagedFields = nil
	ea.Status = status

	statusOpts := &client.SubResourcePatchOptions{
		PatchOptions: client.PatchOptions{
			FieldManager: "source-controller",
		},
	}

	if err := k8sClient.Status().Patch(context.Background(), ea, client.Apply, statusOpts); err != nil {
		return err
	}
	return nil
}

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

// This test extends the existing SOPS decryption error redaction (see
// safeDecrypt in internal/decryptor) to spec.postBuild.substituteFrom
// values: when a substituted value causes a server-side-apply dry-run
// validation error (e.g. a label value that exceeds the 63-byte Kubernetes
// limit), the value must be masked out of the Kustomization's
// .status.conditions[].message and the corresponding Kubernetes Event,
// rather than being echoed back verbatim.
//
// It only exercises the kustomize-controller code path (build -> substitute
// -> SSA dry-run -> status/event), using a synthetic placeholder value in
// place of a real secret.

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

func TestKustomizationReconciler_SubstituteFromRedactedOnValidationError(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	id := "redact-" + randStringRunes(5)
	revision := "v1.0.0/" + randStringRunes(7)

	g.Expect(createNamespace(id)).To(Succeed())
	g.Expect(createKubeConfigSecret(id)).To(Succeed())

	// Synthetic placeholder value standing in for a substituted secret. It
	// is longer than the Kubernetes label-value limit of 63 chars, so that
	// substituting it into metadata.labels causes an SSA dry-run validation
	// error that echoes the offending value back.
	const secretKey = "TOKEN"
	syntheticSecretValue := "SYNTHETIC-PLACEHOLDER-VALUE-" + randStringRunes(61)
	g.Expect(len(syntheticSecretValue)).To(BeNumerically(">", 63))

	secretName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName.Name,
			Namespace: secretName.Namespace,
		},
		StringData: map[string]string{secretKey: syntheticSecretValue},
	}
	g.Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	// Manifest that substitutes the secret value directly into a label,
	// which the Kubernetes API server will reject on dry-run apply.
	manifests := []testserver.File{
		{
			Name: "configmap.yaml",
			Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  namespace: %[1]s
  labels:
    injected: "${%[2]s}"
data:
  foo: bar
`, id, secretKey),
		},
	}

	artifact, err := testServer.ArtifactFromFiles(manifests)
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}
	g.Expect(applyGitRepository(repositoryName, artifact, revision)).To(Succeed())

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
				SubstituteFrom: []kustomizev1.SubstituteReference{
					{
						Kind: "Secret",
						Name: secretName.Name,
					},
				},
			},
		},
	}
	g.Expect(k8sClient.Create(ctx, inputK)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	g.Eventually(func() bool {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(inputK), resultK)
		return isReconcileFailure(resultK)
	}, timeout, interval).Should(BeTrue(), "expected reconciliation to fail due to invalid label value")

	readyCond := apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
	g.Expect(readyCond).NotTo(BeNil())

	t.Logf("Ready condition message: %s", readyCond.Message)

	// The raw substituted value must never appear in the status message.
	g.Expect(readyCond.Message).NotTo(ContainSubstring(syntheticSecretValue))
	// The message should still be informative, with the value redacted.
	g.Expect(readyCond.Message).To(ContainSubstring("*****"))

	events := getEvents(resultK.Name, nil)
	g.Expect(events).NotTo(BeEmpty())
	for _, e := range events {
		g.Expect(e.Message).NotTo(ContainSubstring(syntheticSecretValue))
	}
}

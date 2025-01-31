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

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_InvalidCELExpression(t *testing.T) {
	g := NewWithT(t)
	id := "invalid-config-" + randStringRunes(5)
	revision := "v1.0.0"
	resultK := &kustomizev1.Kustomization{}
	timeout := 60 * time.Second

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "config.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data: {}
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("invalid-config-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("invalid-config-%s", randStringRunes(5)),
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
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Name:      repositoryName.Name,
				Namespace: repositoryName.Namespace,
				Kind:      sourcev1.GitRepositoryKind,
			},
			TargetNamespace: id,
			Prune:           true,
			Timeout:         &metav1.Duration{Duration: time.Second},
			Wait:            true,
			HealthCheckExprs: []kustomize.CustomHealthCheck{{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				HealthCheckExpressions: kustomize.HealthCheckExpressions{
					InProgress: "foo.",
					Current:    "true",
				},
			}},
		},
	}

	err = k8sClient.Create(context.Background(), kustomization)
	g.Expect(err).NotTo(HaveOccurred())

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return conditions.IsFalse(resultK, meta.ReadyCondition)
	}, timeout, time.Second).Should(BeTrue())
	logStatus(t, resultK)

	g.Expect(resultK.Status.ObservedGeneration).To(Equal(resultK.GetGeneration()))

	g.Expect(conditions.IsTrue(resultK, meta.StalledCondition)).To(BeTrue())
	for _, cond := range []string{meta.ReadyCondition, meta.StalledCondition} {
		g.Expect(conditions.GetReason(resultK, cond)).To(Equal(meta.InvalidCELExpressionReason))
		g.Expect(conditions.GetMessage(resultK, cond)).To(ContainSubstring(
			"failed to create custom status reader for healthchecks[0]: failed to parse the expression InProgress: failed to parse the CEL expression 'foo.': ERROR: <input>:1:5: Syntax error: no viable alternative at input '.'"))
	}
}

/*
Copyright 2020 The Flux authors

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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
)

var _ = Describe("KustomizationReconciler", func() {
	const (
		timeout                = time.Second * 30
		interval               = time.Second * 1
		reconciliationInterval = time.Second * 5
	)

	Context("Kustomization", func() {
		var (
			namespace  *corev1.Namespace
			httpServer *testserver.ArtifactServer
			err        error
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "kustomization-test" + randStringRunes(5)},
			}
			err = k8sClient.Create(context.Background(), namespace)
			Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

			httpServer, err = testserver.NewTempArtifactServer()
			Expect(err).NotTo(HaveOccurred())
			httpServer.Start()
		})

		AfterEach(func() {
			err = k8sClient.Delete(context.Background(), namespace)
			Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
			httpServer.Stop()
		})

		type refTestCase struct {
			artifacts      []testserver.File
			waitForReason  string
			expectStatus   corev1.ConditionStatus
			expectMessage  string
			expectRevision string
		}

		DescribeTable("Kustomization tests", func(t refTestCase) {
			artifact, err := httpServer.ArtifactFromFiles(t.artifacts)
			Expect(err).NotTo(HaveOccurred())

			url := fmt.Sprintf("%s/%s", httpServer.URL(), artifact)

			repositoryName := types.NamespacedName{
				Name:      fmt.Sprintf("%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			repository := &sourcev1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repositoryName.Name,
					Namespace: repositoryName.Namespace,
				},
				Spec: sourcev1.GitRepositorySpec{
					URL:      "https://github.com/test/repository",
					Interval: metav1.Duration{Duration: reconciliationInterval},
				},
				Status: sourcev1.GitRepositoryStatus{
					Conditions: []meta.Condition{
						{
							Type:               meta.ReadyCondition,
							Status:             corev1.ConditionTrue,
							LastTransitionTime: metav1.Now(),
							Reason:             sourcev1.GitOperationSucceedReason,
						},
					},
					URL: url,
					Artifact: &sourcev1.Artifact{
						Path:           url,
						URL:            url,
						Revision:       t.expectRevision,
						LastUpdateTime: metav1.Now(),
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), repository)).Should(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), repository)).Should(Succeed())
			defer k8sClient.Delete(context.Background(), repository)

			kName := types.NamespacedName{
				Name:      fmt.Sprintf("%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			k := &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kName.Name,
					Namespace: kName.Namespace,
				},
				Spec: kustomizev1.KustomizationSpec{
					Interval: metav1.Duration{Duration: reconciliationInterval},
					Path:     "./",
					Prune:    true,
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Kind: sourcev1.GitRepositoryKind,
						Name: repository.Name,
					},
					Suspend:    false,
					Timeout:    nil,
					Validation: "client",
				},
			}
			Expect(k8sClient.Create(context.Background(), k)).Should(Succeed())
			defer k8sClient.Delete(context.Background(), k)

			got := &kustomizev1.Kustomization{}
			var cond meta.Condition
			Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), kName, got)
				for _, c := range got.Status.Conditions {
					if c.Reason == t.waitForReason {
						cond = c
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			Expect(cond.Status).To(Equal(t.expectStatus))
			Expect(got.Status.LastAppliedRevision).To(Equal(t.expectRevision))
		},
			Entry("namespace-sa", refTestCase{
				artifacts: []testserver.File{
					{
						Name: "namespace.yaml",
						Body: `---
apiVersion: v1
kind: Namespace
metadata:
  name: test
`,
					},
					{
						Name: "service-account.yaml",
						Body: `---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
  namespace: test
`,
					},
				},
				waitForReason:  meta.ReconciliationSucceededReason,
				expectStatus:   corev1.ConditionTrue,
				expectRevision: "branch/commit1",
			}),
		)
	})

})

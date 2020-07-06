package controllers

import (
	"context"
	"fmt"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1alpha1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1alpha1"
	"github.com/fluxcd/source-controller/pkg/testserver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"
)

var _ = Describe("KustomizationReconciler", func() {
	const (
		timeout       = time.Second * 30
		interval      = time.Second * 1
		indexInterval = time.Second * 1
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
		})

		AfterEach(func() {
			err = k8sClient.Delete(context.Background(), namespace)
			Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
		})

		type refTestCase struct {
			artifacts      string
			waitForReason  string
			expectStatus   corev1.ConditionStatus
			expectMessage  string
			expectRevision string
		}

		DescribeTable("Kustomization tests", func(t refTestCase) {
			httpServer.Start()
			defer httpServer.Stop()

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
					Interval: metav1.Duration{Duration: indexInterval},
				},
				Status: sourcev1.GitRepositoryStatus{
					Conditions: []sourcev1.SourceCondition{
						{
							Type:               sourcev1.ReadyCondition,
							Status:             corev1.ConditionTrue,
							LastTransitionTime: metav1.Now(),
							Reason:             sourcev1.GitOperationSucceedReason,
							Message:            "",
						},
					},
					URL: "some/url",
					Artifact: &sourcev1.Artifact{
						Path:           "some/path",
						URL:            "some/url",
						Revision:       "branch/commit",
						LastUpdateTime: metav1.Time{},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), repository)).Should(Succeed())
			defer k8sClient.Delete(context.Background(), repository)

		},
			Entry("namespace", refTestCase{
				artifacts:      "",
				waitForReason:  kustomizev1.ApplySucceedReason,
				expectStatus:   corev1.ConditionTrue,
				expectRevision: "branch/commit",
			}),
		)
	})

})

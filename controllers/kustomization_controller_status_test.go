package controllers

import (
	"context"
	"fmt"
	"os"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

const StalledCondition = "Stalled"

var _ = Describe("KustomizationReconciler", func() {
	var (
		artifactServer *testserver.ArtifactServer
	)

	const (
		reconciliationInterval = time.Second * 5
		timeout                = time.Second * 30
	)

	BeforeEach(func() {
		var err error
		artifactServer, err = testserver.NewTempArtifactServer()
		Expect(err).ToNot(HaveOccurred())
		artifactServer.Start()
	})

	AfterEach(func() {
		artifactServer.Stop()
		os.RemoveAll(artifactServer.Root())
	})

	Context("Kustomize status", func() {
		var (
			namespace     *corev1.Namespace
			kubeconfig    *kustomizev1.KubeConfig
			artifactFile  string
			artifactURL   string
			kustomization *kustomizev1.Kustomization
			gitRepoKey    client.ObjectKey
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "status-" + randStringRunes(5)
			Expect(k8sClient.Create(context.Background(), namespace)).To(Succeed())

			kubecfgSecret, err := kubeConfigSecret()
			Expect(err).ToNot(HaveOccurred())
			kubecfgSecret.Namespace = namespace.Name
			Expect(k8sClient.Create(context.Background(), kubecfgSecret)).To(Succeed())
			kubeconfig = &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
					Name: kubecfgSecret.Name,
				},
			}

			artifactFile = "status-" + randStringRunes(5)
			_, err = initArtifact(artifactServer, "testdata/patch", artifactFile)
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifactFile)
			Expect(err).ToNot(HaveOccurred())

			gitRepoKey = client.ObjectKey{
				Name:      fmt.Sprintf("status-%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}

			kustomizationKey := types.NamespacedName{
				Name:      "status-" + randStringRunes(5),
				Namespace: namespace.Name,
			}
			kustomization = &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kustomizationKey.Name,
					Namespace: kustomizationKey.Namespace,
				},
				Spec: kustomizev1.KustomizationSpec{
					Interval:   metav1.Duration{Duration: 5 * time.Second},
					Path:       "./",
					KubeConfig: kubeconfig,
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Name:      gitRepoKey.Name,
						Namespace: gitRepoKey.Namespace,
						Kind:      sourcev1.GitRepositoryKind,
					},
					TargetNamespace: namespace.Name,
				},
			}
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("should be progressing when reconciliation is just starting", func() {
			gitRepo := readyGitRepository(gitRepoKey, artifactURL, "", "")
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())
			defer k8sClient.Delete(context.TODO(), gitRepo)

			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())
			defer k8sClient.Delete(context.TODO(), kustomization)
			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				content, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
				unstructured := unstructured.Unstructured{}
				unstructured.SetUnstructuredContent(content)
				res, _ := status.Compute(&unstructured)
				return apimeta.IsStatusConditionPresentAndEqual(obj.Status.Conditions, meta.ReadyCondition, metav1.ConditionUnknown) && (res.Status == status.InProgressStatus)
			}, timeout, time.Second).Should(BeTrue())
		})

		It("should be stalled when sourceref is not found", func() {
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())
			defer k8sClient.Delete(context.TODO(), kustomization)

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				content, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
				unstructured := unstructured.Unstructured{}
				unstructured.SetUnstructuredContent(content)
				res, _ := status.Compute(&unstructured)
				return apimeta.IsStatusConditionTrue(obj.Status.Conditions, StalledCondition) && (res.Status == status.FailedStatus)
			}, timeout, time.Second).Should(BeTrue())
		})

		It("should be stalled when kustomization was previously working then fails", func() {
			gitRepo := readyGitRepository(gitRepoKey, artifactURL, "", "")
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())
			defer k8sClient.Delete(context.TODO(), kustomization)

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)

				content, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
				unstructured := unstructured.Unstructured{}
				unstructured.SetUnstructuredContent(content)
				res, _ := status.Compute(&unstructured)

				isReady := apimeta.IsStatusConditionTrue(obj.Status.Conditions, meta.ReadyCondition)
				stalledCondition := apimeta.FindStatusCondition(obj.Status.Conditions, StalledCondition)
				return isReady && (res.Status == status.CurrentStatus) && (stalledCondition == nil)
			}, timeout, time.Second).Should(BeTrue())

			// delete git repository to cause failure on next reconciliation
			Expect(k8sClient.Delete(context.TODO(), gitRepo)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				content, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
				unstructured := unstructured.Unstructured{}
				unstructured.SetUnstructuredContent(content)
				res, _ := status.Compute(&unstructured)
				return apimeta.IsStatusConditionTrue(obj.Status.Conditions, StalledCondition) && (res.Status == status.FailedStatus)
			}, timeout, time.Second).Should(BeTrue())
		})

		It("should be ready it was failing previously then works", func() {
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())
			defer k8sClient.Delete(context.TODO(), kustomization)

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				content, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
				unstructured := unstructured.Unstructured{}
				unstructured.SetUnstructuredContent(content)
				res, _ := status.Compute(&unstructured)
				return apimeta.IsStatusConditionTrue(obj.Status.Conditions, StalledCondition) && (res.Status == status.FailedStatus)
			}, timeout, time.Second).Should(BeTrue())

			gitRepo := readyGitRepository(gitRepoKey, artifactURL, "", "")
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)

				content, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
				unstructured := unstructured.Unstructured{}
				unstructured.SetUnstructuredContent(content)
				res, _ := status.Compute(&unstructured)

				isReady := apimeta.IsStatusConditionTrue(obj.Status.Conditions, meta.ReadyCondition)
				stalledCondition := apimeta.FindStatusCondition(obj.Status.Conditions, StalledCondition)
				return isReady && (res.Status == status.CurrentStatus) && (stalledCondition == nil)
			}, timeout, time.Second).Should(BeTrue())
		})
	})
})

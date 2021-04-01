/*
Copyright 2021it com The Flux authors

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
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

const timeout = 10 * time.Second

var _ = Describe("KustomizationReconciler", func() {
	var (
		artifactServer *testserver.ArtifactServer
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

	Context("SOPS decryption", func() {
		var (
			namespace        *corev1.Namespace
			kubeconfig       *kustomizev1.KubeConfig
			artifactFile     string
			artifactChecksum string
			artifactURL      string
		)
		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "sops-" + randStringRunes(5)
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

			artifactFile = "sops-test-" + randStringRunes(5)
			artifactChecksum, err = initArtifact(artifactServer, "testdata/sops", artifactFile)
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifactFile)
			Expect(err).ToNot(HaveOccurred())

			gitRepoKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			gitRepo := &sourcev1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      gitRepoKey.Name,
					Namespace: gitRepoKey.Namespace,
				},
				Spec: sourcev1.GitRepositorySpec{
					URL:      "https://github.com/test/repository",
					Interval: metav1.Duration{Duration: time.Minute},
				},
				Status: sourcev1.GitRepositoryStatus{
					Conditions: []metav1.Condition{
						{
							Type:               meta.ReadyCondition,
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.Now(),
							Reason:             sourcev1.GitOperationSucceedReason,
						},
					},
					Artifact: &sourcev1.Artifact{
						Path:           artifactURL,
						URL:            artifactURL,
						Revision:       "main/" + artifactChecksum,
						Checksum:       artifactChecksum,
						LastUpdateTime: metav1.Now(),
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			pgpKey, err := ioutil.ReadFile("testdata/sops/pgp.asc")
			Expect(err).ToNot(HaveOccurred())
			ageKey, err := ioutil.ReadFile("testdata/sops/age.txt")
			Expect(err).ToNot(HaveOccurred())
			sopsSecretKey := types.NamespacedName{
				Name:      "sops-" + randStringRunes(5),
				Namespace: namespace.Name,
			}
			sopsSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sopsSecretKey.Name,
					Namespace: sopsSecretKey.Namespace,
				},
				StringData: map[string]string{
					"pgp.asc":    string(pgpKey),
					"age.agekey": string(ageKey),
				},
			}
			Expect(k8sClient.Create(context.Background(), sopsSecret)).To(Succeed())

			kustomizationKey := types.NamespacedName{
				Name:      "sops-" + randStringRunes(5),
				Namespace: namespace.Name,
			}
			kustomization := &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kustomizationKey.Name,
					Namespace: kustomizationKey.Namespace,
				},
				Spec: kustomizev1.KustomizationSpec{
					Path:       "./",
					KubeConfig: kubeconfig,
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Name:      gitRepoKey.Name,
						Namespace: gitRepoKey.Namespace,
						Kind:      sourcev1.GitRepositoryKind,
					},
					Decryption: &kustomizev1.Decryption{
						Provider: "sops",
						SecretRef: &meta.LocalObjectReference{
							Name: sopsSecretKey.Name,
						},
					},
					PostBuild: &kustomizev1.PostBuild{
						Substitute: map[string]string{
							"FIXTURE_NS": namespace.Name,
						},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				k8sClient.Get(context.TODO(), kustomizationKey, &obj)
				return obj.Status.LastAppliedRevision == gitRepo.Status.Artifact.Revision
			}, timeout, time.Second).Should(BeTrue())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("decrypts SOPS secrets", func() {
			var pgpSecret corev1.Secret
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-pgp", Namespace: namespace.Name}, &pgpSecret)).To(Succeed())
			Expect(pgpSecret.Data["secret"]).To(Equal([]byte(`my-sops-pgp-secret`)))

			var ageSecret corev1.Secret
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-age", Namespace: namespace.Name}, &ageSecret)).To(Succeed())
			Expect(ageSecret.Data["secret"]).To(Equal([]byte(`my-sops-age-secret`)))
		})
	})
})

func initArtifact(artifactServer *testserver.ArtifactServer, fixture, path string) (string, error) {
	if f, err := os.Stat(fixture); os.IsNotExist(err) || !f.IsDir() {
		return "", fmt.Errorf("invalid fixture path: %s", fixture)
	}
	f, err := os.Create(filepath.Join(artifactServer.Root(), path))
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.Remove(f.Name())
		}
	}()

	h := sha1.New()

	mw := io.MultiWriter(h, f)
	gw := gzip.NewWriter(mw)
	tw := tar.NewWriter(gw)

	if err = filepath.Walk(fixture, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Ignore anything that is not a file (directories, symlinks)
		if !fi.Mode().IsRegular() {
			return nil
		}

		// Ignore dotfiles
		if strings.HasPrefix(fi.Name(), ".") {
			return nil
		}

		header, err := tar.FileInfoHeader(fi, p)
		if err != nil {
			return err
		}
		// The name needs to be modified to maintain directory structure
		// as tar.FileInfoHeader only has access to the base name of the file.
		// Ref: https://golang.org/src/archive/tar/common.go?#L626
		relFilePath := p
		if filepath.IsAbs(fixture) {
			relFilePath, err = filepath.Rel(fixture, p)
			if err != nil {
				return err
			}
		}
		header.Name = relFilePath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(p)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}); err != nil {
		return "", err
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		f.Close()
		return "", err
	}
	if err := gw.Close(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	if err := os.Chmod(f.Name(), 0644); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

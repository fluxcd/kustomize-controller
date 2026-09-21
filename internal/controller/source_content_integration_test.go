/*
Copyright 2026 The Flux authors

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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

// TestKustomizationContentChangeIntegration verifies watch-to-apply propagation
// with an unchanged Git revision and hour-long reconciliation interval.
func TestKustomizationContentChangeIntegration(t *testing.T) {
	g := NewWithT(t)
	namespace, err := testEnv.CreateNamespace(ctx, "content-change")
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() { g.Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
	dir := t.TempDir()
	publish := func(value string) *meta.Artifact {
		name := namespace.Name + "-" + value + ".tar.gz"
		manifest := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: content-result\n  namespace: %s\ndata:\n  value: %s\n", namespace.Name, value)
		g.Expect(os.WriteFile(filepath.Join(dir, "configmap.yaml"), []byte(manifest), 0o644)).To(Succeed())
		digest, err := testServer.ArtifactFromDir(dir, name)
		g.Expect(err).NotTo(HaveOccurred())
		return &meta.Artifact{
			Path: name, URL: testServer.URL() + "/" + name, Digest: "sha256:" + digest,
			Revision: "main@sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LastUpdateTime: metav1.Now(),
		}
	}
	source := &sourcev1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "content-source", Namespace: namespace.Name},
		Spec:       sourcev1.GitRepositorySpec{URL: "https://example.invalid/not-fetched", Interval: metav1.Duration{Duration: time.Hour}},
	}
	g.Expect(k8sClient.Create(ctx, source)).To(Succeed())
	t.Cleanup(func() { g.Expect(k8sClient.Delete(ctx, source)).To(Succeed()) })
	source.Status.Artifact = publish("before")
	g.Expect(k8sClient.Status().Update(ctx, source)).To(Succeed())
	k := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{Name: "content-consumer", Namespace: namespace.Name},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: time.Hour}, Prune: true, Path: "./",
			SourceRef: kustomizev1.CrossNamespaceSourceReference{Kind: sourcev1.GitRepositoryKind, Name: source.Name},
		},
	}
	g.Expect(k8sClient.Create(ctx, k)).To(Succeed())
	t.Cleanup(func() { g.Expect(k8sClient.Delete(ctx, k)).To(Succeed()) })
	readValue := func() string {
		var cm corev1.ConfigMap
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: "content-result"}, &cm); err != nil {
			return ""
		}
		return cm.Data["value"]
	}
	expectedRevision := source.Status.Artifact.Revision
	applied := func(value string) bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(k), k); err != nil {
			return false
		}
		return readValue() == value && conditions.IsReady(k) && !conditions.IsReconciling(k) &&
			k.Status.ObservedGeneration == k.Generation &&
			k.Status.LastAppliedRevision == expectedRevision &&
			k.Status.LastAttemptedRevision == expectedRevision
	}
	g.Eventually(func() bool { return applied("before") }, 30*time.Second, 100*time.Millisecond).Should(BeTrue())
	revision := k.Status.LastAppliedRevision
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(source), source)).To(Succeed())
	source.Status.Artifact = publish("after")
	g.Expect(source.Status.Artifact.Revision).To(Equal(revision))
	g.Expect(k8sClient.Status().Update(ctx, source)).To(Succeed())
	t.Log("published changed source artifact after complete initial application")
	g.Eventually(readValue, 30*time.Second, 100*time.Millisecond).Should(Equal("after"))
	g.Eventually(func() bool { return applied("after") }, 30*time.Second, 100*time.Millisecond).Should(BeTrue())
	g.Expect(k.Status.LastAppliedRevision).To(Equal(revision))
	version := k.ResourceVersion
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(source), source)).To(Succeed())
	source.Status.Artifact.LastUpdateTime = metav1.Now()
	g.Expect(k8sClient.Status().Update(ctx, source)).To(Succeed())
	g.Consistently(func() string {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(k), k)).To(Succeed())
		return k.ResourceVersion
	}, time.Second, 100*time.Millisecond).Should(Equal(version))
}

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

package controllers

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	timeout                = time.Second * 30
	interval               = time.Second * 1
	reconciliationInterval = time.Second * 5
)

var (
	cfg          *rest.Config
	k8sClient    client.Client
	testEnv      *envtest.Environment
	testServer   *testserver.ArtifactServer
	testEventsH  controller.Events
	testMetricsH controller.Metrics
	ctx          = ctrl.SetupSignalHandler()
	kubeConfig   []byte
)

// TODO: port this to github.com/fluxcd/pkg/runtime/testenv once testenv can generate kubeconfigs for admin users
func TestMain(m *testing.M) {
	var err error
	utilruntime.Must(sourcev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(kustomizev1.AddToScheme(scheme.Scheme))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	testServer, err = testserver.NewTempArtifactServer()
	if err != nil {
		panic(fmt.Sprintf("Failed to create a temporary storage server: %v", err))
	}
	fmt.Println("Starting the test storage server")
	testServer.Start()

	cfg, err = testEnv.Start()

	user, err := testEnv.ControlPlane.AddUser(envtest.User{
		Name:   "envtest-admin",
		Groups: []string{"system:masters"},
	}, nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create envtest-admin user: %v", err))
	}

	kubeConfig, err = user.KubeConfig()
	if err != nil {
		panic(fmt.Sprintf("Failed to create the envtest-admin user kubeconfig: %v", err))
	}

	// client with caching disabled
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})

	testEventsH = controller.MakeEvents(k8sManager, "kustomize-controller-test", nil)
	testMetricsH = controller.MustMakeMetrics(k8sManager)

	if err := (&KustomizationReconciler{
		Client:          k8sManager.GetClient(),
		EventRecorder:   testEventsH.EventRecorder,
		MetricsRecorder: testMetricsH.MetricsRecorder,
	}).SetupWithManager(k8sManager, KustomizationReconcilerOptions{MaxConcurrentReconciles: 1}); err != nil {
		panic(fmt.Sprintf("Failed to start GitRepositoryReconciler: %v", err))
	}

	go func() {
		fmt.Println("Starting the test environment")
		if err := k8sManager.Start(ctx); err != nil {
			panic(fmt.Sprintf("Failed to start the test environment manager: %v", err))
		}
	}()
	<-k8sManager.Elected()

	code := m.Run()

	events := &corev1.EventList{}
	_ = k8sClient.List(ctx, events)
	for _, event := range events.Items {
		fmt.Println(fmt.Sprintf("%s %s \n%s\n",
			event.InvolvedObject.Name, event.GetAnnotations()["kustomize.toolkit.fluxcd.io/revision"],
			event.Message))
	}

	fmt.Println("Stopping the file server")
	testServer.Stop()
	if err := os.RemoveAll(testServer.Root()); err != nil {
		panic(fmt.Sprintf("Failed to remove storage server dir: %v", err))
	}

	fmt.Println("Stopping the test environment")
	testEnv.Stop()

	os.Exit(code)
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz1234567890")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func getEvents(objName string, annotations map[string]string) []corev1.Event {
	var result []corev1.Event
	events := &corev1.EventList{}
	_ = k8sClient.List(ctx, events)
	for _, event := range events.Items {
		if event.InvolvedObject.Name == objName {
			if annotations == nil && len(annotations) == 0 {
				result = append(result, event)
			} else {
				for ak, av := range annotations {
					if event.GetAnnotations()[ak] == av {
						result = append(result, event)
						break
					}
				}
			}
		}
	}
	return result
}

func createNamespace(name string) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	return k8sClient.Create(context.Background(), namespace)
}

func createKubeConfigSecret(namespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubeconfig",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"value.yaml": kubeConfig,
		},
	}
	return k8sClient.Create(context.Background(), secret)
}

func applyGitRepository(objKey client.ObjectKey, artifactURL, artifactRevision, artifactChecksum string) error {
	repo := &sourcev1.GitRepository{
		TypeMeta: metav1.TypeMeta{
			Kind:       sourcev1.GitRepositoryKind,
			APIVersion: sourcev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      objKey.Name,
			Namespace: objKey.Namespace,
		},
		Spec: sourcev1.GitRepositorySpec{
			URL:      "https://github.com/test/repository",
			Interval: metav1.Duration{Duration: time.Minute},
		},
	}

	status := sourcev1.GitRepositoryStatus{
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
			Revision:       artifactRevision,
			Checksum:       artifactChecksum,
			LastUpdateTime: metav1.Now(),
		},
	}

	opt := []client.PatchOption{
		client.ForceOwnership,
		client.FieldOwner("kustomize-controller"),
	}

	if err := k8sClient.Patch(context.Background(), repo, client.Apply, opt...); err != nil {
		return err
	}

	repo.ManagedFields = nil
	repo.Status = status
	if err := k8sClient.Status().Patch(context.Background(), repo, client.Apply, opt...); err != nil {
		return err
	}
	return nil
}

func createArtifact(artifactServer *testserver.ArtifactServer, fixture, path string) (string, error) {
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

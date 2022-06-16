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
	"crypto/sha256"
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
	"github.com/fluxcd/pkg/runtime/testenv"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/hashicorp/vault/api"
	"github.com/ory/dockertest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	controllerLog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	timeout                = time.Second * 30
	interval               = time.Second * 1
	reconciliationInterval = time.Second * 5
)

const vaultVersion = "1.2.2"

var (
	reconciler   *KustomizationReconciler
	k8sClient    client.Client
	testEnv      *testenv.Environment
	testServer   *testserver.ArtifactServer
	testMetricsH controller.Metrics
	ctx          = ctrl.SetupSignalHandler()
	kubeConfig   []byte
	debugMode    = os.Getenv("DEBUG_TEST") != ""
)

func runInContext(registerControllers func(*testenv.Environment), run func() error, crdPath string) error {
	var err error
	utilruntime.Must(sourcev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(kustomizev1.AddToScheme(scheme.Scheme))

	if debugMode {
		controllerLog.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(false)))
	}

	testEnv = testenv.New(testenv.WithCRDPath(crdPath))

	testServer, err = testserver.NewTempArtifactServer()
	if err != nil {
		panic(fmt.Sprintf("Failed to create a temporary storage server: %v", err))
	}
	fmt.Println("Starting the test storage server")
	testServer.Start()

	registerControllers(testEnv)

	go func() {
		fmt.Println("Starting the test environment")
		if err := testEnv.Start(ctx); err != nil {
			panic(fmt.Sprintf("Failed to start the test environment manager: %v", err))
		}
	}()
	<-testEnv.Manager.Elected()

	user, err := testEnv.AddUser(envtest.User{
		Name:   "testenv-admin",
		Groups: []string{"system:masters"},
	}, nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create testenv-admin user: %v", err))
	}

	kubeConfig, err = user.KubeConfig()
	if err != nil {
		panic(fmt.Sprintf("Failed to create the testenv-admin user kubeconfig: %v", err))
	}

	// Client with caching disabled.
	k8sClient, err = client.New(testEnv.Config, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		panic(fmt.Sprintf("Failed to create k8s client: %v", err))
	}

	// Create a Vault test instance.
	pool, resource, err := createVaultTestInstance()
	if err != nil {
		panic(fmt.Sprintf("Failed to create Vault instance: %v", err))
	}
	defer func() {
		pool.Purge(resource)
	}()

	runErr := run()

	if debugMode {
		events := &corev1.EventList{}
		_ = k8sClient.List(ctx, events)
		for _, event := range events.Items {
			fmt.Printf("%s %s \n%s\n",
				event.InvolvedObject.Name, event.GetAnnotations()["kustomize.toolkit.fluxcd.io/revision"],
				event.Message)
		}
	}

	fmt.Println("Stopping the test environment")
	if err := testEnv.Stop(); err != nil {
		panic(fmt.Sprintf("Failed to stop the test environment: %v", err))
	}

	fmt.Println("Stopping the file server")
	testServer.Stop()
	if err := os.RemoveAll(testServer.Root()); err != nil {
		panic(fmt.Sprintf("Failed to remove storage server dir: %v", err))
	}

	return runErr
}

func TestMain(m *testing.M) {
	code := 0

	runInContext(func(testEnv *testenv.Environment) {
		controllerName := "kustomize-controller"
		testMetricsH = controller.MustMakeMetrics(testEnv)
		reconciler = &KustomizationReconciler{
			ControllerName:  controllerName,
			Client:          testEnv,
			EventRecorder:   testEnv.GetEventRecorderFor(controllerName),
			MetricsRecorder: testMetricsH.MetricsRecorder,
		}
		if err := (reconciler).SetupWithManager(testEnv, KustomizationReconcilerOptions{MaxConcurrentReconciles: 4}); err != nil {
			panic(fmt.Sprintf("Failed to start KustomizationReconciler: %v", err))
		}
	}, func() error {
		code = m.Run()
		return nil
	}, filepath.Join("..", "config", "crd", "bases"))

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

func createServiceAccount(name string, namespace string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	return k8sClient.Create(context.Background(), sa)
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

func applyGitRepository(objKey client.ObjectKey, artifactName string, revision string) error {
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

	b, _ := os.ReadFile(filepath.Join(testServer.Root(), artifactName))
	checksum := fmt.Sprintf("%x", sha256.Sum256(b))

	url := fmt.Sprintf("%s/%s", testServer.URL(), artifactName)

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
			Path:           url,
			URL:            url,
			Revision:       revision,
			Checksum:       checksum,
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

func createVaultTestInstance() (*dockertest.Pool, *dockertest.Resource, error) {
	// uses a sensible default on windows (tcp/http) and linux/osx (socket)
	pool, err := dockertest.NewPool("")
	if err != nil {
		return nil, nil, fmt.Errorf("Could not connect to docker: %s", err)
	}

	// pulls an image, creates a container based on it and runs it
	resource, err := pool.Run("vault", vaultVersion, []string{"VAULT_DEV_ROOT_TOKEN_ID=secret"})
	if err != nil {
		return nil, nil, fmt.Errorf("Could not start resource: %s", err)
	}

	os.Setenv("VAULT_ADDR", fmt.Sprintf("http://127.0.0.1:%v", resource.GetPort("8200/tcp")))
	os.Setenv("VAULT_TOKEN", "secret")
	// exponential backoff-retry, because the application in the container might not be ready to accept connections yet
	if err := pool.Retry(func() error {
		cli, err := api.NewClient(api.DefaultConfig())
		if err != nil {
			return fmt.Errorf("Cannot create Vault Client: %w", err)
		}
		status, err := cli.Sys().InitStatus()
		if err != nil {
			return err
		}
		if status != true {
			return fmt.Errorf("Vault not ready yet")
		}
		if err := cli.Sys().Mount("sops", &api.MountInput{
			Type: "transit",
		}); err != nil {
			return fmt.Errorf("Cannot create Vault Transit Engine: %w", err)
		}

		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("Could not connect to docker: %w", err)
	}

	return pool, resource, nil
}

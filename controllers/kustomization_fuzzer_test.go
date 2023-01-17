//go:build gofuzz_libfuzzer
// +build gofuzz_libfuzzer

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
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/hashicorp/vault/api"
	"github.com/ory/dockertest/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	controllerLog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/testenv"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
)

var (
	doOnce       sync.Once
	reconciler   *KustomizationReconciler
	k8sClient    client.Client
	testEnv      *testenv.Environment
	testServer   *testserver.ArtifactServer
	testMetricsH controller.Metrics
	ctx          = ctrl.SetupSignalHandler()
	kubeConfig   []byte
	debugMode    = os.Getenv("DEBUG_TEST") != ""
)

const vaultVersion = "1.2.2"
const defaultBinVersion = "1.24"

//go:embed testdata/crd/*.yaml
//go:embed testdata/sops/pgp.asc
//go:embed testdata/sops/age.txt
var testFiles embed.FS

// FuzzControllers implements a fuzzer that targets the Kustomize controller.
//
// The test must ensure a valid test state around Kubernetes objects, as the
// focus is to ensure the controller behaves properly, not Kubernetes nor
// testing infrastructure.
func Fuzz_Controllers(f *testing.F) {
	f.Fuzz(func(t *testing.T, seed, fileSeed []byte) {
		// Fuzzing has to be deterministic, so that input A can be
		// associated with crash B consistently. The current approach
		// taken by go-fuzz-headers to achieve that uses the data input
		// as a buffer which is used until its end (i.e. EOF).
		//
		// The problem with this approach is when the test setup requires
		// a higher amount of input than the available in the buffer,
		// resulting in an invalid test state.
		//
		// This is currently being countered by openning two consumers on
		// the data input, and requiring at least a length of 1000 to
		// run the tests.
		//
		// During the migration to the native fuzz feature in go we should
		// review this approach.
		if len(fileSeed) < 1000 {
			return
		}
		fmt.Printf("Data input length: %d\n", len(fileSeed))

		fc := fuzz.NewConsumer(seed)
		ff := fuzz.NewConsumer(fileSeed)

		doOnce.Do(func() {
			if err := ensureDependencies(); err != nil {
				panic(fmt.Sprintf("Failed to ensure dependencies: %v", err))
			}
		})

		err := runInContext(func(testEnv *testenv.Environment) {
			controllerName := "kustomize-controller"
			reconciler := &KustomizationReconciler{
				ControllerName: controllerName,
				Client:         testEnv,
			}
			if err := (reconciler).SetupWithManager(testEnv, KustomizationReconcilerOptions{
				MaxConcurrentReconciles: 1,
				RateLimiter:             controller.GetDefaultRateLimiter(),
			}); err != nil {
				panic(fmt.Sprintf("Failed to start KustomizationReconciler: %v", err))
			}
		}, func() error {
			dname, err := os.MkdirTemp("", "artifact-dir")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dname)

			if err = createFiles(ff, dname); err != nil {
				return err
			}

			namespaceName, err := randStringRange(fc, 1, 63)
			if err != nil {
				return err
			}

			namespace, err := createNamespaceForFuzzing(namespaceName)
			if err != nil {
				return err
			}
			defer k8sClient.Delete(context.Background(), namespace)

			fmt.Println("createKubeConfigSecretForFuzzing...")
			secret, err := createKubeConfigSecretForFuzzing(namespaceName)
			if err != nil {
				fmt.Println(err)
				return err
			}
			defer k8sClient.Delete(context.Background(), secret)

			artifactFile, err := randStringRange(fc, 1, 63)
			if err != nil {
				return err
			}
			fmt.Println("createArtifact...")
			artifactChecksum, err := createArtifact(testServer, dname, artifactFile)
			if err != nil {
				fmt.Println(err)
				return err
			}
			repName, err := randStringRange(fc, 1, 63)
			if err != nil {
				return err
			}
			repositoryName := types.NamespacedName{
				Name:      repName,
				Namespace: namespaceName,
			}
			fmt.Println("ApplyGitRepository...")
			err = applyGitRepository(repositoryName, artifactFile, "main/"+artifactChecksum)
			if err != nil {
				return err
			}
			pgpKey, err := testFiles.ReadFile("testdata/sops/pgp.asc")
			if err != nil {
				return err
			}
			ageKey, err := testFiles.ReadFile("testdata/sops/age.txt")
			if err != nil {
				return err
			}
			sskName, err := randStringRange(fc, 1, 63)
			if err != nil {
				return err
			}
			sopsSecretKey := types.NamespacedName{
				Name:      sskName,
				Namespace: namespaceName,
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

			if err = k8sClient.Create(context.Background(), sopsSecret); err != nil {
				return err
			}
			defer k8sClient.Delete(context.Background(), sopsSecret)

			kkName, err := randStringRange(fc, 1, 63)
			if err != nil {
				return err
			}
			kustomizationKey := types.NamespacedName{
				Name:      kkName,
				Namespace: namespaceName,
			}
			kustomization := &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kustomizationKey.Name,
					Namespace: kustomizationKey.Namespace,
				},
				Spec: kustomizev1.KustomizationSpec{
					Path: "./",
					KubeConfig: &meta.KubeConfigReference{
						SecretRef: meta.SecretKeyReference{
							Name: "kubeconfig",
						},
					},
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Name:      repositoryName.Name,
						Namespace: repositoryName.Namespace,
						Kind:      sourcev1.GitRepositoryKind,
					},
					Decryption: &kustomizev1.Decryption{
						Provider: "sops",
						SecretRef: &meta.LocalObjectReference{
							Name: sopsSecretKey.Name,
						},
					},
					TargetNamespace: namespaceName,
				},
			}

			if err = k8sClient.Create(context.TODO(), kustomization); err != nil {
				return err
			}

			// ensure reconciliation of the kustomization above took place before moving on
			time.Sleep(10 * time.Second)

			if err = k8sClient.Delete(context.TODO(), kustomization); err != nil {
				return err
			}

			// ensure the deferred deletion of all objects (namespace, secret, sopSecret) and
			// the kustomization above were reconciled before moving on. This avoids unneccessary
			// errors whilst tearing down the testing infrastructure.
			time.Sleep(10 * time.Second)

			return nil
		}, "testdata/crd")

		if err != nil {
			fmt.Println(err)
		}
	})
}

// Allows the fuzzer to create a random lowercase string within a given range
func randStringRange(f *fuzz.ConsumeFuzzer, minLen, maxLen int) (string, error) {
	stringLength, err := f.GetInt()
	if err != nil {
		return "", err
	}
	len := stringLength % maxLen
	if len < minLen {
		len += minLen
	}
	return f.GetStringFrom(string(letterRunes), len)
}

// Creates a namespace and returns the created object.
func createNamespaceForFuzzing(name string) (*corev1.Namespace, error) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	err := k8sClient.Create(context.Background(), namespace)
	if err != nil {
		return namespace, err
	}
	return namespace, nil
}

// Creates a secret and returns the created object.
func createKubeConfigSecretForFuzzing(namespace string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubeconfig",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"value.yaml": kubeConfig,
		},
	}
	err := k8sClient.Create(context.Background(), secret)
	if err != nil {
		return secret, err
	}
	return secret, nil
}

// Creates pseudo-random files in rootDir.
// Will create subdirs and place the files there.
// It is the callers responsibility to ensure that
// rootDir exists.
//
// Original source:
// https://github.com/AdaLogics/go-fuzz-headers/blob/9f22f86e471065b8d56861991dc885e27b1ae7de/consumer.go#L345
//
// The change assures that as long as the f buffer has
// enough length to set numberOfFiles and the first fileName,
// this is returned without errors.
// Effectively making subDir and FileContent optional.
func createFiles(f *fuzz.ConsumeFuzzer, rootDir string) error {
	noOfCreatedFiles := 0
	numberOfFiles, err := f.GetInt()
	if err != nil {
		return err
	}
	maxNoFiles := numberOfFiles % 10000 //

	for i := 0; i <= maxNoFiles; i++ {
		fileName, err := f.GetString()
		if err != nil {
			if noOfCreatedFiles > 0 {
				return nil
			} else {
				return errors.New("Could not get fileName")
			}
		}
		if fileName == "" {
			continue
		}

		// leave subDir empty if no more strings are available.
		subDir, _ := f.GetString()

		// Avoid going outside the root dir
		if strings.Contains(subDir, "../") || (len(subDir) > 0 && subDir[0] == 47) || strings.Contains(subDir, "\\") {
			continue // continue as this is not a permanent error
		}

		dirPath, err := securejoin.SecureJoin(rootDir, subDir)
		if err != nil {
			continue // some errors here are not permanent, so we can try again with different values
		}

		err = os.MkdirAll(dirPath, 0o755)
		if err != nil {
			if noOfCreatedFiles > 0 {
				return nil
			} else {
				return errors.New("Could not create the subDir")
			}
		}
		fullFilePath, err := securejoin.SecureJoin(dirPath, fileName)
		if err != nil {
			continue // potentially not a permanent error
		}

		// leave fileContents empty if no more bytes are available,
		// afterall empty files is a valid test case.
		fileContents, _ := f.GetBytes()

		createdFile, err := os.Create(fullFilePath)
		if err != nil {
			createdFile.Close()
			continue // some errors here are not permanent, so we can try again with different values
		}

		_, err = createdFile.Write(fileContents)
		if err != nil {
			createdFile.Close()
			if noOfCreatedFiles > 0 {
				return nil
			} else {
				return errors.New("Could not write the file")
			}
		}

		createdFile.Close()
		noOfCreatedFiles++
	}
	return nil
}

// ensureDependencies ensure that:
// a) setup-envtest is installed and a specific version of envtest is deployed.
// b) the embedded crd files are exported onto the "runner container".
//
// The steps above are important as the fuzzers tend to be built in an
// environment (or container) and executed in other.
func ensureDependencies() error {
	// only install dependencies when running inside a container
	if _, err := os.Stat("/.dockerenv"); os.IsNotExist(err) {
		return nil
	}

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		binVersion := envtestBinVersion()
		cmd := exec.Command("/usr/bin/bash", "-c", fmt.Sprintf(`go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest && \
		/root/go/bin/setup-envtest use -p path %s`, binVersion))

		cmd.Env = append(os.Environ(), "GOPATH=/root/go")
		assetsPath, err := cmd.Output()
		if err != nil {
			return err
		}
		os.Setenv("KUBEBUILDER_ASSETS", string(assetsPath))
	}

	// Output all embedded testdata files
	// "testdata/sops" does not need to be saved in disk
	// as it is being consumed directly from the embed.FS.
	embedDirs := []string{"testdata/crd"}
	for _, dir := range embedDirs {
		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			return fmt.Errorf("mkdir %s: %v", dir, err)
		}

		templates, err := fs.ReadDir(testFiles, dir)
		if err != nil {
			return fmt.Errorf("reading embedded dir: %v", err)
		}

		for _, template := range templates {
			fileName := fmt.Sprintf("%s/%s", dir, template.Name())
			fmt.Println(fileName)

			data, err := testFiles.ReadFile(fileName)
			if err != nil {
				return fmt.Errorf("reading embedded file %s: %v", fileName, err)
			}

			os.WriteFile(fileName, data, 0o644)
			if err != nil {
				return fmt.Errorf("writing %s: %v", fileName, err)
			}
		}
	}

	return nil
}

func envtestBinVersion() string {
	if binVersion := os.Getenv("ENVTEST_BIN_VERSION"); binVersion != "" {
		return binVersion
	}
	return defaultBinVersion
}

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

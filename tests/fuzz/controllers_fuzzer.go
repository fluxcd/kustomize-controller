//go:build gofuzz
// +build gofuzz

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
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"embed"
	"io/fs"

	securejoin "github.com/cyphar/filepath-securejoin"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/kustomize-controller/controllers"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/testenv"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
)

var doOnce sync.Once

const defaultBinVersion = "1.23"

func envtestBinVersion() string {
	if binVersion := os.Getenv("ENVTEST_BIN_VERSION"); binVersion != "" {
		return binVersion
	}
	return defaultBinVersion
}

//go:embed testdata/crd/*.yaml
//go:embed testdata/sops/pgp.asc
//go:embed testdata/sops/age.txt
var testFiles embed.FS

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

// FuzzControllers implements a fuzzer that targets the Kustomize controller.
//
// The test must ensure a valid test state around Kubernetes objects, as the
// focus is to ensure the controller behaves properly, not Kubernetes nor
// testing infrastructure.
func FuzzControllers(data []byte) int {
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
	if len(data) < 1000 {
		return 0
	}
	fmt.Printf("Data input length: %d\n", len(data))

	f := fuzz.NewConsumer(data)
	ff := fuzz.NewConsumer(data)

	doOnce.Do(func() {
		if err := ensureDependencies(); err != nil {
			panic(fmt.Sprintf("Failed to ensure dependencies: %v", err))
		}
	})

	err := runInContext(func(testEnv *testenv.Environment) {
		controllerName := "kustomize-controller"
		reconciler := &controllers.KustomizationReconciler{
			ControllerName: controllerName,
			Client:         testEnv,
		}
		if err := (reconciler).SetupWithManager(testEnv, controllers.KustomizationReconcilerOptions{MaxConcurrentReconciles: 1}); err != nil {
			panic(fmt.Sprintf("Failed to start GitRepositoryReconciler: %v", err))
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

		namespaceName, err := randStringRange(f, 1, 63)
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

		artifactFile, err := randStringRange(f, 1, 63)
		if err != nil {
			return err
		}
		fmt.Println("createArtifact...")
		artifactChecksum, err := createArtifact(testServer, dname, artifactFile)
		if err != nil {
			fmt.Println(err)
			return err
		}
		repName, err := randStringRange(f, 1, 63)
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
		sskName, err := randStringRange(f, 1, 63)
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

		kkName, err := randStringRange(f, 1, 63)
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
				KubeConfig: &kustomizev1.KubeConfig{
					SecretRef: meta.LocalObjectReference{
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
		return 0
	}

	return 1
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

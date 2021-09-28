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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"path/filepath"
	"strings"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"k8s.io/apimachinery/pkg/types"
	"github.com/fluxcd/pkg/apis/meta"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
)


var cfg *rest.Config
var k8sClient client.Client
var k8sManager ctrl.Manager
var testEnv *envtest.Environment
var kubeConfig []byte
var testServer *testserver.ArtifactServer
var	testEventsH  controller.Events
var	testMetricsH controller.Metrics
var	ctx = ctrl.SetupSignalHandler()
var initter sync.Once
var runningInOssfuzz = false
var (
	localCRDpath = []string{filepath.Join("..", "config", "crd", "bases")}

	// Variables for the OSS-fuzz environment:
	downloadLink = "https://storage.googleapis.com/kubebuilder-tools/kubebuilder-tools-1.19.2-linux-amd64.tar.gz"
	downloadPath = "/tmp/envtest-bins.tar.gz"
	binariesDir  = "/tmp/test-binaries"
	ossFuzzCrdPath = []string{filepath.Join(".", "bases")}
	ossFuzzCrdYaml = "https://raw.githubusercontent.com/fluxcd/kustomize-controller/main/config/crd/bases/kustomize.toolkit.fluxcd.io_kustomizations.yaml"
	ossFuzzGitYaml = "https://raw.githubusercontent.com/fluxcd/source-controller/v0.15.4/config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml"
	ossFuzzBucYaml = "https://raw.githubusercontent.com/fluxcd/source-controller/v0.15.4/config/crd/bases/source.toolkit.fluxcd.io_buckets.yaml"
	crdPath []string
	pgpAscFile = "https://raw.githubusercontent.com/fluxcd/kustomize-controller/main/controllers/testdata/sops/pgp.asc"
	ageTxtFile = "https://raw.githubusercontent.com/fluxcd/kustomize-controller/main/controllers/testdata/sops/age.txt"
)

// createKUBEBUILDER_ASSETS runs "setup-envtest use" and
// returns the path of the 3 binaries that are created.
// If this one fails, it means that setup-envtest is not
// available in the runtime environment, and that means
// that the fuzzer is being run by OSS-fuzz. In that case
// we set "runningInOssfuzz" to true which will later trigger
// download of all required files so the OSS-fuzz can run
// the fuzzer.
func createKUBEBUILDER_ASSETS() string {
	out, err := exec.Command("setup-envtest", "use").Output()
	if err != nil {
		// If there is an error here, the fuzzer is running
		// in OSS-fuzz where the binary setup-envtest is
		// not available, so we have to get them in an
		// alternative way
		runningInOssfuzz = true
		return ""
	}

	// split the output:
	splitString := strings.Split(string(out), " ")
	binPath := strings.TrimSuffix(splitString[len(splitString)-1], "\n")
	if err != nil {
		panic(err)
	}
	return binPath
}

// Downloads a file to a path. This is only needed when 
// the fuzzer is running in OSS-fuzz. 
func DownloadFile(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

// When OSS-fuzz runs the fuzzer a few files need to
// be download during initialization. No files from the
// kustomize-controller repo are available at runtime,
// and each of the files that are needed must be downloaded
// manually.
func downloadFilesForOssFuzz() error {	
	err := DownloadFile(downloadPath, downloadLink)
	if err != nil {
		return err
	}
	err = os.MkdirAll(binariesDir, 0777)
	if err != nil {
		return err
	}
	cmd := exec.Command("tar", "xvf", downloadPath, "-C", binariesDir)
	err = cmd.Run()
	if err != nil {
		return err
	}

	// Download crds
	err = os.MkdirAll("bases", 0777)
	if err != nil {
		return err
	}
	err = DownloadFile("./bases/kustomize.toolkit.fluxcd.io_kustomizations.yaml", ossFuzzCrdYaml)
	if err != nil {
		return err
	}
	err = DownloadFile("./bases/source.toolkit.fluxcd.io_gitrepositories.yaml", ossFuzzGitYaml)
	if err != nil {
		return err
	}
	err = DownloadFile("./bases/source.toolkit.fluxcd.io_buckets.yaml", ossFuzzBucYaml)
	if err != nil {
		return err
	}

	// Download sops files
	err = os.MkdirAll("testdata/sops", 0777)
	if err != nil {
		return err
	}
	err = DownloadFile("./testdata/sops/pgp.asc", pgpAscFile)
	if err != nil {
		return err
	}
	err = DownloadFile("./testdata/sops/age.txt", ageTxtFile)
	if err != nil {
		return err
	}
	return nil
}

// customInit implements an init function that
// is invoked by way of sync.Do
func customInit() {
	kubebuilder_assets := createKUBEBUILDER_ASSETS()
	os.Setenv("KUBEBUILDER_ASSETS", kubebuilder_assets)

	// Set up things for fuzzing in the OSS-fuzz environment:
	if runningInOssfuzz {
		err := downloadFilesForOssFuzz()
		if err != nil {
			panic(err)
		}
		os.Setenv("KUBEBUILDER_ASSETS", binariesDir+"/kubebuilder/bin")
	    crdPath = ossFuzzCrdPath
		runningInOssfuzz = false
	} else {
		crdPath = localCRDpath
	}

	var err error
	err = sourcev1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
	err = kustomizev1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: crdPath,
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

	if err := (&KustomizationReconciler{
		Client:          k8sManager.GetClient(),
	}).SetupWithManager(k8sManager, KustomizationReconcilerOptions{MaxConcurrentReconciles: 1}); err != nil {
		panic(fmt.Sprintf("Failed to start GitRepositoryReconciler: %v", err))
	}
	time.Sleep(1*time.Second)

	go func() {
		fmt.Println("Starting the test environment")
		if err := k8sManager.Start(ctx); err != nil {
			panic(fmt.Sprintf("Failed to start the test environment manager: %v", err))
		}
	}()
	time.Sleep(time.Second*1)
	<-k8sManager.Elected()
}

// Fuzz implements the fuzz harness
func Fuzz(data []byte) int {
	initter.Do(customInit)
	f := fuzz.NewConsumer(data)
	dname, err := os.MkdirTemp("", "artifact-dir")
    if err != nil {
    	return 0
    }
    defer os.RemoveAll(dname)
    err = f.CreateFiles(dname)
    if err != nil {
    	return 0
    }
	id, err := randString(f)
	if err != nil {
		return 0
	}
	namespace, err := createNamespaceForFuzzing(id)
	if err != nil {
		return 0
	}
	defer k8sClient.Delete(context.Background(), namespace)

	fmt.Println("createKubeConfigSecretForFuzzing...")
	secret, err := createKubeConfigSecretForFuzzing(id)
	if err != nil {
		fmt.Println(err)
		return 0
	}
	defer k8sClient.Delete(context.Background(), secret)
	
	artifactFile, err := randString(f)
	if err != nil {
		return 0
	}
	fmt.Println("createArtifact...")
	artifactChecksum, err := createArtifact(testServer, dname, artifactFile)
	if err != nil {
		fmt.Println(err)
		return 0
	}
	_ = artifactChecksum
	fmt.Println("testServer.URLForFile...")
	artifactURL, err := testServer.URLForFile(artifactFile)
	if err != nil {
		fmt.Println("URLForFile error: ", err)
		return 0
	}
	_ = artifactURL
	repName, err := randString(f)
	if err != nil {
		return 0
	}
	repositoryName := types.NamespacedName{
		Name:      repName,
		Namespace: id,
	}
	fmt.Println("applyGitRepository...")
	err = applyGitRepository(repositoryName, artifactURL, "main/"+artifactChecksum, artifactChecksum)
	if err != nil {
		fmt.Println(err)
	}
	pgpKey, err := ioutil.ReadFile("testdata/sops/pgp.asc")
	if err != nil {
		return 0
	}
	ageKey, err := ioutil.ReadFile("testdata/sops/age.txt")
	if err != nil {
		return 0
	}
	sskName, err := randString(f)
	if err != nil {
		return 0
	}
	sopsSecretKey := types.NamespacedName{
		Name:      sskName,
		Namespace: id,
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
	err = k8sClient.Create(context.Background(), sopsSecret)
	if err != nil {
		return 0
	}
	defer k8sClient.Delete(context.Background(), sopsSecret)

	kkName, err := randString(f)
	if err != nil {
		return 0
	}
	kustomizationKey := types.NamespacedName{
		Name:      kkName,
		Namespace: id,
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
			TargetNamespace: id,
		},
	}

	err = k8sClient.Create(context.TODO(), kustomization)
	if err != nil {
		fmt.Println(err)
		return 0
	}
	defer k8sClient.Delete(context.TODO(), kustomization)
	return 1
}

// Allows the fuzzer to create a random lowercase string
func randString(f *fuzz.ConsumeFuzzer) (string, error) {
	stringLength, err := f.GetInt()
	if err != nil {
		return "", err
	}
	maxLength := 50
	var buffer bytes.Buffer
	for i:=0;i<stringLength%maxLength;i++ {
		getByte, err := f.GetByte()
		if err != nil {
			return "", nil
		}
		if getByte < 'a' || getByte > 'z' {
			return "", errors.New("Not a good char")
		}
		buffer.WriteByte(getByte)
	}
	return buffer.String(), nil
}

// Creates a namespace.
// Caller must delete the created namespace.
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

// Creates a secret.
// Caller must delete the created secret.
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

// taken from https://github.com/fluxcd/kustomize-controller/blob/main/controllers/suite_test.go#L222
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

// Taken from https://github.com/fluxcd/kustomize-controller/blob/main/controllers/suite_test.go#L171
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
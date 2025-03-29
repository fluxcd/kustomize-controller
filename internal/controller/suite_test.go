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

package controller

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/vault/api"
	"github.com/opencontainers/go-digest"
	"github.com/ory/dockertest/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	controllerLog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	kcheck "github.com/fluxcd/pkg/runtime/conditions/check"
	"github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/metrics"
	"github.com/fluxcd/pkg/runtime/testenv"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

const (
	timeout                = time.Second * 30
	interval               = time.Second * 1
	reconciliationInterval = time.Second * 5
	vaultVersion           = "1.13.2"
	overrideManagerName    = "node-fetch"
)

var (
	reconciler             *KustomizationReconciler
	k8sClient              client.Client
	testEnv                *testenv.Environment
	testServer             *testserver.ArtifactServer
	testMetricsH           controller.Metrics
	ctx                    = ctrl.SetupSignalHandler()
	kubeConfig             []byte
	kstatusCheck           *kcheck.Checker
	kstatusInProgressCheck *kcheck.Checker
	debugMode              = os.Getenv("DEBUG_TEST") != ""
)

func runInContext(registerControllers func(*testenv.Environment), run func() int) (code int) {
	var err error
	utilruntime.Must(kustomizev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(sourcev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(sourcev1b2.AddToScheme(scheme.Scheme))

	if debugMode {
		controllerLog.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(false)))
	}

	testEnv = testenv.New(
		testenv.WithCRDPath(filepath.Join("..", "..", "config", "crd", "bases")),
		testenv.WithMaxConcurrentReconciles(4),
	)

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

	code = run()

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

	return code
}

func TestMain(m *testing.M) {
	code := runInContext(func(testEnv *testenv.Environment) {
		controllerName := "kustomize-controller"
		testMetricsH = controller.NewMetrics(testEnv, metrics.MustMakeRecorder(), kustomizev1.KustomizationFinalizer)
		kstatusCheck = kcheck.NewChecker(testEnv.Client,
			&kcheck.Conditions{
				NegativePolarity: []string{meta.StalledCondition, meta.ReconcilingCondition},
			})
		// Disable fetch for the in-progress kstatus checker so that it can be
		// asked to evaluate snapshot of an object. This is needed to prevent
		// the object status from changing right before the checker fetches it
		// for inspection.
		kstatusInProgressCheck = kcheck.NewInProgressChecker(testEnv.Client)
		kstatusInProgressCheck.DisableFetch = true
		reconciler = &KustomizationReconciler{
			ControllerName:          controllerName,
			Client:                  testEnv,
			Mapper:                  testEnv.GetRESTMapper(),
			APIReader:               testEnv,
			EventRecorder:           testEnv.GetEventRecorderFor(controllerName),
			Metrics:                 testMetricsH,
			ConcurrentSSA:           4,
			DisallowedFieldManagers: []string{overrideManagerName},
		}
		if err := (reconciler).SetupWithManager(ctx, testEnv, KustomizationReconcilerOptions{
			DependencyRequeueInterval: 2 * time.Second,
			EnableDependencyQueueing:  true,
		}); err != nil {
			panic(fmt.Sprintf("Failed to start KustomizationReconciler: %v", err))
		}
	}, m.Run)

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

func isReconcileRunning(k *kustomizev1.Kustomization) bool {
	return conditions.IsReconciling(k) &&
		conditions.GetReason(k, meta.ReconcilingCondition) != meta.ProgressingWithRetryReason
}

func isReconcileSuccess(k *kustomizev1.Kustomization) bool {
	return conditions.IsReady(k) &&
		conditions.GetObservedGeneration(k, meta.ReadyCondition) == k.Generation &&
		k.Status.ObservedGeneration == k.Generation &&
		k.Status.LastAppliedRevision == k.Status.LastAttemptedRevision
}

func isReconcileFailure(k *kustomizev1.Kustomization) bool {
	if conditions.IsStalled(k) {
		return true
	}

	isHandled := true
	if v, ok := meta.ReconcileAnnotationValue(k.GetAnnotations()); ok {
		isHandled = k.Status.LastHandledReconcileAt == v
	}

	return isHandled && conditions.IsReconciling(k) &&
		conditions.IsFalse(k, meta.ReadyCondition) &&
		conditions.GetObservedGeneration(k, meta.ReadyCondition) == k.Generation &&
		conditions.GetReason(k, meta.ReconcilingCondition) == meta.ProgressingWithRetryReason
}

func logStatus(t *testing.T, k *kustomizev1.Kustomization) {
	sts, _ := yaml.Marshal(k.Status)
	t.Log(string(sts))
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

type gitRepoOption func(*gitRepoOptions)

type gitRepoOptions struct {
	artifactMetadata map[string]string
}

func withGitRepoArtifactMetadata(k, v string) gitRepoOption {
	return func(o *gitRepoOptions) {
		if o.artifactMetadata == nil {
			o.artifactMetadata = make(map[string]string)
		}
		o.artifactMetadata[k] = v
	}
}

func applyGitRepository(objKey client.ObjectKey, artifactName string,
	revision string, opts ...gitRepoOption) error {

	var opt gitRepoOptions
	for _, o := range opts {
		o(&opt)
	}

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
	dig := digest.SHA256.FromBytes(b)

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
			Digest:         dig.String(),
			LastUpdateTime: metav1.Now(),
			Metadata:       opt.artifactMetadata,
		},
	}

	patchOpts := []client.PatchOption{
		client.ForceOwnership,
		client.FieldOwner("kustomize-controller"),
	}

	if err := k8sClient.Patch(context.Background(), repo, client.Apply, patchOpts...); err != nil {
		return err
	}

	repo.ManagedFields = nil
	repo.Status = status

	statusOpts := &client.SubResourcePatchOptions{
		PatchOptions: client.PatchOptions{
			FieldManager: "source-controller",
		},
	}

	if err := k8sClient.Status().Patch(context.Background(), repo, client.Apply, statusOpts); err != nil {
		return err
	}
	return nil
}

func createVaultTestInstance() (*dockertest.Pool, *dockertest.Resource, error) {
	// uses a sensible default on windows (tcp/http) and linux/osx (socket)
	pool, err := dockertest.NewPool("")
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	// pulls an image, creates a container based on it and runs it
	resource, err := pool.Run("vault", vaultVersion, []string{"VAULT_DEV_ROOT_TOKEN_ID=secret"})
	if err != nil {
		return nil, nil, fmt.Errorf("could not start resource: %s", err)
	}

	os.Setenv("VAULT_ADDR", fmt.Sprintf("http://127.0.0.1:%v", resource.GetPort("8200/tcp")))
	os.Setenv("VAULT_TOKEN", "secret")
	// exponential backoff-retry, because the application in the container might not be ready to accept connections yet
	if err := pool.Retry(func() error {
		cli, err := api.NewClient(api.DefaultConfig())
		if err != nil {
			return fmt.Errorf("cannot create Vault Client: %w", err)
		}
		status, err := cli.Sys().InitStatus()
		if err != nil {
			return err
		}
		if status != true {
			return fmt.Errorf("vault not ready yet")
		}
		if err := cli.Sys().Mount("sops", &api.MountInput{
			Type: "transit",
		}); err != nil {
			return fmt.Errorf("cannot create Vault Transit Engine: %w", err)
		}

		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("could not connect to docker: %w", err)
	}

	return pool, resource, nil
}
